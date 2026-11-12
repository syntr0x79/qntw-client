//go:build darwin && !ios

package clienttinc

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	macnet "github.com/syntr0x79/qntw-client/mobile/macos/network"
	"net"
	"net/netip"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/syntr0x79/qntw-client/internal/agentproto"
	wire "github.com/syntr0x79/qntw-client/internal/clienttincwire"
	"github.com/syntr0x79/qntw-client/mobile/tincbridge"
	"github.com/xtls/xray-core/app/proxyman"
	"github.com/xtls/xray-core/common/platform"
	"github.com/xtls/xray-core/common/serial"
	"github.com/xtls/xray-core/core"
	_ "github.com/xtls/xray-core/main/distro/all"
	"github.com/xtls/xray-core/transport/internet"
	"golang.org/x/sys/unix"
	"google.golang.org/protobuf/proto"
)

func identity(ctx context.Context, ctl, dir string) (string, error) {
	if e := os.MkdirAll(filepath.Join(dir, "hosts"), 0700); e != nil {
		return "", e
	}
	if e := privateIdentityDirectories(dir); e != nil {
		return "", e
	}
	path := filepath.Join(dir, "public-key")
	if b, e := os.ReadFile(path); e == nil {
		key := strings.TrimSpace(string(b))
		if !agentproto.ValidTincPubkey(key) {
			return "", errors.New("invalid saved tinc public key")
		}
		return key, nil
	}
	if _, e := os.Stat(filepath.Join(dir, "ed25519_key.priv")); e == nil {
		return "", errors.New("tinc identity exists without its saved public key; refusing to rotate")
	}
	_, initErr := command(ctx, "", ctl, "-c", dir, "init", "bootstrap")
	if e := privateIdentityDirectories(dir); e != nil {
		return "", e
	}
	if initErr != nil {
		return "", errors.New("native tinc identity generation failed")
	}
	b, e := os.ReadFile(filepath.Join(dir, "hosts", "bootstrap"))
	if e != nil {
		return "", e
	}
	for _, line := range strings.Split(string(b), "\n") {
		key, value, ok := strings.Cut(line, "=")
		if ok && strings.EqualFold(strings.TrimSpace(key), "Ed25519PublicKey") {
			public := strings.TrimSpace(value)
			if !agentproto.ValidTincPubkey(public) {
				break
			}
			if e = os.WriteFile(path, []byte(public+"\n"), 0600); e != nil {
				return "", e
			}
			return public, nil
		}
	}
	return "", errors.New("native tinc did not produce a valid public key")
}

func renderNative(dir, public string, c wire.ClientConfig) error {
	for _, name := range []string{"tinc-up", "tinc-down", "host-up", "host-down", "subnet-up", "subnet-down"} {
		if e := os.Remove(filepath.Join(dir, name)); e != nil && !errors.Is(e, os.ErrNotExist) {
			return e
		}
	}
	entries, e := os.ReadDir(filepath.Join(dir, "hosts"))
	if e != nil {
		return e
	}
	for _, entry := range entries {
		if entry.IsDir() {
			return errors.New("unexpected nested host directory")
		}
		if e = os.Remove(filepath.Join(dir, "hosts", entry.Name())); e != nil {
			return e
		}
	}
	var conf strings.Builder
	// Keep the Linux prototype on authenticated TCP meta links until mixed
	// UDP/TCP data-session recovery is validated across NAT and VM pauses.
	// Inner VLESS still carries user TCP and UDP; this only selects tinc's
	// outer transport. A short VM scheduling delay must not expire a handshake.
	// tinc clamps PingTimeout to PingInterval, so both must be at least 15s.
	fmt.Fprintf(&conf, "Name = %s\nDeviceType = fd\nDevice = 3\nInterface = qtm0\nMode = router\nAddressFamily = ipv4\nPort = 0\nTCPOnly = yes\nAutoConnect = no\nLocalDiscovery = no\nBroadcast = no\nStrictSubnets = yes\nForwarding = off\nExperimentalProtocol = yes\nPingInterval = 15\nPingTimeout = 15\nMaxTimeout = 10\n", c.Self.Name)
	self := fmt.Sprintf("Ed25519PublicKey = %s\nSubnet = %s/32\nIndirectData = yes\n", public, c.Self.Address)
	if e = os.WriteFile(filepath.Join(dir, "hosts", c.Self.Name), []byte(self), 0600); e != nil {
		return e
	}
	for _, in := range c.Ingresses {
		endpoint := netip.MustParseAddrPort(in.Endpoint)
		host := fmt.Sprintf("Ed25519PublicKey = %s\nAddress = %s\nPort = %d\nSubnet = %s/32\nSubnet = %s#%d\nIndirectData = yes\n", in.PublicKey, endpoint.Addr(), endpoint.Port(), in.Address, in.RoutePrefix, in.Weight)
		if e = os.WriteFile(filepath.Join(dir, "hosts", in.Name), []byte(host), 0600); e != nil {
			return e
		}
		fmt.Fprintf(&conf, "ConnectTo = %s\n", in.Name)
	}
	return os.WriteFile(filepath.Join(dir, "tinc.conf"), []byte(conf.String()), 0600)
}

type dataPath struct {
	overlay      *tunDevice
	guard        *tincbridge.SocketGuard
	cmd          *exec.Cmd
	done         chan struct{}
	xray         *core.Instance
	dir          string
	bridge       *os.File
	overlayRoute bool
	systemFD     *os.File
	dialer       *assignedDialer
}

func startPath(ctx context.Context, n *network, daemon, dir, public string, c wire.ClientConfig) (p *dataPath, err error) {
	p = &dataPath{dir: dir}
	defer func(candidate *dataPath) {
		if err != nil {
			candidate.close()
		}
	}(p)
	physicalIPs := make([]string, 0, len(c.Ingresses))
	for _, peer := range c.Ingresses {
		physicalIPs = append(physicalIPs, netip.MustParseAddrPort(peer.Endpoint).Addr().String())
	}
	if err = n.session.AllowPhysicalIPs(ctx, physicalIPs); err != nil {
		return nil, err
	}
	if err = renderNative(dir, public, c); err != nil {
		return nil, err
	}
	p.overlay, err = newTUN("qtm0")
	if err != nil {
		return nil, err
	}
	if err = macnet.Configure(ctx, p.overlay.device, c.Self.Address); err != nil {
		return nil, err
	}
	if err = macnet.Route(ctx, "add", "10.76.0.0/24", p.overlay.name); err != nil {
		return nil, err
	}
	p.overlayRoute = true
	// tinc's fd driver expects raw IP; utun includes a four-byte AF header.
	pair, e := unix.Socketpair(unix.AF_UNIX, unix.SOCK_DGRAM, 0)
	if e != nil {
		return nil, e
	}
	// Absorb normal bursts in both directions; transient ENOBUFS is still
	// handled by the bridge rather than terminating its forwarding goroutine.
	for _, fd := range pair {
		if err = unix.SetsockoptInt(fd, unix.SOL_SOCKET, unix.SO_RCVBUF, 256<<10); err != nil {
			unix.Close(pair[0])
			unix.Close(pair[1])
			return nil, err
		}
	}
	unix.SetNonblock(pair[0], true)
	unix.CloseOnExec(pair[0])
	unix.CloseOnExec(pair[1])
	p.bridge = os.NewFile(uintptr(pair[0]), "tinc-bridge")
	nativeFD := os.NewFile(uintptr(pair[1]), "tinc-ip")
	defer nativeFD.Close()
	go bridgeUTUNObserved(p.overlay.file, p.bridge, n.session.RecordBridgeError)
	p.guard, err = tincbridge.ListenSocketGuard(dir, n.prepare)
	if err != nil {
		return nil, err
	}
	p.cmd = exec.Command(daemon, "-D", "-c", dir, "--pidfile="+filepath.Join(dir, "tinc.pid"), "--logfile="+filepath.Join(dir, "tinc.log"))
	p.cmd.Env = []string{"PATH=/usr/bin:/bin:/usr/sbin:/sbin"}
	p.cmd.ExtraFiles = []*os.File{nativeFD}
	p.cmd.Env = append(p.cmd.Env, tincbridge.GuardEnvironment+"="+p.guard.Path())
	if err = p.cmd.Start(); err != nil {
		return nil, errors.New("native tinc start failed")
	}
	n.session.ObserveNative(p.cmd.Process.Pid, p.overlay.name, c.Assignment.Address)
	p.done = make(chan struct{})
	go func() { p.cmd.Wait(); close(p.done) }()
	doc := xrayDocument(c, n.system != nil)
	config, e := core.LoadConfig("json", strings.NewReader(string(doc)))
	if e != nil {
		return nil, errors.New("cannot build client tunnel configuration")
	}
	if n.system != nil {
		fd, e := unix.Dup(int(n.system.file.Fd()))
		if e != nil {
			return nil, e
		}
		p.systemFD = os.NewFile(uintptr(fd), "xray-utun")
		os.Setenv(platform.TunFdKey, strconv.Itoa(fd))
		defer os.Unsetenv(platform.TunFdKey)
	}
	p.dialer = newAssignedDialer(netip.AddrPortFrom(netip.MustParseAddr(c.Assignment.Address), c.Assignment.Port), netip.MustParseAddr(c.Self.Address), func(fd int) error {
		iface, e := net.InterfaceByName(p.overlay.name)
		if e != nil {
			return e
		}
		return unix.SetsockoptInt(fd, unix.IPPROTO_IP, unix.IP_BOUND_IF, iface.Index)
	})
	p.xray, e = newAssignmentXray(config, p.dialer)
	if e != nil {
		return nil, errors.New("cannot create client tunnel")
	}
	if e = p.xray.Start(); e != nil {
		return nil, errors.New("cannot start client tunnel")
	}
	if e = n.applyDNS(ctx, c.DNS); e != nil {
		return nil, e
	}
	return p, nil
}

func newAssignmentXray(config *core.Config, owner *assignedDialer) (*core.Instance, error) {
	if owner == nil {
		return nil, errors.New("assigned socket owner is required")
	}
	ownerID, err := nextAssignedOwnerID()
	if err != nil {
		return nil, err
	}
	config, err = assignmentOwnerConfig(config, ownerID)
	if err != nil {
		return nil, err
	}
	// Each elevated process serves one fixed mode. Importing this package has
	// no effect on ordinary VPN/probe dialers; install once before first core.
	installAssignedDispatcher()
	previous, existed := os.LookupEnv(platform.UseCone)
	if err := os.Setenv(platform.UseCone, "true"); err != nil {
		return nil, err
	}
	defer func() {
		if existed {
			_ = os.Setenv(platform.UseCone, previous)
		} else {
			_ = os.Unsetenv(platform.UseCone)
		}
	}()
	instance, err := core.New(config)
	if err != nil {
		return nil, err
	}
	if err = installUDPLifecycle(instance); err != nil {
		_ = instance.Close()
		return nil, err
	}
	if err = installForwardingObservation(instance, owner.responses); err != nil {
		_ = instance.Close()
		return nil, err
	}
	if err = registerAssignedCore(instance, owner, ownerID); err != nil {
		_ = instance.Close()
		return nil, err
	}
	return instance, nil
}

func assignmentOwnerConfig(input *core.Config, ownerID int32) (*core.Config, error) {
	if input == nil || ownerID <= 0 {
		return nil, errors.New("assigned tunnel configuration is missing its owner")
	}
	config := proto.Clone(input).(*core.Config)
	count := 0
	for _, outbound := range config.Outbound {
		if outbound.Tag != "assigned" {
			continue
		}
		count++
		if outbound.SenderSettings == nil {
			return nil, errors.New("assigned tunnel has no transport settings")
		}
		settings, err := outbound.SenderSettings.GetInstance()
		if err != nil {
			return nil, errors.New("assigned tunnel transport settings are invalid")
		}
		sender, ok := settings.(*proxyman.SenderConfig)
		if !ok || sender.StreamSettings == nil {
			return nil, errors.New("assigned tunnel transport settings are missing")
		}
		if sender.StreamSettings.SocketSettings == nil {
			sender.StreamSettings.SocketSettings = &internet.SocketConfig{}
		}
		sender.StreamSettings.SocketSettings.Mark = ownerID
		outbound.SenderSettings = serial.ToTypedMessage(sender)
	}
	if count != 1 {
		return nil, errors.New("assigned tunnel must have exactly one owned outbound")
	}
	return config, nil
}
func xrayDocument(c wire.ClientConfig, system bool) []byte {
	inbounds := []any{map[string]any{"listen": "127.0.0.1", "port": 1089, "protocol": "socks", "settings": map[string]any{"auth": "noauth", "udp": true, "ip": "127.0.0.1"}}}
	if system {
		inbounds = append(inbounds, map[string]any{"tag": "system-tun", "protocol": "tun", "settings": map[string]any{"name": "qtc0", "mtu": 1280}})
	}
	doc := map[string]any{"log": map[string]any{"loglevel": "none"}, "inbounds": inbounds, "outbounds": []any{
		map[string]any{"tag": "assigned", "protocol": "vless", "mux": map[string]any{"enabled": true, "concurrency": 8, "xudpConcurrency": 0, "xudpProxyUDP443": "allow"}, "settings": map[string]any{"vnext": []any{map[string]any{"address": c.Assignment.Address, "port": c.Assignment.Port, "users": []any{map[string]any{"id": c.Assignment.VLESSUUID.String(), "encryption": "none"}}}}}, "streamSettings": map[string]any{"security": "tls", "tlsSettings": map[string]any{"serverName": c.Assignment.TLSName, "disableSystemRoot": true, "certificates": []any{map[string]any{"usage": "verify", "certificate": strings.Split(strings.TrimSpace(c.Assignment.CertificatePEM), "\n")}}}}},
		map[string]any{"tag": "block", "protocol": "blackhole"},
	}, "routing": map[string]any{"rules": []any{map[string]any{"type": "field", "ip": []string{"::/0"}, "outboundTag": "block"}, map[string]any{"type": "field", "network": "tcp,udp", "outboundTag": "assigned"}}}}
	b, _ := json.Marshal(doc)
	return b
}
func (p *dataPath) alive() bool {
	if p == nil || p.done == nil {
		return false
	}
	select {
	case <-p.done:
		return false
	default:
		return true
	}
}
func (p *dataPath) close() {
	if p == nil {
		return
	}
	if p.dialer != nil {
		_ = p.dialer.close()
		p.dialer = nil
	}
	if p.guard != nil {
		p.guard.Close()
		p.guard = nil
	}
	if p.cmd != nil && p.done != nil {
		p.cmd.Process.Signal(unix.SIGTERM)
		select {
		case <-p.done:
		case <-time.After(2 * time.Second):
			p.cmd.Process.Kill()
			<-p.done
		}
		p.done = nil
	}
	if p.bridge != nil {
		p.bridge.Close()
		p.bridge = nil
	}
	if p.overlayRoute && p.overlay != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		_ = macnet.Route(ctx, "delete", "10.76.0.0/24", p.overlay.name)
		cancel()
		p.overlayRoute = false
	}
	p.overlay.close()
	p.overlay = nil
	if p.xray != nil {
		p.xray.Close()
		p.xray = nil
	}
	if p.systemFD != nil {
		p.systemFD.Close()
		p.systemFD = nil
	}
}

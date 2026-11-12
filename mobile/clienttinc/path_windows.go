package clienttinc

import (
	"context"
	"encoding/json"
	"errors"
	"net/netip"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"
	"unsafe"

	wire "github.com/syntr0x79/qntw-client/internal/clienttincwire"
	"github.com/xtls/xray-core/app/proxyman"
	"github.com/xtls/xray-core/common/platform"
	"github.com/xtls/xray-core/common/serial"
	"github.com/xtls/xray-core/core"
	_ "github.com/xtls/xray-core/main/distro/all"
	"github.com/xtls/xray-core/transport/internet"
	"golang.org/x/sys/windows"
	"google.golang.org/protobuf/proto"
)

func windowsNativePath() string {
	p, _ := os.Executable()
	return filepath.Join(filepath.Dir(p), "tincd.exe")
}

type dataPath struct {
	n      *network
	cmd    *exec.Cmd
	done   chan struct{}
	job    windows.Handle
	xray   *core.Instance
	dialer *assignedDialer
	dir    string
}

func startPath(ctx context.Context, n *network, daemon, dir, public string, c wire.ClientConfig) (p *dataPath, err error) {
	p = &dataPath{n: n, dir: dir}
	defer func(candidate *dataPath) {
		if err != nil {
			if cleanup := candidate.close(); cleanup != nil {
				err = errors.Join(err, errPathCleanup, cleanup)
			}
		}
	}(p)
	if err = renderNative(dir, public, c); err != nil {
		return nil, err
	}
	endpoints := make([]netip.AddrPort, 0, len(c.Ingresses))
	for _, peer := range c.Ingresses {
		endpoints = append(endpoints, netip.MustParseAddrPort(peer.Endpoint))
	}
	if err = n.session.SetTincPeers(endpoints); err != nil {
		return nil, err
	}
	p.job, err = windows.CreateJobObject(nil, nil)
	if err != nil {
		return nil, err
	}
	info := windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION{}
	info.BasicLimitInformation.LimitFlags = windows.JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE
	if _, err = windows.SetInformationJobObject(p.job, windows.JobObjectExtendedLimitInformation, uintptr(unsafe.Pointer(&info)), uint32(unsafe.Sizeof(info))); err != nil {
		return nil, err
	}
	p.cmd = exec.Command(daemon, "-D", "-c", dir, "--pidfile="+filepath.Join(dir, "tinc.pid"), "--logfile="+filepath.Join(dir, "tinc.log"))
	p.cmd.Dir = filepath.Dir(daemon)
	p.cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
	p.cmd.Env, err = trustedNativeEnvironment(n.session.PhysicalIndex())
	if err != nil {
		return nil, err
	}
	if err = p.cmd.Start(); err != nil {
		return nil, errors.New("native tinc could not start")
	}
	p.done = make(chan struct{})
	go func() { _ = p.cmd.Wait(); close(p.done) }()
	process, e := windows.OpenProcess(windows.PROCESS_SET_QUOTA|windows.PROCESS_TERMINATE, false, uint32(p.cmd.Process.Pid))
	if e != nil {
		return nil, e
	}
	err = windows.AssignProcessToJobObject(p.job, process)
	windows.CloseHandle(process)
	if err != nil {
		return nil, err
	}
	if err = n.session.AttachOverlay(ctx, "qtm0", netip.MustParseAddr(c.Self.Address)); err != nil {
		return nil, err
	}
	config, e := core.LoadConfig("json", strings.NewReader(string(xrayDocument(c, n.system != nil))))
	if e != nil {
		return nil, errors.New("invalid assigned tunnel")
	}
	p.dialer = newAssignedDialer(netip.AddrPortFrom(netip.MustParseAddr(c.Assignment.Address), c.Assignment.Port), netip.MustParseAddr(c.Self.Address), n.session.PrepareOverlaySocket)
	p.xray, err = newAssignmentXray(config, p.dialer)
	if err != nil {
		return nil, err
	}
	if err = p.xray.Start(); err != nil {
		return nil, err
	}
	if n.system != nil {
		dns := make([]netip.Addr, 0, len(c.DNS))
		for _, a := range c.DNS {
			dns = append(dns, netip.MustParseAddr(a))
		}
		if err = n.session.AttachSystem(ctx, "qtc0", dns); err != nil {
			return nil, err
		}
		if err = n.session.InstallSystemRoutes(); err != nil {
			return nil, err
		}
	}
	return p, nil
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
func (p *dataPath) close() error {
	if p == nil {
		return nil
	}
	var result error
	if p.n != nil {
		result = errors.Join(result, p.n.session.DetachPath())
	}
	result = errors.Join(result, p.dialer.close())
	if p.xray != nil {
		result = errors.Join(result, p.xray.Close())
		p.xray = nil
	}
	if p.cmd != nil && p.done != nil {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		ctl := filepath.Join(filepath.Dir(p.cmd.Path), "tinc.exe")
		_, _ = command(ctx, "", ctl, "-c", p.dir, "--pidfile="+filepath.Join(p.dir, "tinc.pid"), "stop")
		cancel()
		select {
		case <-p.done:
		case <-time.After(2 * time.Second):
			if p.job != 0 {
				_ = windows.TerminateJobObject(p.job, 1)
			}
			// Job assignment itself may have failed during startup. The exact
			// child PID is still ours and must be stopped in that case too.
			_ = p.cmd.Process.Kill()
			select {
			case <-p.done:
			case <-time.After(2 * time.Second):
				result = errors.Join(result, errors.New("native tinc did not stop"))
			}
		}
		p.done = nil
	}
	if p.job != 0 {
		windows.CloseHandle(p.job)
		p.job = 0
	}
	return result
}

var errPathCleanup = errors.New("Windows path cleanup incomplete")

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
		inbounds = append(inbounds, map[string]any{"tag": "system-tun", "protocol": "tun", "settings": map[string]any{"name": "qtc0", "mtu": 1280, "gateway": []string{"198.18.94.1/30"}, "dns": c.DNS, "autoSystemRoutingTable": []string{}}})
	}
	// The dedicated strict SystemDialer pins inner VLESS to the owned mesh
	// adapter and propagates binding errors. No permissive socket-option hook
	// is relied upon for isolation.
	document := map[string]any{"log": map[string]any{"loglevel": "none"}, "inbounds": inbounds, "outbounds": []any{
		map[string]any{"tag": "assigned", "protocol": "vless", "mux": map[string]any{"enabled": true, "concurrency": 8, "xudpConcurrency": 0, "xudpProxyUDP443": "allow"}, "settings": map[string]any{"vnext": []any{map[string]any{"address": c.Assignment.Address, "port": c.Assignment.Port, "users": []any{map[string]any{"id": c.Assignment.VLESSUUID.String(), "encryption": "none"}}}}}, "streamSettings": map[string]any{"security": "tls", "tlsSettings": map[string]any{"serverName": c.Assignment.TLSName, "disableSystemRoot": true, "certificates": []any{map[string]any{"usage": "verify", "certificate": strings.Split(strings.TrimSpace(c.Assignment.CertificatePEM), "\n")}}}}},
		map[string]any{"tag": "block", "protocol": "blackhole"}}, "routing": map[string]any{"rules": []any{map[string]any{"type": "field", "ip": []string{"::/0"}, "outboundTag": "block"}, map[string]any{"type": "field", "network": "tcp,udp", "outboundTag": "assigned"}}}}
	b, _ := json.Marshal(document)
	return b
}

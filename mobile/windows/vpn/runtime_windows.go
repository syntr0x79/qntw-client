package vpn

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/syntr0x79/qntw-client/internal/awgprobe"
	"github.com/syntr0x79/qntw-client/internal/mobileprobe/wire"
	"github.com/syntr0x79/qntw-client/internal/xrayfetch"
	"github.com/syntr0x79/qntw-client/mobile/clienttinc"
	"github.com/syntr0x79/qntw-client/mobile/windows/network"
	xnet "github.com/xtls/xray-core/common/net"
	"github.com/xtls/xray-core/core"
	xout "github.com/xtls/xray-core/features/outbound"
	_ "github.com/xtls/xray-core/main/distro/all"
	"github.com/xtls/xray-core/transport/internet"
	"io"
	"net"
	"net/http"
	"net/netip"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
)

type Options struct {
	Job        json.RawMessage
	ControlURL string
	OnStatus   func(clienttinc.Status)
}

var lifecycle struct {
	sync.Mutex
	active bool
}

// Run owns one elevated session. WFP stays active through reverse-order cleanup.
func Run(ctx context.Context, options Options) (result error) {
	job, outbound, err := parseJob(options.Job)
	if err != nil {
		return err
	}
	if err = ValidateControlURL(options.ControlURL); err != nil {
		return err
	}
	if err = ctx.Err(); err != nil {
		return err
	}
	lifecycle.Lock()
	if lifecycle.active {
		lifecycle.Unlock()
		return errors.New("ordinary VPN already active")
	}
	lifecycle.active = true
	lifecycle.Unlock()
	defer func() { lifecycle.Lock(); lifecycle.active = false; lifecycle.Unlock() }()
	n, err := network.Prepare(ctx, network.Options{})
	if err != nil {
		if errors.Is(ctx.Err(), context.Canceled) {
			return nil
		}
		return err
	}
	var instance *core.Instance
	var stack *awgprobe.Stack
	dialer := newPhysicalDialer(ctx, n.Physical())
	defer func() {
		// Explicit Stop/IPC EOF cancels startup too; retain only cleanup errors.
		if errors.Is(ctx.Err(), context.Canceled) {
			result = nil
		}
		result = errors.Join(result, n.DetachPath())
		dialer.close()
		if instance != nil {
			result = errors.Join(result, instance.Close())
		}
		if stack != nil {
			result = errors.Join(result, stack.Close())
		}
		result = errors.Join(result, n.Close())
	}()
	// Dedicated helper: never import mobile/engine or share Tinc's dialer. Keep
	// the closed owner installed on exit so cached XHTTP cannot fall back direct.
	internet.UseAlternativeSystemDialer(dialer)
	dns := jobDNS(job)
	if job.Engine == "amneziawg" {
		stack, err = awgprobe.OpenStack(ctx, job.AWGConf, awgprobe.Options{
			Resolve: func(ctx context.Context, host string) (netip.Addr, error) {
				ip, err := resolve(ctx, n, host)
				if err != nil {
					return netip.Addr{}, err
				}
				return netip.ParseAddr(ip)
			},
			Listen: func(kind string, address *net.UDPAddr) (*net.UDPConn, error) {
				listen := net.ListenConfig{Control: func(_, _ string, raw syscall.RawConn) error { return prepare(raw, n) }}
				connection, err := listen.ListenPacket(ctx, kind, address.String())
				if err != nil {
					return nil, err
				}
				udp, ok := connection.(*net.UDPConn)
				if !ok {
					connection.Close()
					return nil, errors.New("AWG did not open UDP")
				}
				return udp, nil
			},
		})
		if err != nil {
			return errors.New("cannot start AmneziaWG protocol")
		}
	} else {
		message, err := outbound.ProxySettings.GetInstance()
		if err != nil {
			return errProfile
		}
		endpoint, err := xrayfetch.ServerEndpoint(message)
		if err != nil {
			return errProfile
		}
		resolved, err := resolve(ctx, n, endpoint.Address.AsAddress().String())
		if err != nil {
			return err
		}
		address, err := netip.ParseAddr(resolved)
		if err != nil || !address.Is4() {
			return errors.New("physical server must resolve to IPv4")
		}
		outbound, err = xrayfetch.BindOutbound(outbound, "", address)
		if err != nil {
			return errors.New("cannot pin VPN server to physical network")
		}
		outbound.Tag = "vpn"
	}
	config, err := core.LoadConfig("json", strings.NewReader(string(tunnelDocument())))
	if err != nil {
		return errors.New("cannot construct system tunnel")
	}
	if stack == nil {
		config.Outbound[0] = outbound
	}
	instance, err = core.NewWithContext(ctx, config)
	if err != nil {
		return errors.New("cannot create system tunnel")
	}
	if stack != nil {
		manager, ok := instance.GetFeature(xout.ManagerType()).(xout.Manager)
		if !ok {
			return errors.New("outbound manager unavailable")
		}
		if err = manager.RemoveHandler(ctx, "vpn"); err != nil {
			return err
		}
		if err = manager.AddHandler(ctx, &awgOutbound{stack: stack}); err != nil {
			return err
		}
	}
	if err = instance.Start(); err != nil {
		return errors.New("cannot start Wintun tunnel")
	}
	if err = n.AttachSystem(ctx, tunnelName, dns); err != nil {
		return err
	}
	if err = n.InstallSystemRoutes(); err != nil {
		return err
	}
	if err = verifySystem(ctx, options.ControlURL, dns[0], n); err != nil {
		return errors.New("VPN exit health check failed")
	}
	if err = ctx.Err(); err != nil {
		return err
	}
	if options.OnStatus != nil {
		options.OnStatus(clienttinc.Status{State: "connected"})
	}
	original, err := n.Physical().Snapshot()
	if err != nil {
		return err
	}
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
			current, err := n.Physical().Snapshot()
			if err != nil || current != original {
				return errors.New("physical network changed; reconnect required")
			}
		}
	}
}

func prepare(raw syscall.RawConn, n interface{ PrepareSocket(int) error }) error {
	var callErr error
	if err := raw.Control(func(fd uintptr) { callErr = n.PrepareSocket(int(fd)) }); err != nil {
		return err
	}
	return callErr
}
func resolve(ctx context.Context, n interface{ Resolve(string) (string, error) }, host string) (string, error) {
	type answer struct {
		value string
		err   error
	}
	done := make(chan answer, 1)
	go func() { v, e := n.Resolve(host); done <- answer{v, e} }()
	select {
	case <-ctx.Done():
		return "", ctx.Err()
	case value := <-done:
		return value.value, value.err
	}
}
func jobDNS(job wire.Job) []netip.Addr {
	result := []netip.Addr{netip.MustParseAddr("1.1.1.1")}
	if job.Engine != "amneziawg" {
		return result
	}
	section := ""
	for _, line := range strings.Split(job.AWGConf, "\n") {
		line = strings.TrimSpace(strings.SplitN(line, "#", 2)[0])
		if strings.HasPrefix(line, "[") {
			section = strings.ToLower(line)
			continue
		}
		key, value, ok := strings.Cut(line, "=")
		if !ok || section != "[interface]" || !strings.EqualFold(strings.TrimSpace(key), "DNS") {
			continue
		}
		var addresses []netip.Addr
		for _, item := range strings.Split(value, ",") {
			ip, err := netip.ParseAddr(strings.TrimSpace(item))
			if err == nil && ip.Is4() && !ip.IsLoopback() && !ip.IsUnspecified() && !ip.IsMulticast() {
				addresses = append(addresses, ip)
			}
		}
		if len(addresses) > 0 && len(addresses) <= 4 {
			return addresses
		}
	}
	return result
}

// Both HTTP and pure-Go DNS bind to the owned TUN interface and source. The
// interface policy prevents direct success even when Windows WeakHostSend is on.
func verifySystem(ctx context.Context, url string, dns netip.Addr, policy interface{ PrepareSystemSocket(int) error }) error {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	source := net.ParseIP(tunnelAddress)
	resolver := &net.Resolver{PreferGo: true, StrictErrors: true, Dial: func(ctx context.Context, kind, _ string) (net.Conn, error) {
		dial := net.Dialer{Timeout: 4 * time.Second, Control: func(_, _ string, raw syscall.RawConn) error { return prepareSystem(raw, policy) }}
		if strings.HasPrefix(kind, "udp") {
			dial.LocalAddr = &net.UDPAddr{IP: source}
			kind = "udp4"
		} else {
			dial.LocalAddr = &net.TCPAddr{IP: source}
			kind = "tcp4"
		}
		return dial.DialContext(ctx, kind, net.JoinHostPort(dns.String(), "53"))
	}}
	dial := net.Dialer{Timeout: 8 * time.Second, LocalAddr: &net.TCPAddr{IP: source}, Resolver: resolver, Control: func(_, _ string, raw syscall.RawConn) error { return prepareSystem(raw, policy) }}
	transport := &http.Transport{Proxy: nil, DialContext: func(ctx context.Context, _, address string) (net.Conn, error) {
		return dial.DialContext(ctx, "tcp4", address)
	}, TLSHandshakeTimeout: 8 * time.Second, DisableKeepAlives: true}
	defer transport.CloseIdleConnections()
	client := http.Client{Transport: transport, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	response, err := client.Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return errors.New("unhealthy VPN response")
	}
	_, err = io.Copy(io.Discard, io.LimitReader(response.Body, 128<<10))
	return err
}

// Own the lifetime of physical sockets, including detached cached HTTP/3 work.
type physicalDialer struct {
	sync.Mutex
	physical *network.Physical
	ctx      context.Context
	closed   bool
	sockets  map[net.Conn]struct{}
}

func newPhysicalDialer(ctx context.Context, p *network.Physical) *physicalDialer {
	return &physicalDialer{physical: p, ctx: ctx, sockets: map[net.Conn]struct{}{}}
}
func (d *physicalDialer) DestIpAddress() xnet.IP { return nil }
func (d *physicalDialer) add(c net.Conn) error {
	d.Lock()
	if d.closed || d.ctx.Err() != nil {
		d.Unlock()
		c.Close()
		return context.Canceled
	}
	d.sockets[c] = struct{}{}
	d.Unlock()
	return nil
}
func (d *physicalDialer) remove(c net.Conn) { d.Lock(); delete(d.sockets, c); d.Unlock() }
func (d *physicalDialer) close() {
	d.Lock()
	d.closed = true
	sockets := d.sockets
	d.sockets = nil
	d.Unlock()
	for c := range sockets {
		c.Close()
	}
}

type ownedTCP struct {
	*net.TCPConn
	owner *physicalDialer
}

func (c *ownedTCP) Close() error { err := c.TCPConn.Close(); c.owner.remove(c); return err }

type ownedUDP struct {
	*net.UDPConn
	owner *physicalDialer
}

func (c *ownedUDP) Close() error { err := c.UDPConn.Close(); c.owner.remove(c); return err }
func (d *physicalDialer) Dial(ctx context.Context, _ xnet.Address, destination xnet.Destination, _ *internet.SocketConfig) (net.Conn, error) {
	d.Lock()
	closed := d.closed
	d.Unlock()
	if closed || d.ctx.Err() != nil {
		return nil, context.Canceled
	}
	ctx, cancel := context.WithCancel(ctx)
	stop := context.AfterFunc(d.ctx, cancel)
	defer stop()
	defer cancel()
	address, err := resolve(ctx, d.physical, destination.Address.String())
	if err != nil {
		return nil, err
	}
	target := net.JoinHostPort(address, strconv.Itoa(int(destination.Port)))
	if destination.Network == xnet.Network_UDP {
		remote, err := net.ResolveUDPAddr("udp4", target)
		if err != nil {
			return nil, err
		}
		listen := net.ListenConfig{Control: func(_, _ string, raw syscall.RawConn) error { return prepare(raw, d.physical) }}
		packet, err := listen.ListenPacket(ctx, "udp4", "0.0.0.0:0")
		if err != nil {
			return nil, err
		}
		owned := &ownedUDP{UDPConn: packet.(*net.UDPConn), owner: d}
		if err = d.add(owned); err != nil {
			return nil, err
		}
		return &internet.PacketConnWrapper{PacketConn: owned, Dest: remote}, nil
	}
	if destination.Network != xnet.Network_TCP {
		return nil, errors.New("unsupported outer VPN network")
	}
	connection, err := d.physical.DialContext(ctx, "tcp4", target)
	if err != nil {
		return nil, err
	}
	owned := &ownedTCP{TCPConn: connection.(*net.TCPConn), owner: d}
	if err = d.add(owned); err != nil {
		return nil, err
	}
	return owned, nil
}

func prepareSystem(raw syscall.RawConn, policy interface{ PrepareSystemSocket(int) error }) error {
	var callErr error
	if err := raw.Control(func(fd uintptr) { callErr = policy.PrepareSystemSocket(int(fd)) }); err != nil {
		return err
	}
	return callErr
}

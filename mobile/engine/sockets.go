package engine

import (
	"context"
	"errors"
	"net"
	"sync"
	"syscall"
	"time"
	"weak"

	xnet "github.com/xtls/xray-core/common/net"
	"github.com/xtls/xray-core/core"
	"github.com/xtls/xray-core/transport/internet"
)

var knownCores = struct {
	sync.Mutex
	instances map[weak.Pointer[core.Instance]]struct{}
}{instances: make(map[weak.Pointer[core.Instance]]struct{})}

func rememberCore(instance *core.Instance) {
	knownCores.Lock()
	defer knownCores.Unlock()
	for key := range knownCores.instances {
		if key.Value() == nil {
			delete(knownCores.instances, key)
		}
	}
	knownCores.instances[weak.Make(instance)] = struct{}{}
}
func wasOwnedCore(instance *core.Instance) bool {
	knownCores.Lock()
	defer knownCores.Unlock()
	_, found := knownCores.instances[weak.Make(instance)]
	return found
}

type socketOwnerKey struct{}

var probeOwners = struct {
	sync.Mutex
	instances map[*core.Instance]*socketOwner
}{instances: make(map[*core.Instance]*socketOwner)}

// XHTTP keeps HTTP/3 clients outside core.Instance and deliberately strips
// request cancellation. Own the physical sockets instead of trusting that pool
// to close them when a probe or VPN instance ends.
type socketOwner struct {
	sync.Mutex
	network Network
	ctx     context.Context
	closed  bool
	conns   map[net.Conn]struct{}
	failure string
}

func newSocketOwner(ctx context.Context, n Network) *socketOwner {
	return &socketOwner{network: n, ctx: ctx, conns: make(map[net.Conn]struct{})}
}

func (o *socketOwner) recordFailure(err error, fallback string) {
	if o == nil || err == nil {
		return
	}
	reason := probeErrorReason(err)
	if reason == "request_failed" {
		reason = fallback
	}
	if reason == "cancelled" {
		return
	}
	o.Lock()
	if o.failure == "" {
		o.failure = reason
	}
	o.Unlock()
}

func (o *socketOwner) physicalFailure() string {
	if o == nil {
		return ""
	}
	o.Lock()
	defer o.Unlock()
	return o.failure
}
func (o *socketOwner) add(c net.Conn) error {
	o.Lock()
	if o.closed || o.ctx.Err() != nil {
		o.Unlock()
		c.Close()
		return context.Canceled
	}
	o.conns[c] = struct{}{}
	o.Unlock()
	return nil
}
func (o *socketOwner) remove(c net.Conn) { o.Lock(); delete(o.conns, c); o.Unlock() }

type ownedTCP struct {
	*net.TCPConn
	owner *socketOwner
}

func (c *ownedTCP) Close() error { err := c.TCPConn.Close(); c.owner.remove(c); return err }

type ownedUDP struct {
	*net.UDPConn
	owner *socketOwner
}

func (c *ownedUDP) Close() error { err := c.UDPConn.Close(); c.owner.remove(c); return err }
func (o *socketOwner) close() {
	if o == nil {
		return
	}
	o.Lock()
	o.closed = true
	conns := o.conns
	o.conns = nil
	o.Unlock()
	for c := range conns {
		c.Close()
	}
}

type mobileSystemDialer struct{}

func (*mobileSystemDialer) DestIpAddress() xnet.IP { return nil }
func (*mobileSystemDialer) Dial(ctx context.Context, src xnet.Address, dest xnet.Destination, options *internet.SocketConfig) (net.Conn, error) {
	owner, _ := ctx.Value(socketOwnerKey{}).(*socketOwner)
	if owner == nil {
		// TUN detaches context values but retains the owning core instance.
		if instance := core.FromContext(ctx); instance != nil {
			state.Lock()
			if state.tunnelCore == instance {
				owner = state.tunnelOwner
			}
			state.Unlock()
			if owner == nil {
				probeOwners.Lock()
				owner = probeOwners.instances[instance]
				probeOwners.Unlock()
			}
			if owner == nil && wasOwnedCore(instance) {
				return nil, context.Canceled
			}
		}
	}
	var network Network
	if owner != nil {
		owner.Lock()
		closed := owner.closed
		network = owner.network
		owner.Unlock()
		if closed || owner.ctx.Err() != nil {
			return nil, context.Canceled
		}
		var cancel context.CancelFunc
		ctx, cancel = context.WithCancel(ctx)
		stop := context.AfterFunc(owner.ctx, cancel)
		defer stop()
		defer cancel()
	} else {
		state.Lock()
		network = state.tunnelNetwork
		if network == nil {
			network = state.network
		}
		state.Unlock()
	}
	if network == nil {
		return nil, errors.New("mobile: physical network unavailable")
	}
	host := dest.Address.String()
	if dest.Address.Family().IsDomain() {
		ip, e := resolve(ctx, network, host)
		if e != nil {
			owner.recordFailure(e, "dns_failed")
			return nil, e
		}
		host = ip
	}
	address := net.JoinHostPort(host, dest.Port.String())
	control := func(_, _ string, raw syscall.RawConn) error {
		err := prepare(raw, network)
		owner.recordFailure(err, "socket_binding_failed")
		return err
	}
	var conn net.Conn
	if dest.Network == xnet.Network_UDP {
		target, e := net.ResolveUDPAddr("udp", address)
		if e != nil {
			return nil, e
		}
		kind, bind := "udp4", "0.0.0.0:0"
		if target.IP.To4() == nil {
			kind, bind = "udp6", "[::]:0"
		}
		lc := net.ListenConfig{Control: control}
		packet, e := lc.ListenPacket(ctx, kind, bind)
		if e != nil {
			owner.recordFailure(e, "request_failed")
			return nil, e
		}
		if owner != nil {
			udp := &ownedUDP{UDPConn: packet.(*net.UDPConn), owner: owner}
			if e = owner.add(udp); e != nil {
				return nil, e
			}
			packet = udp
		}
		return &internet.PacketConnWrapper{PacketConn: packet, Dest: target}, nil
	}
	keepAlive := net.KeepAliveConfig{Enable: true, Idle: 45 * time.Second, Interval: 45 * time.Second, Count: -1}
	if options != nil {
		if options.TcpKeepAliveIdle < 0 || options.TcpKeepAliveInterval < 0 {
			keepAlive.Enable = false
		}
		if options.TcpKeepAliveIdle > 0 {
			keepAlive.Idle = time.Duration(options.TcpKeepAliveIdle) * time.Second
		}
		if options.TcpKeepAliveInterval > 0 {
			keepAlive.Interval = time.Duration(options.TcpKeepAliveInterval) * time.Second
		}
	}
	dialer := net.Dialer{Timeout: 16 * time.Second, Control: control, KeepAliveConfig: keepAlive}
	if src != nil && src.Family().IsIP() {
		dialer.LocalAddr = &net.TCPAddr{IP: src.IP()}
	}
	var err error
	conn, err = dialer.DialContext(ctx, "tcp", address)
	if err != nil {
		owner.recordFailure(err, "request_failed")
		return nil, err
	}
	if owner != nil {
		tcp := &ownedTCP{TCPConn: conn.(*net.TCPConn), owner: owner}
		if err = owner.add(tcp); err != nil {
			return nil, err
		}
		conn = tcp
	}
	return conn, nil
}

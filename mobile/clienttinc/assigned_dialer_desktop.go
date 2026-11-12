//go:build windows || (darwin && !ios)

package clienttinc

import (
	"context"
	"errors"
	"net"
	"net/netip"
	"sync"
	"syscall"
	"time"

	xnet "github.com/xtls/xray-core/common/net"
	"github.com/xtls/xray-core/core"
	"github.com/xtls/xray-core/transport/internet"
)

// The dedicated Windows tinc supervisor imports no mobile/engine. Keep one
// stable dispatcher for its lifetime: changing Xray's global dialer during
// reconnection would race surviving old-core goroutines.
var assignedDialers = struct {
	sync.RWMutex
	cores  map[*core.Instance]*assignedDialer
	owners map[int32]*assignedDialer
	nextID int64
}{cores: make(map[*core.Instance]*assignedDialer), owners: make(map[int32]*assignedDialer)}

var assignedDispatcherOnce sync.Once

func installAssignedDispatcher() {
	assignedDispatcherOnce.Do(func() { internet.UseAlternativeSystemDialer(windowsAssignedSystemDialer{}) })
}

type windowsAssignedSystemDialer struct{}

func (windowsAssignedSystemDialer) DestIpAddress() xnet.IP { return nil }
func (windowsAssignedSystemDialer) Dial(ctx context.Context, source xnet.Address, destination xnet.Destination, socket *internet.SocketConfig) (net.Conn, error) {
	if socket == nil || socket.Mark <= 0 {
		return nil, errors.New("assigned socket has no lifetime owner")
	}
	instance := core.FromContext(ctx)
	assignedDialers.RLock()
	owner := assignedDialers.owners[socket.Mark]
	if instance != nil && assignedDialers.cores[instance] != owner {
		owner = nil
	}
	assignedDialers.RUnlock()
	if owner == nil {
		return nil, errors.New("no live authorized assignment owns this connection")
	}
	return owner.dial(ctx, source, destination)
}

// Mux creates transport contexts from context.Background, so core.FromContext
// is absent on real TCP and UDP mux dials. The fixed helper configuration uses
// SocketConfig.Mark solely as an opaque, never-reused lifetime ID. This custom
// Windows dialer never forwards it to an OS SO_MARK operation. Mux preserves
// the handler's SocketConfig even when it detaches the context.
func nextAssignedOwnerID() (int32, error) {
	assignedDialers.Lock()
	defer assignedDialers.Unlock()
	if assignedDialers.nextID >= 1<<31-1 {
		return 0, errors.New("assigned socket lifetime IDs exhausted")
	}
	assignedDialers.nextID++
	return int32(assignedDialers.nextID), nil
}

func registerAssignedCore(instance *core.Instance, owner *assignedDialer, id int32) error {
	if owner == nil {
		return errors.New("assigned socket owner is required")
	}
	assignedDialers.Lock()
	defer assignedDialers.Unlock()
	owner.mu.Lock()
	defer owner.mu.Unlock()
	if instance == nil || id <= 0 || owner.closed || owner.id != 0 || assignedDialers.owners[id] != nil || assignedDialers.cores[instance] != nil {
		return errors.New("assigned socket owner is not available")
	}
	owner.id = id
	owner.instance = instance
	assignedDialers.cores[instance] = owner
	assignedDialers.owners[id] = owner
	return nil
}

type assignedDialer struct {
	mu        sync.Mutex
	target    netip.AddrPort
	self      netip.Addr
	prepare   func(int) error
	ctx       context.Context
	cancel    context.CancelFunc
	closed    bool
	conns     map[*assignedConnection]struct{}
	peak      int
	opened    uint64
	id        int32
	instance  *core.Instance
	responses *forwardingObservation
}

func newAssignedDialer(target netip.AddrPort, self netip.Addr, prepare func(int) error) *assignedDialer {
	ctx, cancel := context.WithCancel(context.Background())
	return &assignedDialer{target: target, self: self, prepare: prepare, ctx: ctx, cancel: cancel, conns: make(map[*assignedConnection]struct{}), responses: newForwardingObservation()}
}

func (d *assignedDialer) dial(ctx context.Context, source xnet.Address, dest xnet.Destination) (net.Conn, error) {
	if err := validateAssignedDestination(d.target, d.self, source, dest); err != nil {
		return nil, err
	}
	if d.ctx.Err() != nil || ctx.Err() != nil {
		return nil, context.Canceled
	}
	ctx, cancel := context.WithCancel(ctx)
	stop := context.AfterFunc(d.ctx, cancel)
	defer stop()
	defer cancel()
	dial := net.Dialer{Timeout: 10 * time.Second, LocalAddr: &net.TCPAddr{IP: net.IP(d.self.AsSlice())},
		KeepAliveConfig: net.KeepAliveConfig{Enable: true, Idle: 45 * time.Second, Interval: 45 * time.Second, Count: -1},
		Control: func(_, _ string, raw syscall.RawConn) error {
			var bindErr error
			if err := raw.Control(func(fd uintptr) { bindErr = d.prepare(int(fd)) }); err != nil {
				return err
			}
			return bindErr
		}}
	conn, err := dial.DialContext(ctx, "tcp4", d.target.String())
	if err != nil {
		return nil, err
	}
	tracked := &assignedConnection{Conn: conn, owner: d}
	d.mu.Lock()
	if d.closed || d.ctx.Err() != nil {
		d.mu.Unlock()
		_ = conn.Close()
		return nil, context.Canceled
	}
	d.conns[tracked] = struct{}{}
	d.opened++
	if len(d.conns) > d.peak {
		d.peak = len(d.conns)
	}
	d.mu.Unlock()
	return tracked, nil
}

// AssignedTransportStats contains counts only, never endpoints, UUIDs or keys.
// It makes a real connection-capacity failure distinguishable from a timeout.
type AssignedTransportStats struct {
	PhysicalConnections int                 `json:"physical_connections"`
	PeakConnections     int                 `json:"peak_connections"`
	OpenedConnections   uint64              `json:"opened_connections"`
	LastReplyAt         time.Time           `json:"last_reply_at,omitempty"`
	Sockets             []map[string]uint64 `json:"socket_stats,omitempty"`
}

func CurrentAssignedTransportStats() AssignedTransportStats {
	var result AssignedTransportStats
	assignedDialers.RLock()
	defer assignedDialers.RUnlock()
	for _, owner := range assignedDialers.owners {
		owner.mu.Lock()
		result.PhysicalConnections += len(owner.conns)
		result.PeakConnections += owner.peak
		result.OpenedConnections += owner.opened
		for conn := range owner.conns {
			if stats := assignedSocketStats(conn.Conn); stats != nil {
				result.Sockets = append(result.Sockets, stats)
			}
		}
		owner.mu.Unlock()
		owner.responses.mu.Lock()
		if owner.responses.lastReply.After(result.LastReplyAt) {
			result.LastReplyAt = owner.responses.lastReply
		}
		owner.responses.mu.Unlock()
	}
	return result
}

type assignedConnection struct {
	net.Conn
	owner *assignedDialer
	once  sync.Once
	err   error
}

func (c *assignedConnection) Close() error {
	c.once.Do(func() {
		c.err = c.Conn.Close()
		c.owner.mu.Lock()
		delete(c.owner.conns, c)
		c.owner.mu.Unlock()
	})
	return c.err
}

func (d *assignedDialer) close() error {
	if d == nil {
		return nil
	}
	d.responses.revoke()
	assignedDialers.Lock()
	d.mu.Lock()
	if assignedDialers.owners[d.id] == d {
		delete(assignedDialers.owners, d.id)
	}
	if assignedDialers.cores[d.instance] == d {
		delete(assignedDialers.cores, d.instance)
	}
	d.closed = true
	d.cancel()
	conns := d.conns
	d.conns = make(map[*assignedConnection]struct{})
	d.mu.Unlock()
	assignedDialers.Unlock()
	var result error
	for conn := range conns {
		result = errors.Join(result, conn.Close())
	}
	return result
}

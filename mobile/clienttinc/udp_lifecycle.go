//go:build (linux && !android) || windows || (darwin && !ios)

package clienttinc

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/xtls/xray-core/common/buf"
	xnet "github.com/xtls/xray-core/common/net"
	"github.com/xtls/xray-core/common/session"
	"github.com/xtls/xray-core/core"
	"github.com/xtls/xray-core/features/outbound"
	"github.com/xtls/xray-core/transport"
)

const dnsAssociationIdle = 10 * time.Second
const udpAssociationIdle = 60 * time.Second

// udpLifecycleHandler bounds logical TUN UDP associations before they enter
// Mux. UDP has no FIN, so a closed application's source port otherwise occupies
// a logical Mux slot until the remote timeout, independently of local policy.
type udpLifecycleHandler struct {
	outbound.Handler
	mu      sync.Mutex
	closed  bool
	active  map[*udpActivity]struct{}
	dnsIdle time.Duration
	udpIdle time.Duration
}

func installUDPLifecycle(instance *core.Instance) error {
	manager, ok := instance.GetFeature(outbound.ManagerType()).(outbound.Manager)
	if !ok {
		return errors.New("client outbound manager is unavailable")
	}
	original := manager.GetHandler("assigned")
	if original == nil {
		return errors.New("assigned client outbound is unavailable")
	}
	wrapper := &udpLifecycleHandler{Handler: original, active: make(map[*udpActivity]struct{}), dnsIdle: dnsAssociationIdle, udpIdle: udpAssociationIdle}
	// The pinned Manager removes the registration without closing its handler.
	// Install before Start, preserving its tag, settings and default routing.
	if err := manager.RemoveHandler(context.Background(), original.Tag()); err != nil {
		return errors.New("cannot prepare client UDP lifecycle")
	}
	if err := manager.AddHandler(context.Background(), wrapper); err != nil {
		if manager.AddHandler(context.Background(), original) != nil {
			_ = original.Close()
		}
		return errors.New("cannot install client UDP lifecycle")
	}
	return nil
}

func (h *udpLifecycleHandler) Dispatch(ctx context.Context, link *transport.Link) {
	inbound := session.InboundFromContext(ctx)
	outs := session.OutboundsFromContext(ctx)
	if inbound == nil || inbound.Name != "tun" || len(outs) == 0 || outs[len(outs)-1].Target.Network != xnet.Network_UDP {
		h.Handler.Dispatch(ctx, link)
		return
	}
	idle := h.udpIdle
	if outs[len(outs)-1].Target.Port == 53 {
		idle = h.dnsIdle
	}
	ctx, cancel := context.WithCancel(ctx)
	activity := &udpActivity{cancel: cancel, idle: idle, generalIdle: h.udpIdle, until: time.Now().Add(idle)}
	h.mu.Lock()
	if h.closed {
		h.mu.Unlock()
		cancel()
		return
	}
	h.active[activity] = struct{}{}
	activity.mu.Lock()
	activity.timer = time.AfterFunc(idle, activity.expire)
	activity.mu.Unlock()
	h.mu.Unlock()
	defer func() {
		activity.stop()
		h.mu.Lock()
		delete(h.active, activity)
		h.mu.Unlock()
	}()
	wrapped := &transport.Link{
		Reader: &udpActivityReader{Reader: link.Reader, activity: activity},
		Writer: &udpActivityWriter{Writer: link.Writer, activity: activity},
	}
	// The actual TUN reader isn't a pipe.Reader, so stock Mux.Dispatch waits
	// for this context or remote End. Cancellation returns to TUN's single
	// deferred conn.Close, removing its source mapping and unblocking fetchInput.
	h.Handler.Dispatch(ctx, wrapped)
}

func (h *udpLifecycleHandler) Close() error {
	h.mu.Lock()
	h.closed = true
	active := make([]*udpActivity, 0, len(h.active))
	for activity := range h.active {
		active = append(active, activity)
	}
	h.mu.Unlock()
	for _, activity := range active {
		activity.stop()
	}
	return h.Handler.Close()
}

type udpActivity struct {
	mu          sync.Mutex
	cancel      context.CancelFunc
	timer       *time.Timer
	idle        time.Duration
	generalIdle time.Duration
	until       time.Time
	done        bool
}

func (a *udpActivity) touch(mb buf.MultiBuffer) {
	promote := false
	for _, b := range mb {
		if b != nil && b.UDP != nil && b.UDP.Network == xnet.Network_UDP && b.UDP.Port != 53 {
			promote = true
		}
	}
	a.mu.Lock()
	if !a.done {
		if promote && a.idle < a.generalIdle {
			a.idle = a.generalIdle
		}
		a.until = time.Now().Add(a.idle)
		a.timer.Reset(a.idle)
	}
	a.mu.Unlock()
}

func (a *udpActivity) expire() {
	a.mu.Lock()
	if a.done {
		a.mu.Unlock()
		return
	}
	if remaining := time.Until(a.until); remaining > 0 {
		a.timer.Reset(remaining)
		a.mu.Unlock()
		return
	}
	a.done = true
	a.mu.Unlock()
	a.cancel()
}

func (a *udpActivity) stop() {
	a.mu.Lock()
	if !a.done {
		a.done = true
		a.timer.Stop()
	}
	a.mu.Unlock()
	a.cancel()
}

type udpActivityReader struct {
	buf.Reader
	activity *udpActivity
}

func (r *udpActivityReader) observe(mb buf.MultiBuffer, err error) (buf.MultiBuffer, error) {
	if len(mb) > 0 {
		r.activity.touch(mb)
	}
	if err != nil {
		r.activity.stop()
	}
	return mb, err
}
func (r *udpActivityReader) ReadMultiBuffer() (buf.MultiBuffer, error) {
	return r.observe(r.Reader.ReadMultiBuffer())
}
func (r *udpActivityReader) ReadMultiBufferTimeout(timeout time.Duration) (buf.MultiBuffer, error) {
	if reader, ok := r.Reader.(buf.TimeoutReader); ok {
		mb, err := reader.ReadMultiBufferTimeout(timeout)
		if err == buf.ErrReadTimeout || err == buf.ErrNotTimeoutReader {
			if len(mb) > 0 {
				r.activity.touch(mb)
			}
			return mb, err
		}
		return r.observe(mb, err)
	}
	return nil, buf.ErrNotTimeoutReader
}
func (r *udpActivityReader) Interrupt()   { r.activity.stop() }
func (r *udpActivityReader) Close() error { r.activity.stop(); return nil }

type udpActivityWriter struct {
	buf.Writer
	activity *udpActivity
}

func (w *udpActivityWriter) WriteMultiBuffer(mb buf.MultiBuffer) error {
	if len(mb) > 0 {
		w.activity.touch(mb) // Writer consumes/releases the buffers.
	}
	err := w.Writer.WriteMultiBuffer(mb)
	if err != nil {
		w.activity.stop()
	}
	return err
}
func (w *udpActivityWriter) Interrupt()   { w.activity.stop() }
func (w *udpActivityWriter) Close() error { w.activity.stop(); return nil }

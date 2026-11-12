//go:build (linux && !android) || windows || (darwin && !ios)

package clienttinc

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/xtls/xray-core/common"
	"github.com/xtls/xray-core/common/buf"
	"github.com/xtls/xray-core/core"
	"github.com/xtls/xray-core/features/outbound"
	"github.com/xtls/xray-core/transport"
)

// Each observation belongs to one authorized core. Only a reply delivered by
// its assigned outbound confirms forwarding; local TUN counters/SYN-ACKs do not.
type forwardingObservation struct {
	mu        sync.Mutex
	confirmed bool
	revoked   bool
	lastReply time.Time
	first     chan struct{}
	closed    chan struct{}
}

func newForwardingObservation() *forwardingObservation {
	return &forwardingObservation{first: make(chan struct{}), closed: make(chan struct{})}
}

func (o *forwardingObservation) reply() {
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.revoked {
		return
	}
	o.lastReply = time.Now()
	if !o.confirmed {
		o.confirmed = true
		close(o.first)
	}
}

func (o *forwardingObservation) confirmedNow() bool {
	if o == nil {
		return false
	}
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.confirmed && !o.revoked
}

func (o *forwardingObservation) recent(now time.Time, grace time.Duration) bool {
	if o == nil {
		return false
	}
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.confirmed && !o.revoked && !now.Before(o.lastReply) && now.Sub(o.lastReply) <= grace
}

func (o *forwardingObservation) revoke() {
	if o == nil {
		return
	}
	o.mu.Lock()
	defer o.mu.Unlock()
	if !o.revoked {
		o.revoked = true
		close(o.closed)
	}
}

// A slow third-party HTTPS check must not hide a real reply from this core.
// Stop/expiry and revocation still win; the pending check is always cancelled.
func checkObservedForwarding(ctx context.Context, o *forwardingObservation, probe func(context.Context) error) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if o == nil {
		return probe(ctx)
	}
	select {
	case <-o.closed:
		return errors.New("assigned forwarding owner was revoked")
	default:
	}
	if o.confirmedNow() {
		return ctx.Err()
	}
	check, cancel := context.WithCancel(ctx)
	defer cancel()
	result := make(chan error, 1)
	go func() { result <- probe(check) }()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-o.closed:
		return errors.New("assigned forwarding owner was revoked")
	case <-o.first:
		if err := ctx.Err(); err != nil {
			return err
		}
		if !o.confirmedNow() {
			return errors.New("assigned forwarding owner was revoked")
		}
		return nil
	case err := <-result:
		if cancelled := ctx.Err(); cancelled != nil {
			return cancelled
		}
		select {
		case <-o.closed:
			return errors.New("assigned forwarding owner was revoked")
		default:
		}
		if o.confirmedNow() {
			return nil
		}
		return err
	}
}

type responseObservedHandler struct {
	outbound.Handler
	observation *forwardingObservation
}

func (h *responseObservedHandler) Dispatch(ctx context.Context, link *transport.Link) {
	h.Handler.Dispatch(ctx, &transport.Link{Reader: link.Reader, Writer: &responseObservedWriter{Writer: link.Writer, observation: h.observation}})
}

func (h *responseObservedHandler) Close() error {
	h.observation.revoke()
	return h.Handler.Close()
}

type responseObservedWriter struct {
	buf.Writer
	observation *forwardingObservation
}

func (w *responseObservedWriter) WriteMultiBuffer(mb buf.MultiBuffer) error {
	payload := mb.Len() > 0 // The underlying writer owns/releases the buffers.
	err := w.Writer.WriteMultiBuffer(mb)
	if err == nil && payload {
		w.observation.reply()
	}
	return err
}

func (w *responseObservedWriter) Close() error { return common.Close(w.Writer) }
func (w *responseObservedWriter) Interrupt()   { _ = common.Interrupt(w.Writer) }

func installForwardingObservation(instance *core.Instance, observation *forwardingObservation) error {
	manager, ok := instance.GetFeature(outbound.ManagerType()).(outbound.Manager)
	if !ok || observation == nil {
		return errors.New("assigned forwarding observer unavailable")
	}
	original := manager.GetHandler("assigned")
	if original == nil {
		return errors.New("assigned client outbound unavailable")
	}
	if err := manager.RemoveHandler(context.Background(), original.Tag()); err != nil {
		return err
	}
	wrapper := &responseObservedHandler{Handler: original, observation: observation}
	if err := manager.AddHandler(context.Background(), wrapper); err != nil {
		if manager.AddHandler(context.Background(), original) != nil {
			_ = original.Close()
		}
		return err
	}
	return nil
}

//go:build (linux && !android) || windows || (darwin && !ios)

package clienttinc

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/xtls/xray-core/common/buf"
	"github.com/xtls/xray-core/features/outbound"
	"github.com/xtls/xray-core/transport"
)

type responseTestWriter struct {
	err       error
	closed    int
	interrupt int
}

func (w *responseTestWriter) WriteMultiBuffer(mb buf.MultiBuffer) error {
	buf.ReleaseMulti(mb)
	return w.err
}
func (w *responseTestWriter) Close() error { w.closed++; return nil }
func (w *responseTestWriter) Interrupt()   { w.interrupt++ }

type responseTestHandler struct {
	outbound.Handler
	dispatch func(*transport.Link)
	closed   int
}

func (h *responseTestHandler) Dispatch(_ context.Context, link *transport.Link) { h.dispatch(link) }
func (h *responseTestHandler) Close() error                                     { h.closed++; return nil }

func responsePayload() buf.MultiBuffer {
	b := buf.New()
	_, _ = b.WriteString("reply received through assigned VLESS")
	return buf.MultiBuffer{b}
}

func TestForwardingObservationRequiresDeliveredResponse(t *testing.T) {
	for _, tc := range []struct {
		name    string
		payload bool
		err     error
		want    bool
	}{
		{"empty", false, nil, false},
		{"failed delivery", true, errors.New("closed consumer"), false},
		{"returned data", true, nil, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			o := newForwardingObservation()
			underlying := &responseTestWriter{err: tc.err}
			stub := &responseTestHandler{dispatch: func(link *transport.Link) {
				var payload buf.MultiBuffer
				if tc.payload {
					payload = responsePayload()
				}
				if err := link.Writer.WriteMultiBuffer(payload); !errors.Is(err, tc.err) {
					t.Fatal("writer error was hidden")
				}
				_ = link.Writer.(interface{ Close() error }).Close()
				link.Writer.(interface{ Interrupt() }).Interrupt()
			}}
			h := &responseObservedHandler{Handler: stub, observation: o}
			h.Dispatch(context.Background(), &transport.Link{Writer: underlying})
			if o.confirmedNow() != tc.want || underlying.closed != 1 || underlying.interrupt != 1 {
				t.Fatalf("confirmation=%v, close=%d interrupt=%d", o.confirmedNow(), underlying.closed, underlying.interrupt)
			}
			_ = h.Close()
			if o.confirmedNow() || stub.closed != 1 {
				t.Fatal("core shutdown retained confirmation or lost close")
			}
		})
	}
}

func TestForwardingResponseCancelsBlockedPublicHealthProbe(t *testing.T) {
	o := newForwardingObservation()
	started, cancelled := make(chan struct{}), make(chan struct{})
	result := make(chan error, 1)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	go func() {
		result <- checkObservedForwarding(ctx, o, func(probe context.Context) error {
			close(started)
			<-probe.Done()
			close(cancelled)
			return probe.Err()
		})
	}()
	<-started
	o.reply()
	select {
	case err := <-result:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("real data still waited for the external HTTPS timeout")
	}
	select {
	case <-cancelled:
	case <-time.After(time.Second):
		t.Fatal("superseded health request was left running")
	}
}

func TestForwardingConfirmedPathSurvivesPublicProbeFlaps(t *testing.T) {
	o := newForwardingObservation()
	o.reply()
	var calls atomic.Int32
	for i := 0; i < 5; i++ {
		if err := checkObservedForwarding(context.Background(), o, func(context.Context) error {
			calls.Add(1)
			return errors.New("public website unavailable")
		}); err != nil {
			t.Fatal("confirmed assigned path was relabelled by a public website", err)
		}
	}
	if calls.Load() != 0 {
		t.Fatal("public check delayed an already confirmed path")
	}
}

func TestForwardingRevocationAndExpiryOverrideConfirmation(t *testing.T) {
	old, next := newForwardingObservation(), newForwardingObservation()
	old.reply()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := checkObservedForwarding(ctx, old, func(context.Context) error { return nil }); !errors.Is(err, context.Canceled) {
		t.Fatal("cancelled/expired authorization used prior traffic", err)
	}
	old.revoke()
	old.reply() // A late old-core writer must never revive its owner.
	if old.confirmedNow() || next.confirmedNow() {
		t.Fatal("revocation or core lifetime isolation failed")
	}
	if err := checkObservedForwarding(context.Background(), old, func(context.Context) error { return nil }); err == nil {
		t.Fatal("revoked owner accepted a successful late check")
	}
	failure := errors.New("new exit has not forwarded data")
	if err := checkObservedForwarding(context.Background(), next, func(context.Context) error { return failure }); !errors.Is(err, failure) {
		t.Fatal("new path inherited old confirmation", err)
	}
}

func TestForwardingControlSnapshotGraceExpires(t *testing.T) {
	o := newForwardingObservation()
	o.reply()
	o.mu.Lock()
	last := o.lastReply
	o.mu.Unlock()
	if !o.recent(last.Add(time.Second), 15*time.Second) || o.recent(last.Add(16*time.Second), 15*time.Second) {
		t.Fatal("control snapshot fallback was not bounded to recent data")
	}
	o.revoke()
	if o.recent(last.Add(time.Second), 15*time.Second) {
		t.Fatal("revoked traffic bypassed the control snapshot gate")
	}
}

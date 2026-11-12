//go:build (linux && !android) || windows || (darwin && !ios)

package clienttinc

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/xtls/xray-core/common/buf"
	xnet "github.com/xtls/xray-core/common/net"
	"github.com/xtls/xray-core/common/session"
	"github.com/xtls/xray-core/features/outbound"
	"github.com/xtls/xray-core/transport"
)

type udpLifecycleStub struct {
	outbound.Handler
	dispatch func(context.Context, *transport.Link)
	closed   atomic.Int32
}

func (s *udpLifecycleStub) Dispatch(ctx context.Context, link *transport.Link) { s.dispatch(ctx, link) }
func (s *udpLifecycleStub) Close() error                                       { s.closed.Add(1); return nil }

type udpTestReader struct{ closed atomic.Int32 }

func (*udpTestReader) ReadMultiBuffer() (buf.MultiBuffer, error) {
	b := buf.New()
	b.WriteString("request")
	return buf.MultiBuffer{b}, nil
}
func (*udpTestReader) ReadMultiBufferTimeout(time.Duration) (buf.MultiBuffer, error) { return nil, nil }
func (r *udpTestReader) Close() error                                                { r.closed.Add(1); return nil }
func (r *udpTestReader) Interrupt()                                                  { r.closed.Add(1) }

type udpTestWriter struct{ closed atomic.Int32 }

func (*udpTestWriter) WriteMultiBuffer(mb buf.MultiBuffer) error { buf.ReleaseMulti(mb); return nil }
func (w *udpTestWriter) Close() error                            { w.closed.Add(1); return nil }
func (w *udpTestWriter) Interrupt()                              { w.closed.Add(1) }

func udpTestContext(name string, network xnet.Network, port xnet.Port) context.Context {
	ctx := session.ContextWithInbound(context.Background(), &session.Inbound{Name: name})
	return session.ContextWithOutbounds(ctx, []*session.Outbound{{Target: xnet.Destination{Network: network, Address: xnet.ParseAddress("93.184.215.100"), Port: port}}})
}

func TestUDPLifecycleDNSExactIdleResetsOnBothPayloadDirections(t *testing.T) {
	entered := make(chan *transport.Link, 1)
	stub := &udpLifecycleStub{dispatch: func(ctx context.Context, link *transport.Link) { entered <- link; <-ctx.Done() }}
	h := &udpLifecycleHandler{Handler: stub, active: map[*udpActivity]struct{}{}, dnsIdle: 180 * time.Millisecond, udpIdle: time.Second}
	r, w := &udpTestReader{}, &udpTestWriter{}
	finished := make(chan struct{})
	go func() {
		h.Dispatch(udpTestContext("tun", xnet.Network_UDP, 53), &transport.Link{Reader: r, Writer: w})
		close(finished)
	}()
	link := <-entered
	time.Sleep(100 * time.Millisecond)
	mb, err := link.Reader.ReadMultiBuffer()
	if err != nil {
		t.Fatal(err)
	}
	buf.ReleaseMulti(mb)
	time.Sleep(110 * time.Millisecond)
	select {
	case <-finished:
		t.Fatal("uplink activity did not reset DNS lifetime")
	default:
	}
	b := buf.New()
	b.WriteString("response")
	if err := link.Writer.WriteMultiBuffer(buf.MultiBuffer{b}); err != nil {
		t.Fatal(err)
	}
	time.Sleep(110 * time.Millisecond)
	select {
	case <-finished:
		t.Fatal("downlink activity did not reset DNS lifetime")
	default:
	}
	// A timed probe with no payload must not count as network activity.
	if mb, err := link.Reader.(buf.TimeoutReader).ReadMultiBufferTimeout(time.Millisecond); mb != nil || err != nil {
		t.Fatal("timeout-reader contract changed")
	}
	select {
	case <-finished:
	case <-time.After(180 * time.Millisecond):
		t.Fatal("DNS association retained an inactive source")
	}
	if r.closed.Load() != 0 || w.closed.Load() != 0 {
		t.Fatal("wrapper double-closed the underlying TUN source")
	}
	if len(h.active) != 0 {
		t.Fatal("expired UDP dispatch retained its lifecycle entry")
	}
}

func TestUDPLifecycleRemoteEndCloseAndPerDispatchIsolation(t *testing.T) {
	type call struct {
		ctx  context.Context
		link *transport.Link
	}
	entered := make(chan call, 2)
	stub := &udpLifecycleStub{dispatch: func(ctx context.Context, link *transport.Link) { entered <- call{ctx, link}; <-ctx.Done() }}
	h := &udpLifecycleHandler{Handler: stub, active: map[*udpActivity]struct{}{}, dnsIdle: 50 * time.Millisecond, udpIdle: time.Second}
	r, w := &udpTestReader{}, &udpTestWriter{}
	finished1, finished2 := make(chan struct{}), make(chan struct{})
	go func() {
		h.Dispatch(udpTestContext("tun", xnet.Network_UDP, 443), &transport.Link{Reader: r, Writer: w})
		close(finished1)
	}()
	first := <-entered
	time.Sleep(100 * time.Millisecond)
	select {
	case <-finished1:
		t.Fatal("general UDP incorrectly used the short DNS lifetime")
	default:
	}
	go func() {
		h.Dispatch(udpTestContext("tun", xnet.Network_UDP, 443), &transport.Link{Reader: r, Writer: w})
		close(finished2)
	}()
	second := <-entered
	if err := first.link.Writer.(interface{ Close() error }).Close(); err != nil {
		t.Fatal(err)
	}
	select {
	case <-finished1:
	case <-time.After(time.Second):
		t.Fatal("remote End did not cancel its logical UDP source")
	}
	select {
	case <-second.ctx.Done():
		t.Fatal("late old completion canceled a newer dispatch")
	default:
	}
	if err := h.Close(); err != nil {
		t.Fatal(err)
	}
	select {
	case <-finished2:
	case <-time.After(time.Second):
		t.Fatal("core Close retained a logical UDP association")
	}
	if r.closed.Load() != 0 || w.closed.Load() != 0 || stub.closed.Load() != 1 {
		t.Fatal("underlying TUN source was closed twice or delegated handler was not closed")
	}
}

func TestUDPLifecycleLeavesTCPAndOtherInboundsUnchanged(t *testing.T) {
	for _, item := range []struct {
		name    string
		network xnet.Network
	}{{"tun", xnet.Network_TCP}, {"socks", xnet.Network_UDP}} {
		link := &transport.Link{Reader: &udpTestReader{}, Writer: &udpTestWriter{}}
		ctx := udpTestContext(item.name, item.network, 53)
		stub := &udpLifecycleStub{dispatch: func(got context.Context, actual *transport.Link) {
			if got != ctx || actual != link {
				t.Fatal("non-TUN-UDP flow was wrapped")
			}
		}}
		h := &udpLifecycleHandler{Handler: stub, active: map[*udpActivity]struct{}{}, dnsIdle: time.Millisecond, udpIdle: time.Millisecond}
		h.Dispatch(ctx, link)
		if len(h.active) != 0 {
			t.Fatal("non-TUN-UDP flow acquired an idle timer")
		}
	}
}

func TestUDPLifecyclePromotesDNSAssociationForAnotherDestination(t *testing.T) {
	entered := make(chan *transport.Link, 1)
	stub := &udpLifecycleStub{dispatch: func(ctx context.Context, link *transport.Link) { entered <- link; <-ctx.Done() }}
	h := &udpLifecycleHandler{Handler: stub, active: map[*udpActivity]struct{}{}, dnsIdle: 80 * time.Millisecond, udpIdle: time.Second}
	finished := make(chan struct{})
	go func() {
		h.Dispatch(udpTestContext("tun", xnet.Network_UDP, 53), &transport.Link{Reader: &udpTestReader{}, Writer: &udpTestWriter{}})
		close(finished)
	}()
	link := <-entered
	b := buf.New()
	b.WriteString("same-source-QUIC-reply")
	destination := xnet.UDPDestination(xnet.ParseAddress("93.184.215.100"), 443)
	b.UDP = &destination
	if err := link.Writer.WriteMultiBuffer(buf.MultiBuffer{b}); err != nil {
		t.Fatal(err)
	}
	time.Sleep(180 * time.Millisecond)
	select {
	case <-finished:
		t.Fatal("DNS-to-general association expired at the DNS deadline")
	default:
	}
	h.Close()
	select {
	case <-finished:
	case <-time.After(time.Second):
		t.Fatal("promoted association ignored core Close")
	}
}

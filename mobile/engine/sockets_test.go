package engine

import (
	"context"
	"errors"
	xnet "github.com/xtls/xray-core/common/net"
	"github.com/xtls/xray-core/core"
	"net"
	"testing"
	"time"
)

func TestMobileDialerRejectsBindingFailure(t *testing.T) {
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	sentinel := errors.New("network binding failed")
	n := &testNetwork{err: sentinel}
	owner := newSocketOwner(context.Background(), n)
	defer owner.close()
	ctx := context.WithValue(context.Background(), socketOwnerKey{}, owner)
	target := xnet.TCPDestination(xnet.LocalHostIP, xnet.Port(listener.Addr().(*net.TCPAddr).Port))
	conn, err := (&mobileSystemDialer{}).Dial(ctx, nil, target, nil)
	if conn != nil {
		conn.Close()
		t.Fatal("unprotected connection escaped")
	}
	if !errors.Is(err, sentinel) {
		t.Fatalf("binding error lost: %v", err)
	}
	if n.calls.Load() != 1 {
		t.Fatal("physical binding not attempted")
	}
}
func TestProbeOwnsUDPSocketAndRejectsLateHTTP3Dial(t *testing.T) {
	parent, cancel := context.WithCancel(context.Background())
	defer cancel()
	n := &testNetwork{}
	owner := newSocketOwner(parent, n)
	ctx := context.WithValue(parent, socketOwnerKey{}, owner)
	target := xnet.UDPDestination(xnet.LocalHostIP, 443)
	conn, err := (&mobileSystemDialer{}).Dial(ctx, nil, target, nil)
	if err != nil {
		t.Fatal(err)
	}
	owner.close()
	conn.SetDeadline(time.Now().Add(time.Second))
	if _, err = conn.Write([]byte{1}); !errors.Is(err, net.ErrClosed) {
		t.Fatalf("probe UDP socket survived completion: %v", err)
	}
	// XHTTP removes cancellation but preserves context values. A cached transport
	// must not open a new socket after its probe owner has ended.
	conn, err = (&mobileSystemDialer{}).Dial(context.WithoutCancel(ctx), nil, target, nil)
	if conn != nil || !errors.Is(err, context.Canceled) {
		t.Fatalf("late HTTP3 dial accepted: %v", err)
	}
	if n.calls.Load() != 1 {
		t.Fatal("late dial reached Android network")
	}
}

func TestNormallyClosedSocketIsRemovedFromOwner(t *testing.T) {
	n := &testNetwork{}
	owner := newSocketOwner(context.Background(), n)
	defer owner.close()
	ctx := context.WithValue(context.Background(), socketOwnerKey{}, owner)
	target := xnet.UDPDestination(xnet.LocalHostIP, 443)
	for i := 0; i < 100; i++ {
		conn, err := (&mobileSystemDialer{}).Dial(ctx, nil, target, nil)
		if err != nil {
			t.Fatal(err)
		}
		if err = conn.Close(); err != nil {
			t.Fatal(err)
		}
	}
	owner.Lock()
	remaining := len(owner.conns)
	owner.Unlock()
	if remaining != 0 {
		t.Fatalf("closed sockets retained: %d", remaining)
	}
}

func TestTunDetachedContextUsesVPNOwner(t *testing.T) {
	n := &testNetwork{}
	owner := newSocketOwner(context.Background(), n)
	instance := &core.Instance{}
	state.Lock()
	state.tunnelCore, state.tunnelOwner = instance, owner
	state.Unlock()
	defer func() { owner.close(); state.Lock(); state.tunnelCore, state.tunnelOwner = nil, nil; state.Unlock() }()
	ctx := context.WithValue(context.Background(), core.XrayKey(1), instance)
	target := xnet.UDPDestination(xnet.LocalHostIP, 443)
	conn, err := (&mobileSystemDialer{}).Dial(ctx, nil, target, nil)
	if err != nil {
		t.Fatal(err)
	}
	owner.close()
	if _, err = conn.Write([]byte{1}); !errors.Is(err, net.ErrClosed) {
		t.Fatalf("VPN socket survived stop: %v", err)
	}
	if conn, err = (&mobileSystemDialer{}).Dial(ctx, nil, target, nil); conn != nil || !errors.Is(err, context.Canceled) {
		t.Fatalf("late VPN dial accepted: %v", err)
	}
}

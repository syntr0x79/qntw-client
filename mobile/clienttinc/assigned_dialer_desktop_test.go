//go:build windows || (darwin && !ios)

package clienttinc

import (
	"context"
	"errors"
	"net/netip"
	"sync/atomic"
	"testing"
	"time"

	xnet "github.com/xtls/xray-core/common/net"
	"github.com/xtls/xray-core/core"
	"github.com/xtls/xray-core/transport/internet"
)

func TestWindowsAssignedDialerPropagatesBindingFailureWithoutDialing(t *testing.T) {
	bindFailure := errors.New("owned overlay is gone")
	var prepared atomic.Int32
	dialer := newAssignedDialer(netip.MustParseAddrPort("10.76.0.10:18443"), netip.MustParseAddr("10.77.4.1"), func(int) error { prepared.Add(1); return bindFailure })
	defer dialer.close()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	conn, err := dialer.dial(ctx, nil, xnet.TCPDestination(xnet.ParseAddress("10.76.0.10"), 18443))
	if conn != nil || !errors.Is(err, bindFailure) || prepared.Load() != 1 {
		t.Fatalf("bind failure was hidden: conn=%v err=%v calls=%d", conn, err, prepared.Load())
	}
}

func TestWindowsAssignedDispatcherCannotAdoptAnOldCore(t *testing.T) {
	instance, other := &core.Instance{}, &core.Instance{}
	var prepared atomic.Int32
	marker := errors.New("new owner binding")
	owner := newAssignedDialer(netip.MustParseAddrPort("10.76.0.10:18443"), netip.MustParseAddr("10.77.4.1"), func(int) error { prepared.Add(1); return marker })
	defer owner.close()
	id, err := nextAssignedOwnerID()
	if err != nil {
		t.Fatal(err)
	}
	if err = registerAssignedCore(instance, owner, id); err != nil {
		t.Fatal(err)
	}
	options := &internet.SocketConfig{Mark: id}
	dest := xnet.TCPDestination(xnet.ParseAddress("10.76.0.10"), 18443)
	dispatch := windowsAssignedSystemDialer{}
	for _, opt := range []*internet.SocketConfig{nil, {}, {Mark: -1}, {Mark: id + 1}} {
		if conn, err := dispatch.Dial(context.Background(), nil, dest, opt); conn != nil || err == nil {
			t.Fatalf("unknown core authorized: %v/%v", conn, err)
		}
	}
	foreign := context.WithValue(context.Background(), core.XrayKey(1), other)
	if conn, err := dispatch.Dial(foreign, nil, dest, options); conn != nil || err == nil {
		t.Fatal("foreign core adopted another owner's token")
	}
	if prepared.Load() != 0 {
		t.Fatal("unknown core reached socket creation")
	}
	ctx := context.WithValue(context.Background(), core.XrayKey(1), instance)
	if _, err := dispatch.Dial(ctx, nil, dest, options); !errors.Is(err, marker) {
		t.Fatalf("registered core not routed to strict binder: %v", err)
	}
	if _, err := dispatch.Dial(context.Background(), nil, dest, options); !errors.Is(err, marker) {
		t.Fatalf("live detached mux owner did not reach strict binder: %v", err)
	}
	owner.close()
	if conn, err := dispatch.Dial(ctx, nil, dest, options); conn != nil || err == nil {
		t.Fatal("revoked old core was adopted")
	}
	if prepared.Load() != 2 {
		t.Fatal("revoked core opened another socket")
	}
	newID, err := nextAssignedOwnerID()
	if err != nil || newID <= id {
		t.Fatalf("assignment lifetime ID reused: old=%d new=%d err=%v", id, newID, err)
	}
	newOwner := newAssignedDialer(owner.target, owner.self, func(int) error { t.Fatal("revoked old mux adopted a new assignment"); return nil })
	defer newOwner.close()
	if err = registerAssignedCore(&core.Instance{}, newOwner, newID); err != nil {
		t.Fatal(err)
	}
	if conn, err := dispatch.Dial(context.Background(), nil, dest, options); conn != nil || err == nil {
		t.Fatal("detached old mux was adopted after reconnection")
	}
}

//go:build linux && !android

package main

import (
	"context"
	"net"
	"testing"
	"time"

	xnet "github.com/xtls/xray-core/common/net"
	"github.com/xtls/xray-core/transport/internet"
)

// The ordinary engine replaces Xray's global dialer in init. A seemingly
// harmless shared-helper import must never make the independent tinc runtime
// require ordinary-engine socket ownership or bypass its overlay routing.
func TestDedicatedHelperRetainsIndependentXraySystemDialer(t *testing.T) {
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	accepted := make(chan struct{})
	go func() {
		conn, err := listener.Accept()
		if err == nil {
			conn.Close()
		}
		close(accepted)
	}()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	port := xnet.Port(listener.Addr().(*net.TCPAddr).Port)
	conn, err := internet.DialSystem(ctx, xnet.TCPDestination(xnet.LocalHostIP, port), nil)
	if err != nil {
		t.Fatalf("dedicated tinc helper inherited an incompatible global Xray dialer: %v", err)
	}
	conn.Close()
	select {
	case <-accepted:
	case <-ctx.Done():
		t.Fatal("system dialer did not reach the independent listener")
	}
}

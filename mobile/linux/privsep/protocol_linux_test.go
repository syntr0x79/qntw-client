//go:build linux

package privsep

import (
	"context"
	"github.com/vishvananda/netlink"
	"net"
	"net/netip"
	"os"
	"os/exec"
	"strconv"
	"testing"
	"time"
)

type fakeDNS struct{ applied, reverted bool }

func (d *fakeDNS) Apply(_ context.Context, _ string, _ netip.Addr) error {
	d.applied = true
	return nil
}
func (d *fakeDNS) Revert(_ context.Context, _ string) error { d.reverted = true; return nil }
func testPair(t *testing.T) (*net.UnixConn, *net.UnixConn) {
	t.Helper()
	parent, file, err := NewPair()
	if err != nil {
		t.Fatal(err)
	}
	childConn, err := net.FileConn(file)
	file.Close()
	if err != nil {
		parent.Close()
		t.Fatal(err)
	}
	child, ok := childConn.(*net.UnixConn)
	if !ok {
		t.Fatal("not a Unix socket")
	}
	return parent, child
}
func TestHelperRejectsDifferentPeer(t *testing.T) {
	parent, child := testPair(t)
	defer parent.Close()
	if err := Serve(child, uint32(os.Getuid()+1), &fakeDNS{}); err == nil {
		t.Fatal("accepted wrong uid")
	}
}
func TestHelperCleansRoutesAfterPeerDisconnect(t *testing.T) {
	if os.Getenv("QUANTWAY_TUN_TEST") != "1" {
		t.Skip("requires isolated network namespace and CAP_NET_ADMIN")
	}
	before, err := netlink.RouteGet(net.ParseIP("8.8.8.8"))
	if err != nil || len(before) == 0 {
		t.Fatal("missing original route")
	}
	parent, child := testPair(t)
	dns := &fakeDNS{}
	done := make(chan error, 1)
	go func() { done <- Serve(child, uint32(os.Getuid()), dns) }()
	file, name, err := RequestTunnel(parent, netip.MustParsePrefix("172.19.0.1/32"), netip.MustParseAddr("1.1.1.1"))
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	if err = Activate(parent); err != nil {
		t.Fatal(err)
	}
	link, err := net.InterfaceByName(name)
	if err != nil {
		t.Fatal(err)
	}
	active, err := netlink.RouteGet(net.ParseIP("8.8.8.8"))
	if err != nil || len(active) == 0 || active[0].LinkIndex != link.Index {
		t.Fatal("tunnel route not active")
	}
	// Simulate a crashed client while a duplicate TUN descriptor is still open.
	parent.Close()
	select {
	case err = <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("helper failed to exit")
	}
	if !dns.applied || !dns.reverted {
		t.Fatal("DNS lifecycle incomplete")
	}
	after, err := netlink.RouteGet(net.ParseIP("8.8.8.8"))
	if err != nil || len(after) == 0 || after[0].LinkIndex != before[0].LinkIndex {
		t.Fatal("route not restored after disconnect")
	}
	if _, err = net.InterfaceByName(name); err != nil {
		t.Fatal("client-held TUN should still exist")
	}
}

func TestSeparateHelperTransfersTUNToUnprivilegedClient(t *testing.T) {
	binary := os.Getenv("QUANTWAY_TEST_NET_HELPER")
	if binary == "" {
		t.Skip("requires isolated setuid test helper")
	}
	if os.Geteuid() == 0 {
		t.Fatal("test client must be unprivileged")
	}
	parent, child, err := NewPair()
	if err != nil {
		t.Fatal(err)
	}
	defer parent.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, binary)
	cmd.Stdin = child
	cmd.Env = append(os.Environ(), "PKEXEC_UID="+strconv.Itoa(os.Getuid()))
	if err = cmd.Start(); err != nil {
		child.Close()
		t.Fatal(err)
	}
	child.Close()
	file, name, err := RequestTunnel(parent, netip.MustParsePrefix("172.19.0.1/32"), netip.MustParseAddr("1.1.1.1"))
	if err != nil {
		cmd.Wait()
		t.Fatal(err)
	}
	if _, err = net.InterfaceByName(name); err != nil {
		file.Close()
		t.Fatal("received TUN not visible")
	}
	file.Close()
	parent.Close()
	_ = cmd.Wait() // disconnecting before activation makes the helper exit with an error
	if ctx.Err() != nil {
		t.Fatal("helper did not stop after client exit")
	}
}

func TestExplicitStopAcknowledgesCleanup(t *testing.T) {
	if os.Getenv("QUANTWAY_TUN_TEST") != "1" {
		t.Skip("requires isolated network namespace and CAP_NET_ADMIN")
	}
	parent, child := testPair(t)
	defer parent.Close()
	dns := &fakeDNS{}
	done := make(chan error, 1)
	go func() { done <- Serve(child, uint32(os.Getuid()), dns) }()
	file, _, err := RequestTunnel(parent, netip.MustParsePrefix("172.19.0.1/32"), netip.MustParseAddr("1.1.1.1"))
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	if err = Activate(parent); err != nil {
		t.Fatal(err)
	}
	if err = Stop(parent); err != nil {
		t.Fatalf("stop: %v", err)
	}
	select {
	case err = <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("helper did not finish")
	}
	if !dns.reverted {
		t.Fatal("stop acknowledged before DNS revert")
	}
}

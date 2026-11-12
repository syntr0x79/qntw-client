//go:build linux

package vpn

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/netip"
	"os"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/syntr0x79/qntw-client/internal/mobileprobe/wire"
	"github.com/vishvananda/netlink"
	"github.com/xtls/xray-core/core"
	"google.golang.org/protobuf/proto"
)

type testNetwork struct{}

func (testNetwork) PrepareSocket(int) error        { return nil }
func (testNetwork) Resolve(string) (string, error) { return "127.0.0.1", nil }

type testDNS struct {
	name    string
	reverts atomic.Int32
}

func (d *testDNS) Apply(_ context.Context, name string, _ netip.Addr) error {
	d.name = name
	return nil
}
func (d *testDNS) Revert(_ context.Context, _ string) error { d.reverts.Add(1); return nil }
func testConfig(t *testing.T, dns DNS) Config {
	t.Helper()
	cfg, err := core.LoadConfig("json", strings.NewReader(`{"outbounds":[{"protocol":"vless","settings":{"vnext":[{"address":"127.0.0.1","port":443,"users":[{"id":"11111111-2222-4333-8444-555555555555","encryption":"none"}]}]}}]}`))
	if err != nil {
		t.Fatal(err)
	}
	out, err := proto.Marshal(cfg.Outbound[0])
	if err != nil {
		t.Fatal(err)
	}
	job, err := json.Marshal(wire.Job{ID: "test", Engine: "xray", Outbound: out})
	if err != nil {
		t.Fatal(err)
	}
	return Config{Job: string(job), Address: netip.MustParsePrefix("172.19.0.1/32"), DNSAddress: netip.MustParseAddr("1.1.1.1"), Network: testNetwork{}, ControlURL: "https://example.com/", DNS: dns}
}
func TestSessionLifecycleAndFailureRollback(t *testing.T) {
	if os.Getenv("QUANTWAY_TUN_TEST") != "1" {
		t.Skip("requires isolated network namespace and CAP_NET_ADMIN")
	}
	before, err := netlink.RouteGet(net.ParseIP("8.8.8.8"))
	if err != nil || len(before) == 0 {
		t.Fatal("no original route")
	}
	dns := &testDNS{}
	cfg := testConfig(t, dns)
	cfg.Verify = func(context.Context) error { return errors.New("simulated health failure") }
	if _, err = Start(context.Background(), cfg); err == nil {
		t.Fatal("expected health failure")
	}
	if dns.reverts.Load() != 1 {
		t.Fatal("DNS was not reverted")
	}
	if _, err = net.InterfaceByName(dns.name); err == nil {
		t.Fatal("TUN remains after failed start")
	}
	after, err := netlink.RouteGet(net.ParseIP("8.8.8.8"))
	if err != nil || len(after) == 0 || before[0].LinkIndex != after[0].LinkIndex {
		t.Fatal("route not restored")
	}
	cfg.Verify = func(context.Context) error { return nil }
	s, err := Start(context.Background(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = Start(context.Background(), cfg); err == nil {
		t.Fatal("second concurrent VPN accepted")
	}
	if err = s.Close(); err != nil {
		t.Fatal(err)
	}
	if err = s.Close(); err != nil {
		t.Fatal(err)
	}
	if dns.reverts.Load() != 2 {
		t.Fatalf("expected one revert per session, got %d", dns.reverts.Load())
	}
	after, err = netlink.RouteGet(net.ParseIP("8.8.8.8"))
	if err != nil || len(after) == 0 || before[0].LinkIndex != after[0].LinkIndex {
		t.Fatal("route not restored after close")
	}
}

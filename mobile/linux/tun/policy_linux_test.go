//go:build linux

package tun

import (
	"net"
	"net/netip"
	"os"
	"testing"

	netroute "github.com/libp2p/go-netroute"
	"github.com/vishvananda/netlink"
)

func TestPolicyProtectsMoreSpecificRoutes(t *testing.T) {
	if os.Getenv("QUANTWAY_TUN_TEST") != "1" {
		t.Skip("requires isolated network namespace and CAP_NET_ADMIN")
	}
	router, err := netroute.New()
	if err != nil {
		t.Fatal(err)
	}
	iface, _, _, err := router.Route(net.ParseIP("1.1.1.1"))
	if err != nil || iface == nil {
		t.Fatal("missing physical route")
	}
	_, prefix, _ := net.ParseCIDR("198.18.0.0/16")
	specific := netlink.Route{LinkIndex: iface.Index, Dst: prefix, Scope: netlink.SCOPE_LINK}
	if err = netlink.RouteAdd(&specific); err != nil {
		t.Fatal(err)
	}
	defer netlink.RouteDel(&specific)
	d, err := Create()
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	if err = d.Configure(netip.MustParsePrefix("172.19.0.1/32"), 1280); err != nil {
		t.Fatal(err)
	}
	tunLink, err := netlink.LinkByName(d.Name)
	if err != nil {
		t.Fatal(err)
	}
	policy, err := d.InstallPolicy(iface.Name)
	if err != nil {
		t.Fatal(err)
	}
	defer policy.Remove()
	target := net.ParseIP("198.18.0.1")
	routes, err := netlink.RouteGet(target)
	if err != nil || len(routes) == 0 || routes[0].LinkIndex != tunLink.Attrs().Index {
		t.Fatalf("unbound route bypasses tunnel: %+v %v", routes, err)
	}
	routes, err = netlink.RouteGetWithOptions(target, &netlink.RouteGetOptions{Oif: iface.Name})
	if err != nil || len(routes) == 0 || routes[0].LinkIndex != iface.Index {
		t.Fatalf("bound route doesn't use physical network: %+v %v", routes, err)
	}
	if err = policy.Remove(); err != nil {
		t.Fatal(err)
	}
	routes, err = netlink.RouteGet(target)
	if err != nil || len(routes) == 0 || routes[0].LinkIndex != iface.Index {
		t.Fatalf("route not restored: %+v %v", routes, err)
	}
}

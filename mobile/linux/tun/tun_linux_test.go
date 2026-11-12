//go:build linux

package tun

import (
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"github.com/amnezia-vpn/amneziawg-go/v3/conn"
	"github.com/amnezia-vpn/amneziawg-go/v3/device"
	"github.com/amnezia-vpn/amneziawg-go/v3/tun/netstack"
	netroute "github.com/libp2p/go-netroute"
	"github.com/syntr0x79/qntw-client/internal/mobileprobe/wire"
	"github.com/syntr0x79/qntw-client/internal/topology"
	"github.com/syntr0x79/qntw-client/mobile/engine"
	"github.com/vishvananda/netlink"
	"github.com/xtls/xray-core/core"
	"golang.org/x/sys/unix"
	"google.golang.org/protobuf/proto"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestDeviceLifecycleIsolated(t *testing.T) {
	if os.Getenv("QUANTWAY_TUN_TEST") != "1" {
		t.Skip("requires isolated network namespace and CAP_NET_ADMIN")
	}
	d, err := Create()
	if err != nil {
		t.Fatal(err)
	}
	name := d.Name
	if err = d.Configure(netip.MustParsePrefix("172.19.0.1/32"), 1280); err != nil {
		t.Fatal(err)
	}
	iface, err := net.InterfaceByName(name)
	if err != nil || iface.MTU != 1280 || iface.Flags&net.FlagUp == 0 {
		t.Fatalf("invalid interface: %+v %v", iface, err)
	}
	if _, err = net.InterfaceByName(name); err != nil {
		t.Fatalf("interface not created: %v", err)
	}
	if err = d.Close(); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 30; i++ {
		if _, err = net.InterfaceByName(name); err != nil {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("interface was not removed after closing")
}

type localNetwork struct{}

func (localNetwork) PrepareSocket(int) error        { return nil }
func (localNetwork) Resolve(string) (string, error) { return "127.0.0.1", nil }

func TestXrayStartsWithLinuxTUN(t *testing.T) {
	if os.Getenv("QUANTWAY_TUN_TEST") != "1" {
		t.Skip("requires isolated network namespace and CAP_NET_ADMIN")
	}
	device, err := Create()
	if err != nil {
		t.Fatal(err)
	}
	defer device.Close()
	config, err := core.LoadConfig("json", strings.NewReader(`{"outbounds":[{"protocol":"vless","settings":{"vnext":[{"address":"127.0.0.1","port":443,"users":[{"id":"11111111-2222-4333-8444-555555555555","encryption":"none"}]}]}}]}`))
	if err != nil {
		t.Fatal(err)
	}
	out, err := proto.Marshal(config.Outbound[0])
	if err != nil {
		t.Fatal(err)
	}
	job, err := json.Marshal(wire.Job{ID: "test", Engine: "xray", Outbound: out})
	if err != nil {
		t.Fatal(err)
	}
	if err = engine.StartVPN(int(device.File.Fd()), string(job), localNetwork{}); err != nil {
		t.Fatalf("start xray TUN: %v", err)
	}
	engine.StopVPN()
}

func TestAWGStartsWithLinuxTUN(t *testing.T) {
	if os.Getenv("QUANTWAY_TUN_TEST") != "1" {
		t.Skip("requires isolated network namespace and CAP_NET_ADMIN")
	}
	device, err := Create()
	if err != nil {
		t.Fatal(err)
	}
	defer device.Close()
	params, err := topology.NewAWGParams(51820)
	if err != nil {
		t.Fatal(err)
	}
	_, params.ServerPublicKey, err = topology.NewAWGKeyPair()
	if err != nil {
		t.Fatal(err)
	}
	private, _, err := topology.NewAWGKeyPair()
	if err != nil {
		t.Fatal(err)
	}
	conf, err := topology.BuildAWGConf(topology.Inbound{Host: "127.0.0.1", Port: 51820}, params, topology.AWGClient{PrivateKey: private, Address: netip.MustParseAddr("10.66.0.2")})
	if err != nil {
		t.Fatal(err)
	}
	job, err := json.Marshal(wire.Job{ID: "test-awg", Engine: "amneziawg", AWGConf: conf})
	if err != nil {
		t.Fatal(err)
	}
	if err = engine.StartVPN(int(device.File.Fd()), string(job), localNetwork{}); err != nil {
		t.Fatalf("start AWG TUN: %v", err)
	}
	engine.StopVPN()
}

func TestRoutesAreRemovedWithTunnel(t *testing.T) {
	if os.Getenv("QUANTWAY_TUN_TEST") != "1" {
		t.Skip("requires isolated network namespace and CAP_NET_ADMIN")
	}
	before, err := netlink.RouteGet(net.ParseIP("8.8.8.8"))
	if err != nil || len(before) == 0 {
		t.Fatalf("original route: %v", err)
	}
	d, err := Create()
	if err != nil {
		t.Fatal(err)
	}
	if err = d.Configure(netip.MustParsePrefix("172.19.0.1/32"), 1280); err != nil {
		d.Close()
		t.Fatal(err)
	}
	link, err := netlink.LinkByName(d.Name)
	if err != nil {
		d.Close()
		t.Fatal(err)
	}
	if err = d.InstallRoutes(); err != nil {
		d.Close()
		t.Fatal(err)
	}
	routes, err := netlink.RouteGet(net.ParseIP("8.8.8.8"))
	if err != nil || len(routes) == 0 || routes[0].LinkIndex != link.Attrs().Index {
		d.Close()
		t.Fatalf("tunnel route not used: %v", err)
	}
	if err = d.Close(); err != nil {
		t.Fatal(err)
	}
	after, err := netlink.RouteGet(net.ParseIP("8.8.8.8"))
	if err != nil || len(after) == 0 || after[0].LinkIndex != before[0].LinkIndex {
		t.Fatalf("original route not restored: %v", err)
	}
}

func TestConflictingRouteRollsBackOwnChanges(t *testing.T) {
	if os.Getenv("QUANTWAY_TUN_TEST") != "1" {
		t.Skip("requires isolated network namespace and CAP_NET_ADMIN")
	}
	existing, err := Create()
	if err != nil {
		t.Fatal(err)
	}
	defer existing.Close()
	if err = existing.Configure(netip.MustParsePrefix("172.19.1.1/32"), 1280); err != nil {
		t.Fatal(err)
	}
	other, err := netlink.LinkByName(existing.Name)
	if err != nil {
		t.Fatal(err)
	}
	_, dst, _ := net.ParseCIDR("128.0.0.0/1")
	old := netlink.Route{LinkIndex: other.Attrs().Index, Dst: dst, Scope: netlink.SCOPE_LINK}
	if err = netlink.RouteAdd(&old); err != nil {
		t.Fatal(err)
	}
	d, err := Create()
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	if err = d.Configure(netip.MustParsePrefix("172.19.0.1/32"), 1280); err != nil {
		t.Fatal(err)
	}
	if err = d.InstallRoutes(); err == nil {
		t.Fatal("expected conflicting route")
	}
	routes, err := netlink.RouteGet(net.ParseIP("8.8.8.8"))
	if err != nil || len(routes) == 0 {
		t.Fatal("missing original route")
	}
	if routes[0].LinkIndex == other.Attrs().Index {
		t.Fatal("unexpected route for other destination")
	}
	otherRoutes, err := netlink.RouteGet(net.ParseIP("200.1.1.1"))
	if err != nil || len(otherRoutes) == 0 || otherRoutes[0].LinkIndex != other.Attrs().Index {
		t.Fatal("existing route was altered")
	}
}

type pinnedNetwork struct{ iface string }

func (n pinnedNetwork) PrepareSocket(fd int) error {
	return unix.SetsockoptString(fd, unix.SOL_SOCKET, unix.SO_BINDTODEVICE, n.iface)
}
func (pinnedNetwork) Resolve(s string) (string, error) { return s, nil }

// This test modifies routes only inside the disposable container's namespace.
func TestXrayCarriesSystemTrafficAndRestoresRoute(t *testing.T) {
	if os.Getenv("QUANTWAY_TUN_TEST") != "1" || os.Getenv("QUANTWAY_TEST_XRAY") == "" {
		t.Skip("requires isolated network namespace, CAP_NET_ADMIN and test Xray")
	}
	router, err := netroute.New()
	if err != nil {
		t.Fatal(err)
	}
	iface, _, source, err := router.Route(net.ParseIP("1.1.1.1"))
	if err != nil || iface == nil || source == nil {
		t.Fatal("no test network")
	}
	before, err := netlink.RouteGet(net.ParseIP("198.18.0.2"))
	if err != nil || len(before) == 0 {
		t.Fatal("no original route")
	}
	destination := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusNoContent) }))
	defer destination.Close()
	destURL, err := url.Parse(destination.URL)
	if err != nil {
		t.Fatal(err)
	}
	listener, err := net.Listen("tcp4", net.JoinHostPort(source.String(), "0"))
	if err != nil {
		t.Fatal(err)
	}
	port := listener.Addr().(*net.TCPAddr).Port
	listener.Close()
	const id = "11111111-2222-4333-8444-555555555555"
	document := fmt.Sprintf(`{"log":{"loglevel":"none"},"inbounds":[{"listen":%q,"port":%d,"protocol":"vless","settings":{"clients":[{"id":%q}],"decryption":"none"}}],"outbounds":[{"protocol":"freedom","settings":{"redirect":%q,"finalRules":[{"action":"allow","ip":["127.0.0.0/8"]}]}}]}`, source.String(), port, id, destURL.Host)
	path := filepath.Join(t.TempDir(), "xray.json")
	if err = os.WriteFile(path, []byte(document), 0600); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(os.Getenv("QUANTWAY_TEST_XRAY"), "run", "-c", path)
	if err = cmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = cmd.Process.Kill(); _ = cmd.Wait() }()
	address := net.JoinHostPort(source.String(), strconv.Itoa(port))
	ready := false
	for i := 0; i < 100; i++ {
		conn, e := net.DialTimeout("tcp", address, 100*time.Millisecond)
		if e == nil {
			conn.Close()
			ready = true
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if !ready {
		t.Fatal("test Xray did not start")
	}
	config, err := core.LoadConfig("json", strings.NewReader(fmt.Sprintf(`{"outbounds":[{"protocol":"vless","settings":{"vnext":[{"address":%q,"port":%d,"users":[{"id":%q,"encryption":"none"}]}]}}]}`, source.String(), port, id)))
	if err != nil {
		t.Fatal(err)
	}
	out, err := proto.Marshal(config.Outbound[0])
	if err != nil {
		t.Fatal(err)
	}
	job, _ := json.Marshal(wire.Job{ID: "test", Engine: "xray", Outbound: out})
	device, err := Create()
	if err != nil {
		t.Fatal(err)
	}
	defer device.Close()
	if err = device.Configure(netip.MustParsePrefix("172.19.0.1/32"), 1280); err != nil {
		t.Fatal(err)
	}
	if err = engine.StartVPN(int(device.File.Fd()), string(job), pinnedNetwork{iface: iface.Name}); err != nil {
		t.Fatal(err)
	}
	defer engine.StopVPN()
	if err = device.InstallRoutes(); err != nil {
		t.Fatal(err)
	}
	client := http.Client{Timeout: 10 * time.Second, Transport: &http.Transport{Proxy: nil, DisableKeepAlives: true}}
	resp, err := client.Get("http://198.18.0.2/")
	if err != nil {
		t.Fatalf("system traffic through tunnel: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("unexpected response: %d", resp.StatusCode)
	}
	engine.StopVPN()
	if err = device.Close(); err != nil {
		t.Fatal(err)
	}
	after, err := netlink.RouteGet(net.ParseIP("198.18.0.2"))
	if err != nil || len(after) == 0 || before[0].LinkIndex != after[0].LinkIndex {
		t.Fatalf("route not restored: %v", err)
	}
}

func TestAWGCarriesSystemTrafficAndRestoresRoute(t *testing.T) {
	if os.Getenv("QUANTWAY_TUN_TEST") != "1" {
		t.Skip("requires isolated network namespace and CAP_NET_ADMIN")
	}
	router, err := netroute.New()
	if err != nil {
		t.Fatal(err)
	}
	iface, _, source, err := router.Route(net.ParseIP("1.1.1.1"))
	if err != nil || iface == nil || source == nil {
		t.Fatal("no test network")
	}
	before, err := netlink.RouteGet(net.ParseIP("10.66.0.1"))
	if err != nil || len(before) == 0 {
		t.Fatal("no original route")
	}
	params, err := topology.NewAWGParams(0)
	if err != nil {
		t.Fatal(err)
	}
	serverPrivate, serverPublic, err := topology.NewAWGKeyPair()
	if err != nil {
		t.Fatal(err)
	}
	clientPrivate, clientPublic, err := topology.NewAWGKeyPair()
	if err != nil {
		t.Fatal(err)
	}
	params.ServerPublicKey = serverPublic
	keyHex := func(s string) string {
		b, e := base64.StdEncoding.DecodeString(s)
		if e != nil {
			t.Fatal(e)
		}
		return hex.EncodeToString(b)
	}
	serverTun, serverNet, err := netstack.CreateNetTUN([]netip.Addr{netip.MustParseAddr("10.66.0.1")}, nil, params.MTU)
	if err != nil {
		t.Fatal(err)
	}
	server := device.NewDevice(serverTun, conn.NewDefaultBind(), device.NewLogger(device.LogLevelSilent, "test"))
	defer server.Close()
	uapi := strings.Join([]string{"private_key=" + keyHex(serverPrivate), "listen_port=0", fmt.Sprintf("s1=%d", params.S1), fmt.Sprintf("s2=%d", params.S2), fmt.Sprintf("s3=%d", params.S3), fmt.Sprintf("s4=%d", params.S4), "h1=" + params.H1, "h2=" + params.H2, "h3=" + params.H3, "h4=" + params.H4, "header_protection_key=" + keyHex(params.HeaderProtectionKey), "public_key=" + keyHex(clientPublic), "allowed_ip=10.66.0.2/32"}, "\n") + "\n"
	if err = server.IpcSet(uapi); err != nil {
		t.Fatal(err)
	}
	if err = server.Up(); err != nil {
		t.Fatal(err)
	}
	listener, err := serverNet.ListenTCP(&net.TCPAddr{Port: 80})
	if err != nil {
		t.Fatal(err)
	}
	service := http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusNoContent) })}
	go service.Serve(listener)
	defer service.Close()
	details, err := server.IpcGet()
	if err != nil {
		t.Fatal(err)
	}
	var port int
	for _, line := range strings.Split(details, "\n") {
		if v, ok := strings.CutPrefix(line, "listen_port="); ok {
			_, _ = fmt.Sscan(v, &port)
		}
	}
	if port == 0 {
		t.Fatal("missing server port")
	}
	conf, err := topology.BuildAWGConf(topology.Inbound{Host: source.String(), Port: port}, params, topology.AWGClient{PrivateKey: clientPrivate, Address: netip.MustParseAddr("10.66.0.2")})
	if err != nil {
		t.Fatal(err)
	}
	job, err := json.Marshal(wire.Job{ID: "test-awg", Engine: "amneziawg", AWGConf: conf})
	if err != nil {
		t.Fatal(err)
	}
	clientTun, err := Create()
	if err != nil {
		t.Fatal(err)
	}
	defer clientTun.Close()
	if err = clientTun.Configure(netip.MustParsePrefix("10.66.0.2/32"), 1280); err != nil {
		t.Fatal(err)
	}
	if err = engine.StartVPN(int(clientTun.File.Fd()), string(job), pinnedNetwork{iface: iface.Name}); err != nil {
		t.Fatal(err)
	}
	defer engine.StopVPN()
	if err = clientTun.InstallRoutes(); err != nil {
		t.Fatal(err)
	}
	client := http.Client{Timeout: 10 * time.Second, Transport: &http.Transport{Proxy: nil, DisableKeepAlives: true}}
	resp, err := client.Get("http://10.66.0.1/")
	if err != nil {
		t.Fatalf("system traffic through AWG: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("unexpected response: %d", resp.StatusCode)
	}
	engine.StopVPN()
	if err = clientTun.Close(); err != nil {
		t.Fatal(err)
	}
	after, err := netlink.RouteGet(net.ParseIP("10.66.0.1"))
	if err != nil || len(after) == 0 || before[0].LinkIndex != after[0].LinkIndex {
		t.Fatalf("route not restored: %v", err)
	}
}

func TestRemoveRoutesWhileAnotherProcessHoldsTUN(t *testing.T) {
	if os.Getenv("QUANTWAY_TUN_TEST") != "1" {
		t.Skip("requires isolated network namespace and CAP_NET_ADMIN")
	}
	before, err := netlink.RouteGet(net.ParseIP("8.8.8.8"))
	if err != nil || len(before) == 0 {
		t.Fatal("no original route")
	}
	d, err := Create()
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	if err = d.Configure(netip.MustParsePrefix("172.19.0.1/32"), 1280); err != nil {
		t.Fatal(err)
	}
	borrowed, err := unix.Dup(int(d.File.Fd()))
	if err != nil {
		t.Fatal(err)
	}
	defer unix.Close(borrowed)
	if err = d.InstallRoutes(); err != nil {
		t.Fatal(err)
	}
	if err = d.RemoveRoutes(); err != nil {
		t.Fatal(err)
	}
	if _, err = net.InterfaceByName(d.Name); err != nil {
		t.Fatal("interface unexpectedly removed")
	}
	after, err := netlink.RouteGet(net.ParseIP("8.8.8.8"))
	if err != nil || len(after) == 0 || before[0].LinkIndex != after[0].LinkIndex {
		t.Fatal("route not restored")
	}
}

//go:build linux

package desktopvpn

import (
	"context"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/amnezia-vpn/amneziawg-go/v3/conn"
	"github.com/amnezia-vpn/amneziawg-go/v3/device"
	"github.com/amnezia-vpn/amneziawg-go/v3/tun/netstack"
	netroute "github.com/libp2p/go-netroute"
	"github.com/syntr0x79/qntw-client/internal/topology"
	"golang.org/x/sys/unix"
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
	"sync/atomic"
	"testing"
	"time"

	"github.com/syntr0x79/qntw-client/internal/mobileprobe/wire"
	"github.com/syntr0x79/qntw-client/mobile/linux/privsep"
	"github.com/vishvananda/netlink"
	"github.com/xtls/xray-core/core"
	"google.golang.org/protobuf/proto"
)

type network struct{}

func (network) PrepareSocket(int) error        { return nil }
func (network) Resolve(string) (string, error) { return "127.0.0.1", nil }

type fakeDNS struct{ reverted atomic.Bool }

func (*fakeDNS) Apply(context.Context, string, netip.Addr) error { return nil }
func (d *fakeDNS) Revert(context.Context, string) error          { d.reverted.Store(true); return nil }
func testConfig(t *testing.T, dns *fakeDNS) Config {
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
	launch := func(_ context.Context, f *os.File) (<-chan error, error) {
		c, err := net.FileConn(f)
		if err != nil {
			return nil, err
		}
		u, ok := c.(*net.UnixConn)
		if !ok {
			return nil, errors.New("not unix")
		}
		done := make(chan error, 1)
		go func() { done <- privsep.Serve(u, uint32(os.Getuid()), dns); close(done) }()
		return done, nil
	}
	return Config{Job: string(job), Address: netip.MustParsePrefix("172.19.0.1/32"), DNS: netip.MustParseAddr("1.1.1.1"), Network: network{}, ControlURL: "https://example.com/", verify: func() error { return nil }, launch: launch}
}
func TestSessionSeparatesHelperAndRestoresRoutes(t *testing.T) {
	if os.Getenv("QUANTWAY_TUN_TEST") != "1" {
		t.Skip("requires isolated network namespace and CAP_NET_ADMIN")
	}
	before, err := netlink.RouteGet(net.ParseIP("8.8.8.8"))
	if err != nil || len(before) == 0 {
		t.Fatal("missing original route")
	}
	dns := &fakeDNS{}
	cfg := testConfig(t, dns)
	session, err := Start(context.Background(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = Start(context.Background(), cfg); err == nil {
		t.Fatal("duplicate session accepted")
	}
	if err = session.Close(); err != nil {
		t.Fatal(err)
	}
	if !dns.reverted.Load() {
		t.Fatal("DNS not restored")
	}
	after, err := netlink.RouteGet(net.ParseIP("8.8.8.8"))
	if err != nil || len(after) == 0 || after[0].LinkIndex != before[0].LinkIndex {
		t.Fatal("route not restored")
	}
}
func TestHealthFailureUnwindsSeparateHelper(t *testing.T) {
	if os.Getenv("QUANTWAY_TUN_TEST") != "1" {
		t.Skip("requires isolated network namespace and CAP_NET_ADMIN")
	}
	dns := &fakeDNS{}
	cfg := testConfig(t, dns)
	cfg.verify = func() error { return errors.New("test health failure") }
	if _, err := Start(context.Background(), cfg); err == nil {
		t.Fatal("failed health check accepted")
	}
	if !dns.reverted.Load() {
		t.Fatal("DNS not restored after health failure")
	}
}
func TestRejectsUserWritableHelper(t *testing.T) {
	path := filepath.Join(t.TempDir(), "helper")
	if err := os.WriteFile(path, []byte("test"), 0777); err != nil {
		t.Fatal(err)
	}
	if err := trustedHelper(path); err == nil {
		t.Fatal("user-writable helper accepted")
	}
}

func TestHelperFailureStopsEngineAndRestoresRoutes(t *testing.T) {
	if os.Getenv("QUANTWAY_TUN_TEST") != "1" {
		t.Skip("requires isolated network namespace and CAP_NET_ADMIN")
	}
	before, err := netlink.RouteGet(net.ParseIP("8.8.8.8"))
	if err != nil || len(before) == 0 {
		t.Fatal("missing original route")
	}
	dns := &fakeDNS{}
	cfg := testConfig(t, dns)
	var server *net.UnixConn
	cfg.launch = func(_ context.Context, f *os.File) (<-chan error, error) {
		c, err := net.FileConn(f)
		if err != nil {
			return nil, err
		}
		server = c.(*net.UnixConn)
		done := make(chan error, 1)
		go func() { done <- privsep.Serve(server, uint32(os.Getuid()), dns); close(done) }()
		return done, nil
	}
	session, err := Start(context.Background(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	server.Close()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		after, e := netlink.RouteGet(net.ParseIP("8.8.8.8"))
		if e == nil && len(after) > 0 && before[0].LinkIndex == after[0].LinkIndex {
			_ = session.Close()
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	_ = session.Close()
	t.Fatal("route not restored after helper failure")
}

type pinned struct{ name string }

func (p pinned) PrepareSocket(fd int) error {
	return unix.SetsockoptString(fd, unix.SOL_SOCKET, unix.SO_BINDTODEVICE, p.name)
}
func (p pinned) Resolve(host string) (string, error) { return host, nil }

// The client runs unprivileged; only a disposable test fixture owns CAP_NET_ADMIN.
func TestUnprivilegedXrayTrafficWithSeparateHelper(t *testing.T) {
	helper, xray := os.Getenv("QUANTWAY_TEST_NET_HELPER"), os.Getenv("QUANTWAY_TEST_XRAY")
	if helper == "" || xray == "" {
		t.Skip("requires isolated setuid fixture and Xray")
	}
	if os.Geteuid() == 0 {
		t.Fatal("client must be unprivileged")
	}
	router, err := netroute.New()
	if err != nil {
		t.Fatal(err)
	}
	iface, _, source, err := router.Route(net.ParseIP("1.1.1.1"))
	if err != nil || iface == nil || source == nil {
		t.Fatal("no physical route")
	}
	before, err := netlink.RouteGet(net.ParseIP("198.18.0.2"))
	if err != nil || len(before) == 0 {
		t.Fatal("no original route")
	}
	destination := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusNoContent) }))
	defer destination.Close()
	target, err := url.Parse(destination.URL)
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
	config := fmt.Sprintf(`{"log":{"loglevel":"none"},"inbounds":[{"listen":%q,"port":%d,"protocol":"vless","settings":{"clients":[{"id":%q}],"decryption":"none"}}],"outbounds":[{"protocol":"freedom","settings":{"redirect":%q,"finalRules":[{"action":"allow","ip":["127.0.0.0/8"]}]}}]}`, source.String(), port, id, target.Host)
	path := filepath.Join(t.TempDir(), "server.json")
	if err = os.WriteFile(path, []byte(config), 0600); err != nil {
		t.Fatal(err)
	}
	server := exec.Command(xray, "run", "-c", path)
	if err = server.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = server.Process.Kill(); _ = server.Wait() }()
	ready := false
	address := net.JoinHostPort(source.String(), strconv.Itoa(port))
	for i := 0; i < 100; i++ {
		c, e := net.DialTimeout("tcp", address, 100*time.Millisecond)
		if e == nil {
			c.Close()
			ready = true
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if !ready {
		t.Fatal("test server not ready")
	}
	outbound, err := core.LoadConfig("json", strings.NewReader(fmt.Sprintf(`{"outbounds":[{"protocol":"vless","settings":{"vnext":[{"address":%q,"port":%d,"users":[{"id":%q,"encryption":"none"}]}]}}]}`, source.String(), port, id)))
	if err != nil {
		t.Fatal(err)
	}
	raw, err := proto.Marshal(outbound.Outbound[0])
	if err != nil {
		t.Fatal(err)
	}
	job, err := json.Marshal(wire.Job{ID: "test", Engine: "xray", Outbound: raw})
	if err != nil {
		t.Fatal(err)
	}
	launch := func(_ context.Context, f *os.File) (<-chan error, error) {
		cmd := exec.Command(helper)
		cmd.Stdin = f
		cmd.Env = append(os.Environ(), "TEST_UID="+strconv.Itoa(os.Getuid()))
		if e := cmd.Start(); e != nil {
			return nil, e
		}
		done := make(chan error, 1)
		go func() { done <- cmd.Wait(); close(done) }()
		return done, nil
	}
	verify := func() error {
		client := http.Client{Timeout: 10 * time.Second, Transport: &http.Transport{Proxy: nil, DisableKeepAlives: true}}
		resp, e := client.Get("http://198.18.0.2/")
		if e != nil {
			return e
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusNoContent {
			return errors.New("unexpected status")
		}
		return nil
	}
	cfg := Config{Job: string(job), Address: netip.MustParsePrefix("172.19.0.1/32"), DNS: netip.MustParseAddr("1.1.1.1"), Network: pinned{name: iface.Name}, ControlURL: "https://example.com/", launch: launch, verify: verify}
	session, err := Start(context.Background(), cfg)
	if err != nil {
		t.Fatalf("connect through separate helper: %v", err)
	}
	if err = session.Close(); err != nil {
		t.Fatal(err)
	}
	after, err := netlink.RouteGet(net.ParseIP("198.18.0.2"))
	if err != nil || len(after) == 0 || after[0].LinkIndex != before[0].LinkIndex {
		t.Fatal("route not restored")
	}
}

func TestUnprivilegedAWGTrafficWithSeparateHelper(t *testing.T) {
	helper := os.Getenv("QUANTWAY_TEST_NET_HELPER")
	if helper == "" {
		t.Skip("requires isolated setuid fixture")
	}
	if os.Geteuid() == 0 {
		t.Fatal("client must be unprivileged")
	}
	router, err := netroute.New()
	if err != nil {
		t.Fatal(err)
	}
	iface, _, source, err := router.Route(net.ParseIP("1.1.1.1"))
	if err != nil || iface == nil || source == nil {
		t.Fatal("no physical route")
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
	launch := func(_ context.Context, f *os.File) (<-chan error, error) {
		cmd := exec.Command(helper)
		cmd.Stdin = f
		cmd.Env = append(os.Environ(), "TEST_UID="+strconv.Itoa(os.Getuid()))
		if e := cmd.Start(); e != nil {
			return nil, e
		}
		done := make(chan error, 1)
		go func() { done <- cmd.Wait(); close(done) }()
		return done, nil
	}
	verify := func() error {
		client := http.Client{Timeout: 10 * time.Second, Transport: &http.Transport{Proxy: nil, DisableKeepAlives: true}}
		resp, e := client.Get("http://10.66.0.1/")
		if e != nil {
			return e
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusNoContent {
			return errors.New("unexpected status")
		}
		return nil
	}
	cfg := Config{Job: string(job), Address: netip.MustParsePrefix("10.66.0.2/32"), DNS: netip.MustParseAddr("1.1.1.1"), Network: pinned{name: iface.Name}, ControlURL: "https://example.com/", launch: launch, verify: verify}
	session, err := Start(context.Background(), cfg)
	if err != nil {
		t.Fatalf("connect through separate helper: %v", err)
	}
	if err = session.Close(); err != nil {
		t.Fatal(err)
	}
	after, err := netlink.RouteGet(net.ParseIP("10.66.0.1"))
	if err != nil || len(after) == 0 || after[0].LinkIndex != before[0].LinkIndex {
		t.Fatal("route not restored")
	}
}

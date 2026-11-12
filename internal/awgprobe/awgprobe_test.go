package awgprobe

import (
	"context"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/netip"
	"regexp"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/amnezia-vpn/amneziawg-go/v3/conn"
	"github.com/amnezia-vpn/amneziawg-go/v3/device"
	"github.com/amnezia-vpn/amneziawg-go/v3/tun/netstack"

	"github.com/syntr0x79/qntw-client/internal/topology"
)

// testConf — клиентский .conf в нашем формате и всё, из чего он собран.
func testConf(t *testing.T, host string, port int) (conf string, p topology.AWGParams, clientPub, serverPriv string) {
	t.Helper()
	p, err := topology.NewAWGParams(port)
	if err != nil {
		t.Fatal(err)
	}
	p.I2 = "<b 0xdeadbeef><r 16>"
	serverPriv, serverPub, err := topology.NewAWGKeyPair()
	if err != nil {
		t.Fatal(err)
	}
	p.ServerPublicKey = serverPub
	clientPriv, clientPub, err := topology.NewAWGKeyPair()
	if err != nil {
		t.Fatal(err)
	}
	conf, err = topology.BuildAWGConf(topology.Inbound{Host: host, Port: port}, p,
		topology.AWGClient{PrivateKey: clientPriv, Address: netip.MustParseAddr("10.66.0.2")})
	if err != nil {
		t.Fatal(err)
	}
	return conf, p, clientPub, serverPriv
}

func b64hex(t *testing.T, s string) string {
	t.Helper()
	k, err := base64.StdEncoding.DecodeString(s)
	if err != nil {
		t.Fatal(err)
	}
	return hex.EncodeToString(k)
}

func TestConfToUAPI(t *testing.T) {
	conf, p, _, _ := testConf(t, "awg.example", 51820)
	c, err := parseConf(conf)
	if err != nil {
		t.Fatal(err)
	}
	var asked string
	resolve := func(_ context.Context, host string) (netip.Addr, error) {
		asked = host
		return netip.MustParseAddr("192.0.2.7"), nil
	}
	got, ep, err := c.uapi(context.Background(), resolve)
	if err != nil {
		t.Fatal(err)
	}
	if asked != "awg.example" || ep != netip.MustParseAddrPort("192.0.2.7:51820") {
		t.Fatalf("resolve asked %q, endpoint %s", asked, ep)
	}
	privB64 := strings.TrimSpace(strings.SplitN(strings.SplitN(conf, "PrivateKey = ", 2)[1], "\n", 2)[0])
	want := []string{
		"private_key=" + b64hex(t, privB64),
		fmt.Sprintf("jc=%d", p.Jc), fmt.Sprintf("jmin=%d", p.Jmin), fmt.Sprintf("jmax=%d", p.Jmax),
		fmt.Sprintf("s1=%d", p.S1), fmt.Sprintf("s2=%d", p.S2), fmt.Sprintf("s3=%d", p.S3), fmt.Sprintf("s4=%d", p.S4),
		"h1=" + p.H1, "h2=" + p.H2, "h3=" + p.H3, "h4=" + p.H4,
		"i1=" + p.I1, "i2=" + p.I2,
		"header_protection_key=" + b64hex(t, p.HeaderProtectionKey),
		"public_key=" + b64hex(t, p.ServerPublicKey),
		"endpoint=192.0.2.7:51820",
		fmt.Sprintf("persistent_keepalive_interval=%d", p.Keepalive),
		"allowed_ip=0.0.0.0/0",
	}
	lines := strings.Split(strings.TrimSpace(got), "\n")
	if len(lines) != len(want) {
		t.Fatalf("uapi:\n%s\nwant %d lines", got, len(want))
	}
	for i := range want {
		if lines[i] != want[i] {
			t.Errorf("line %d = %q, want %q", i, lines[i], want[i])
		}
	}
	if c.address != netip.MustParseAddr("10.66.0.2") || c.mtu != p.MTU ||
		len(c.dns) != 1 || c.dns[0] != netip.MustParseAddr(p.DNS) {
		t.Fatalf("interface: %+v", c)
	}
	// UAPI принимает всё, что мы ему отдали, — проверка о реальном
	// amneziawg-go, а не о наших строках.
	tn, _, err := netstack.CreateNetTUN([]netip.Addr{c.address}, c.dns, c.mtu)
	if err != nil {
		t.Fatal(err)
	}
	dev := device.NewDevice(tn, conn.NewDefaultBind(), device.NewLogger(device.LogLevelSilent, ""))
	defer dev.Close()
	if err := dev.IpcSet(got); err != nil {
		t.Fatalf("IpcSet: %v", err)
	}
}

func TestConfRejectsUnknownKey(t *testing.T) {
	conf, _, _, _ := testConf(t, "127.0.0.1", 1)
	conf = strings.Replace(conf, "[Peer]\n", "[Peer]\nPresharedKeyX = 1\n", 1)
	if _, err := parseConf(conf); err == nil {
		t.Fatal("unknown key accepted")
	}
}

// silentServer — UDP-сокет, который всё читает и ничего не отвечает.
func silentServer(t *testing.T) int {
	t.Helper()
	c, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { c.Close() })
	go func() {
		buf := make([]byte, 2048)
		for {
			if _, _, err := c.ReadFrom(buf); err != nil {
				return
			}
		}
	}()
	return c.LocalAddr().(*net.UDPAddr).Port
}

func TestProbeHandshakeTimeout(t *testing.T) {
	conf, _, _, _ := testConf(t, "127.0.0.1", silentServer(t))
	const timeout = 2 * time.Second
	start := time.Now()
	err := Probe(context.Background(), conf, Options{ControlURL: "http://10.66.0.1/", Timeout: timeout})
	if !errors.Is(err, ErrHandshake) {
		t.Fatalf("err = %v, want ErrHandshake", err)
	}
	if d := time.Since(start); d > timeout+time.Second {
		t.Fatalf("took %s", d)
	}
}

func TestProbeUsesListen(t *testing.T) {
	conf, _, _, _ := testConf(t, "127.0.0.1", silentServer(t))
	var called atomic.Int32
	var network atomic.Value
	listen := func(n string, laddr *net.UDPAddr) (*net.UDPConn, error) {
		called.Add(1)
		network.Store(n)
		return net.ListenUDP(n, laddr)
	}
	err := Probe(context.Background(), conf, Options{Listen: listen, ControlURL: "http://10.66.0.1/", Timeout: 500 * time.Millisecond})
	if !errors.Is(err, ErrHandshake) {
		t.Fatalf("err = %v", err)
	}
	if called.Load() == 0 || network.Load() != "udp4" {
		t.Fatalf("listen called %d times, network %v", called.Load(), network.Load())
	}
}

// inProcessServer — AWG-узел в процессе: amneziawg-go на netstack с
// адресом 10.66.0.1 и HTTP-ответчиком 204 внутри туннеля.
func inProcessServer(t *testing.T, p topology.AWGParams, serverPriv, clientPub string) int {
	t.Helper()
	tn, tnet, err := netstack.CreateNetTUN([]netip.Addr{netip.MustParseAddr("10.66.0.1")}, nil, p.MTU)
	if err != nil {
		t.Fatal(err)
	}
	dev := device.NewDevice(tn, newBind(nil, "udp4"), device.NewLogger(device.LogLevelSilent, "srv "))
	t.Cleanup(dev.Close)
	uapi := strings.Join([]string{
		"private_key=" + b64hex(t, serverPriv),
		"listen_port=0",
		fmt.Sprintf("s1=%d", p.S1), fmt.Sprintf("s2=%d", p.S2), fmt.Sprintf("s3=%d", p.S3), fmt.Sprintf("s4=%d", p.S4),
		"h1=" + p.H1, "h2=" + p.H2, "h3=" + p.H3, "h4=" + p.H4,
		"header_protection_key=" + b64hex(t, p.HeaderProtectionKey),
		"public_key=" + b64hex(t, clientPub),
		"allowed_ip=10.66.0.2/32",
	}, "\n") + "\n"
	if err := dev.IpcSet(uapi); err != nil {
		t.Fatal(err)
	}
	flushStaleRead(tnet) // та же гонка паддинга и у сервера, см. awgprobe.go
	if err := dev.Up(); err != nil {
		t.Fatal(err)
	}
	ln, err := tnet.ListenTCP(&net.TCPAddr{Port: 80})
	if err != nil {
		t.Fatal(err)
	}
	srv := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/fail" {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	})}
	go srv.Serve(ln)
	t.Cleanup(func() { srv.Close() })
	get, err := dev.IpcGet()
	if err != nil {
		t.Fatal(err)
	}
	for _, l := range strings.Split(get, "\n") {
		if v, ok := strings.CutPrefix(l, "listen_port="); ok {
			var port int
			fmt.Sscan(v, &port)
			return port
		}
	}
	t.Fatalf("no listen_port in %q", get)
	return 0
}

func TestProbeAgainstInProcessServer(t *testing.T) {
	p, err := topology.NewAWGParams(0)
	if err != nil {
		t.Fatal(err)
	}
	if p.S1 == 0 || p.H1 == "" || p.HeaderProtectionKey == "" || p.Jc == 0 || p.I1 == "" {
		t.Fatalf("obfuscation is off: %+v", p)
	}
	serverPriv, serverPub, _ := topology.NewAWGKeyPair()
	clientPriv, clientPub, _ := topology.NewAWGKeyPair()
	p.ServerPublicKey = serverPub
	port := inProcessServer(t, p, serverPriv, clientPub)
	conf, err := topology.BuildAWGConf(topology.Inbound{Host: "127.0.0.1", Port: port}, p,
		topology.AWGClient{PrivateKey: clientPriv, Address: netip.MustParseAddr("10.66.0.2")})
	if err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	if err := Probe(context.Background(), conf, Options{ControlURL: "http://10.66.0.1/generate_204", Timeout: 10 * time.Second}); err != nil {
		t.Fatalf("Probe: %v", err)
	}
	// Секунда и больше — потерян первый пакет и TCP повторил SYN
	// (flushStaleRead не сработал).
	if d := time.Since(start); d > 900*time.Millisecond {
		t.Fatalf("Probe took %s", d)
	}

	// Тот же сервер, но у клиента другие H1–H4: рукопожатия нет.
	q := p
	q.H1, q.H2, q.H3, q.H4 = p.H2, p.H3, p.H4, p.H1
	bad, _ := topology.BuildAWGConf(topology.Inbound{Host: "127.0.0.1", Port: port}, q,
		topology.AWGClient{PrivateKey: clientPriv, Address: netip.MustParseAddr("10.66.0.2")})
	if err := Probe(context.Background(), bad, Options{ControlURL: "http://10.66.0.1/", Timeout: time.Second}); !errors.Is(err, ErrHandshake) {
		t.Fatalf("mismatched headers: err = %v, want ErrHandshake", err)
	}
}

func TestProbeNon2xxIsError(t *testing.T) {
	p, _ := topology.NewAWGParams(0)
	serverPriv, serverPub, _ := topology.NewAWGKeyPair()
	clientPriv, clientPub, _ := topology.NewAWGKeyPair()
	p.ServerPublicKey = serverPub
	port := inProcessServer(t, p, serverPriv, clientPub)
	conf, _ := topology.BuildAWGConf(topology.Inbound{Host: "127.0.0.1", Port: port}, p,
		topology.AWGClient{PrivateKey: clientPriv, Address: netip.MustParseAddr("10.66.0.2")})
	// Рукопожатие есть, ответ 503 — ошибка, но не ErrHandshake.
	err := Probe(context.Background(), conf, Options{ControlURL: "http://10.66.0.1/fail", Timeout: 3 * time.Second})
	if err == nil || errors.Is(err, ErrHandshake) || !strings.Contains(err.Error(), "503") {
		t.Fatalf("err = %v", err)
	}
}

// Пустой DNS (у входа в параметрах нет dns — BuildAWGConf пишет «DNS = »)
// не валит разбор: внутри туннеля резолвит 1.1.1.1.
func TestConfEmptyDNSFallsBack(t *testing.T) {
	for _, line := range []string{"DNS = ", "DNS = ,", "DNS = , 9.9.9.9"} {
		conf, _, _, _ := testConf(t, "awg.example", 51820)
		conf = regexp.MustCompile(`(?m)^DNS = .*$`).ReplaceAllString(conf, line)
		c, err := parseConf(conf)
		if err != nil {
			t.Fatalf("%q: %v", line, err)
		}
		want := "1.1.1.1"
		if strings.Contains(line, "9.9.9.9") {
			want = "9.9.9.9"
		}
		if len(c.dns) != 1 || c.dns[0] != netip.MustParseAddr(want) {
			t.Fatalf("%q: dns = %v, want [%s]", line, c.dns, want)
		}
	}
}

func TestProbeEmptyDNSAgainstInProcessServer(t *testing.T) {
	p, _ := topology.NewAWGParams(0)
	p.DNS = ""
	serverPriv, serverPub, _ := topology.NewAWGKeyPair()
	clientPriv, clientPub, _ := topology.NewAWGKeyPair()
	p.ServerPublicKey = serverPub
	port := inProcessServer(t, p, serverPriv, clientPub)
	conf, err := topology.BuildAWGConf(topology.Inbound{Host: "127.0.0.1", Port: port}, p,
		topology.AWGClient{PrivateKey: clientPriv, Address: netip.MustParseAddr("10.66.0.2")})
	if err != nil {
		t.Fatal(err)
	}
	if !regexp.MustCompile(`(?m)^DNS =\s*$`).MatchString(conf) {
		t.Fatalf("в .conf нет пустого DNS:\n%s", conf)
	}
	if err := Probe(context.Background(), conf, Options{ControlURL: "http://10.66.0.1/generate_204", Timeout: 10 * time.Second}); err != nil {
		t.Fatalf("Probe: %v", err)
	}
}

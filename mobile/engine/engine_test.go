package engine

import (
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/syntr0x79/qntw-backend/internal/mobileprobe/wire"
	"github.com/syntr0x79/qntw-backend/internal/xrayfetch"
	xnet "github.com/xtls/xray-core/common/net"
	"github.com/xtls/xray-core/common/protocol"
	"github.com/xtls/xray-core/common/serial"
	"github.com/xtls/xray-core/core"
	"github.com/xtls/xray-core/proxy/vless/outbound"
	"google.golang.org/protobuf/proto"
)

type testNetwork struct {
	calls atomic.Int64
	err   error
}

func (n *testNetwork) PrepareSocket(fd int) error          { n.calls.Add(1); return n.err }
func (n *testNetwork) Resolve(host string) (string, error) { return "192.0.2.1", nil }

func TestResolveOuterServerPreservesProtocol(t *testing.T) {
	out := &core.OutboundHandlerConfig{ProxySettings: serial.ToTypedMessage(&outbound.Config{Vnext: &protocol.ServerEndpoint{Address: xnet.NewIPOrDomain(xnet.DomainAddress("mask.example")), Port: 443}})}
	if err := resolveOutbound(out, &testNetwork{}); err != nil {
		t.Fatal(err)
	}
	host, err := xrayfetch.ServerHost(out)
	if err != nil || host != "192.0.2.1" {
		t.Fatalf("host=%s err=%v", host, err)
	}
}
func TestSocketPreparationFailsClosed(t *testing.T) {
	c, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4zero})
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	raw, err := c.SyscallConn()
	if err != nil {
		t.Fatal(err)
	}
	sentinel := errors.New("network disappeared")
	network := &testNetwork{err: sentinel}
	if err = prepare(syscall.RawConn(raw), network); !errors.Is(err, sentinel) {
		t.Fatalf("error=%v", err)
	}
	if network.calls.Load() != 1 {
		t.Fatal("socket callback not called")
	}
}
func TestUnsupportedOutboundDoesNotAttemptDirectFetch(t *testing.T) {
	job, _ := json.Marshal(wire.Job{ID: "one", Engine: "xray", Outbound: []byte{0xff}})
	network := &testNetwork{}
	data, err := Probe(string(job), "https://control.invalid/", 100, network)
	if err != nil {
		t.Fatal(err)
	}
	var result wire.Result
	if err = json.Unmarshal([]byte(data), &result); err != nil {
		t.Fatal(err)
	}
	if result.Outcome != "unprobeable" || network.calls.Load() != 0 {
		t.Fatalf("result=%+v sockets=%d", result, network.calls.Load())
	}
}
func TestRejectsUnsafeControlURL(t *testing.T) {
	if _, err := Probe(`{"job_id":"one","engine":"amneziawg"}`, "http://example.com", 100, &testNetwork{}); err == nil {
		t.Fatal("accepted cleartext URL")
	}
}

func TestProbeTraversesRealVLESSOutbound(t *testing.T) {
	control := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(204) }))
	defer control.Close()
	ca := filepath.Join(t.TempDir(), "ca.pem")
	if err := os.WriteFile(ca, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: control.Certificate().Raw}), 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("SSL_CERT_FILE", ca)
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := listener.Addr().(*net.TCPAddr).Port
	listener.Close()
	id := "11111111-2222-4333-8444-555555555555"
	serverJSON := fmt.Sprintf(`{"log":{"loglevel":"none"},"inbounds":[{"listen":"127.0.0.1","port":%d,"protocol":"vless","settings":{"clients":[{"id":%q}],"decryption":"none"}}],"outbounds":[{"protocol":"freedom","settings":{"finalRules":[{"action":"allow","ip":["127.0.0.0/8"]}]}}]}`, port, id)
	cfg, err := core.LoadConfig("json", strings.NewReader(serverJSON))
	if err != nil {
		t.Fatal(err)
	}
	server, err := core.New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if err = server.Start(); err != nil {
		t.Fatal(err)
	}
	defer server.Close()
	clientJSON := fmt.Sprintf(`{"outbounds":[{"protocol":"vless","settings":{"vnext":[{"address":"127.0.0.1","port":%d,"users":[{"id":%q,"encryption":"none"}]}]}}]}`, port, id)
	client, err := core.LoadConfig("json", strings.NewReader(clientJSON))
	if err != nil {
		t.Fatal(err)
	}
	payload, err := proto.Marshal(client.Outbound[0])
	if err != nil {
		t.Fatal(err)
	}
	job, _ := json.Marshal(wire.Job{ID: "live-vless", Engine: "xray", Outbound: payload})
	network := &testNetwork{}
	data, err := Probe(string(job), control.URL, 3000, network)
	if err != nil {
		t.Fatal(err)
	}
	var result wire.Result
	if err = json.Unmarshal([]byte(data), &result); err != nil {
		t.Fatal(err)
	}
	if result.Outcome != "ok" || network.calls.Load() == 0 {
		t.Fatalf("result=%+v sockets=%d", result, network.calls.Load())
	}
}

type blockedDNS struct {
	entered chan struct{}
	release chan struct{}
}

func (n *blockedDNS) PrepareSocket(fd int) error { return nil }
func (n *blockedDNS) Resolve(host string) (string, error) {
	close(n.entered)
	<-n.release
	return "192.0.2.1", nil
}

func TestCancelInterruptsPlatformDNSWithoutVotingFailure(t *testing.T) {
	n := &blockedDNS{entered: make(chan struct{}), release: make(chan struct{})}
	defer close(n.release)
	out := &core.OutboundHandlerConfig{ProxySettings: serial.ToTypedMessage(&outbound.Config{Vnext: &protocol.ServerEndpoint{Address: xnet.NewIPOrDomain(xnet.DomainAddress("mask.example")), Port: 443}})}
	payload, err := proto.Marshal(out)
	if err != nil {
		t.Fatal(err)
	}
	job, _ := json.Marshal(wire.Job{ID: "cancel", Engine: "xray", Outbound: payload})
	done := make(chan string, 1)
	go func() {
		data, e := Probe(string(job), "https://control.invalid", 30000, n)
		if e != nil {
			done <- "error"
			return
		}
		done <- data
	}()
	select {
	case <-n.entered:
	case <-time.After(time.Second):
		t.Fatal("DNS did not start")
	}
	Cancel()
	select {
	case data := <-done:
		var result wire.Result
		if err = json.Unmarshal([]byte(data), &result); err != nil {
			t.Fatal(err)
		}
		if result.Outcome != "cancelled" {
			t.Fatalf("outcome=%s", result.Outcome)
		}
	case <-time.After(time.Second):
		t.Fatal("cancel did not interrupt DNS")
	}
}

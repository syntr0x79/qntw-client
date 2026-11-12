//go:build darwin && !ios

package main

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/syntr0x79/qntw-client/internal/awgprobe"
	"github.com/syntr0x79/qntw-client/internal/mobileprobe/wire"
	"github.com/syntr0x79/qntw-client/internal/xrayfetch"
	macnet "github.com/syntr0x79/qntw-client/mobile/macos/network"
	"github.com/xtls/xray-core/app/proxyman"
	xnet "github.com/xtls/xray-core/common/net"
	"github.com/xtls/xray-core/common/protocol"
	"github.com/xtls/xray-core/common/serial"
	"github.com/xtls/xray-core/core"
	"github.com/xtls/xray-core/proxy/vless/outbound"
	"github.com/xtls/xray-core/transport/internet"
	"github.com/xtls/xray-core/transport/internet/splithttp"
	xtls "github.com/xtls/xray-core/transport/internet/tls"
	"google.golang.org/protobuf/proto"
)

type ordinaryTestNetwork struct {
	calls   []string
	address string
	err     error
}

func (*ordinaryTestNetwork) PrepareSocket(int) error { return nil }
func (n *ordinaryTestNetwork) Resolve(host string) (string, error) {
	n.calls = append(n.calls, host)
	return n.address, n.err
}

func ordinaryAWGJob(endpoint string) wire.Job {
	return wire.Job{ID: "test-awg", Engine: "amneziawg", AWGConf: `[Interface]
PrivateKey = AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA=
Address = 10.66.0.7/32
DNS = 1.1.1.1
MTU = 1280
S1 = 64
S4 = 32
HeaderProtectionKey = AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA=

[Peer]
PublicKey = AQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQE=
Endpoint = ` + endpoint + `
AllowedIPs = 0.0.0.0/0
PersistentKeepalive = 25
`}
}

func ordinaryXrayJob(t *testing.T, host, downloadHost string) wire.Job {
	t.Helper()
	transport := &splithttp.Config{Host: "http-front.example", Path: "/private-path", Mode: "stream-up"}
	if downloadHost != "" {
		transport.DownloadSettings = &internet.StreamConfig{
			ProtocolName: "splithttp",
			Address:      xnet.NewIPOrDomain(xnet.ParseAddress(downloadHost)), Port: 8443,
			SecurityType:      serial.GetMessageType(&xtls.Config{}),
			SecuritySettings:  []*serial.TypedMessage{serial.ToTypedMessage(&xtls.Config{ServerName: "download-front.example"})},
			TransportSettings: []*internet.TransportConfig{{ProtocolName: "splithttp", Settings: serial.ToTypedMessage(&splithttp.Config{Host: "download-http.example"})}},
		}
	}
	out := &core.OutboundHandlerConfig{
		ProxySettings: serial.ToTypedMessage(&outbound.Config{Vnext: &protocol.ServerEndpoint{Address: xnet.NewIPOrDomain(xnet.ParseAddress(host)), Port: 443}}),
		SenderSettings: serial.ToTypedMessage(&proxyman.SenderConfig{StreamSettings: &internet.StreamConfig{
			ProtocolName:      "splithttp",
			SecurityType:      serial.GetMessageType(&xtls.Config{}),
			SecuritySettings:  []*serial.TypedMessage{serial.ToTypedMessage(&xtls.Config{ServerName: "tls-front.example"})},
			TransportSettings: []*internet.TransportConfig{{ProtocolName: "splithttp", Settings: serial.ToTypedMessage(transport)}},
		}}),
	}
	payload, err := proto.Marshal(out)
	if err != nil {
		t.Fatal(err)
	}
	return wire.Job{ID: "test-xray", Engine: "xray", Outbound: payload}
}

func TestPrepareOrdinaryAWGPinsResolvedAddressAndPreservesConfig(t *testing.T) {
	job := ordinaryAWGJob("server.example:51821")
	network := &ordinaryTestNetwork{address: "192.0.2.12"}
	var allowed []string
	prepared, err := prepareOrdinaryRoute(context.Background(), job, network, func(_ context.Context, addresses []string) error {
		allowed = append(allowed, addresses...)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(network.calls, []string{"server.example"}) || !reflect.DeepEqual(allowed, []string{"192.0.2.12"}) {
		t.Fatalf("resolve=%v allowed=%v", network.calls, allowed)
	}
	expected := strings.Replace(job.AWGConf, "server.example:51821", "192.0.2.12:51821", 1)
	if prepared.AWGConf != expected || prepared.ID != job.ID || awgprobe.ValidateConfig(prepared.AWGConf) != nil {
		t.Fatal("preparation changed AWG fields beyond the endpoint")
	}
	if !strings.Contains(job.AWGConf, "server.example:51821") {
		t.Fatal("input job changed")
	}
}

func TestPrepareOrdinaryLiteralEndpointDoesNotResolve(t *testing.T) {
	for _, job := range []wire.Job{ordinaryAWGJob("192.0.2.12:51821"), ordinaryXrayJob(t, "192.0.2.12", "")} {
		network := &ordinaryTestNetwork{err: errors.New("DNS must not run")}
		_, err := prepareOrdinaryRoute(context.Background(), job, network, func(_ context.Context, addresses []string) error {
			if !reflect.DeepEqual(addresses, []string{"192.0.2.12"}) {
				t.Errorf("unexpected physical addresses: %v", addresses)
			}
			return nil
		})
		if err != nil || len(network.calls) != 0 {
			t.Fatalf("literal endpoint resolved: calls=%v err=%v", network.calls, err)
		}
	}
}

func TestPrepareOrdinaryXrayPreservesNamesAndPinsDownload(t *testing.T) {
	for _, downloadHost := range []string{"203.0.113.24", "server.example"} {
		t.Run(downloadHost, func(t *testing.T) {
			job := ordinaryXrayJob(t, "server.example", downloadHost)
			network := &ordinaryTestNetwork{address: "192.0.2.12"}
			var allowed []string
			prepared, err := prepareOrdinaryRoute(context.Background(), job, network, func(_ context.Context, addresses []string) error {
				allowed = append(allowed, addresses...)
				return nil
			})
			if err != nil {
				t.Fatal(err)
			}
			expected := []string{"192.0.2.12"}
			if downloadHost != "server.example" {
				expected = append(expected, downloadHost)
			}
			if !reflect.DeepEqual(network.calls, []string{"server.example"}) || !reflect.DeepEqual(allowed, expected) {
				t.Fatalf("resolve=%v allowed=%v want=%v", network.calls, allowed, expected)
			}
			var out core.OutboundHandlerConfig
			if err := proto.Unmarshal(prepared.Outbound, &out); err != nil {
				t.Fatal(err)
			}
			if host, err := xrayfetch.ServerHost(&out); err != nil || host != "192.0.2.12" {
				t.Fatalf("unprotected engine address: %s err=%v", host, err)
			}
			msg, _ := out.SenderSettings.GetInstance()
			stream := msg.(*proxyman.SenderConfig).StreamSettings
			tls, _ := stream.SecuritySettings[0].GetInstance()
			transport, _ := stream.TransportSettings[0].Settings.GetInstance()
			xhttp := transport.(*splithttp.Config)
			if tls.(*xtls.Config).ServerName != "tls-front.example" || xhttp.Host != "http-front.example" || xhttp.Path != "/private-path" {
				t.Fatal("primary TLS or HTTP name changed")
			}
			download := xhttp.DownloadSettings
			tls, _ = download.SecuritySettings[0].GetInstance()
			transport, _ = download.TransportSettings[0].Settings.GetInstance()
			if tls.(*xtls.Config).ServerName != "download-front.example" || transport.(*splithttp.Config).Host != "download-http.example" || download.Port != 8443 || download.Address.AsAddress().String() != expected[len(expected)-1] {
				t.Fatal("download TLS/HTTP names or pinned endpoint changed")
			}
		})
	}
}

func TestPrepareOrdinaryRejectsUnsupportedEndpointsBeforeChangingRoutes(t *testing.T) {
	duplicate := ordinaryAWGJob("server.example:51821")
	duplicate.AWGConf += "Endpoint = second.example:51821\n"
	jobs := []wire.Job{
		ordinaryAWGJob("[2001:db8::1]:51821"),
		ordinaryAWGJob("server.example:0"),
		ordinaryAWGJob("server.example:99999"),
		duplicate,
		ordinaryXrayJob(t, "2001:db8::1", ""),
		ordinaryXrayJob(t, "server.example", "2001:db8::2"),
		ordinaryXrayJob(t, "server.example", "different.example"),
		{Engine: "unsupported"},
	}
	for i, job := range jobs {
		network := &ordinaryTestNetwork{address: "192.0.2.12"}
		_, err := prepareOrdinaryRoute(context.Background(), job, network, func(context.Context, []string) error {
			t.Errorf("invalid profile %d reached route mutation", i)
			return nil
		})
		if err == nil {
			t.Errorf("unsupported endpoint %d accepted", i)
		}
	}
	_, err := prepareOrdinaryRoute(context.Background(), ordinaryAWGJob("server.example:51821"), &ordinaryTestNetwork{address: "2001:db8::1"}, func(context.Context, []string) error {
		t.Error("resolved IPv6 reached route mutation")
		return nil
	})
	if err == nil {
		t.Fatal("resolved IPv6 accepted")
	}
}

func TestPrepareOrdinaryFailuresReturnNoProfileOrSensitiveErrors(t *testing.T) {
	for _, dnsFails := range []bool{false, true} {
		network := &ordinaryTestNetwork{address: "192.0.2.12"}
		if dnsFails {
			network.err = errors.New("private DNS name and secret credentials")
		}
		prepared, err := prepareOrdinaryRoute(context.Background(), ordinaryAWGJob("private.example:51821"), network, func(context.Context, []string) error {
			if dnsFails {
				t.Error("route mutation after DNS failure")
			}
			return errors.New("private route address and credentials")
		})
		var routeError macnet.ConnectionError
		if (dnsFails && err != errOrdinaryRoute) || (!dnsFails && (!errors.As(err, &routeError) || routeError.Code != "physical_route_failed" || routeError.Diagnostics != nil)) || prepared.Engine != "" || prepared.AWGConf != "" || prepared.Outbound != nil {
			t.Fatal("preparation failure returned a profile or unbounded error")
		}
	}
}

func TestPrepareOrdinaryDoesNotProtectUnusedDownloadSettings(t *testing.T) {
	job := ordinaryXrayJob(t, "192.0.2.12", "203.0.113.24")
	var out core.OutboundHandlerConfig
	if err := proto.Unmarshal(job.Outbound, &out); err != nil {
		t.Fatal(err)
	}
	settings, _ := out.SenderSettings.GetInstance()
	sender := settings.(*proxyman.SenderConfig)
	sender.StreamSettings.ProtocolName = "tcp"
	out.SenderSettings = serial.ToTypedMessage(sender)
	job.Outbound, _ = proto.Marshal(&out)
	_, err := prepareOrdinaryRoute(context.Background(), job, &ordinaryTestNetwork{}, func(_ context.Context, addresses []string) error {
		if !reflect.DeepEqual(addresses, []string{"192.0.2.12"}) {
			t.Errorf("unused XHTTP download got a physical route: %v", addresses)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestPrepareOrdinaryRejectsIncompleteDownloadBeforeChangingRoutes(t *testing.T) {
	for name, change := range map[string]func(*splithttp.Config){
		"missing address": func(c *splithttp.Config) { c.DownloadSettings.Address = nil },
		"empty address":   func(c *splithttp.Config) { c.DownloadSettings.Address = &xnet.IPOrDomain{} },
		"missing port":    func(c *splithttp.Config) { c.DownloadSettings.Port = 0 },
		"stream one":      func(c *splithttp.Config) { c.Mode = "stream-one" },
	} {
		t.Run(name, func(t *testing.T) {
			job := ordinaryXrayJob(t, "192.0.2.12", "203.0.113.24")
			var out core.OutboundHandlerConfig
			if err := proto.Unmarshal(job.Outbound, &out); err != nil {
				t.Fatal(err)
			}
			settings, _ := out.SenderSettings.GetInstance()
			sender := settings.(*proxyman.SenderConfig)
			transport := sender.StreamSettings.TransportSettings[0]
			settings, _ = transport.Settings.GetInstance()
			xhttp := settings.(*splithttp.Config)
			change(xhttp)
			transport.Settings = serial.ToTypedMessage(xhttp)
			out.SenderSettings = serial.ToTypedMessage(sender)
			job.Outbound, _ = proto.Marshal(&out)
			_, err := prepareOrdinaryRoute(context.Background(), job, &ordinaryTestNetwork{}, func(context.Context, []string) error {
				t.Error("invalid download reached route mutation")
				return nil
			})
			if err == nil {
				t.Fatal("invalid download accepted")
			}
		})
	}
}

type blockedOrdinaryNetwork struct {
	entered chan struct{}
	release chan struct{}
}

func (*blockedOrdinaryNetwork) PrepareSocket(int) error { return nil }
func (n *blockedOrdinaryNetwork) Resolve(string) (string, error) {
	close(n.entered)
	<-n.release
	return "192.0.2.12", nil
}

func TestPrepareOrdinaryCancellationStopsBeforeRouteMutation(t *testing.T) {
	network := &blockedOrdinaryNetwork{entered: make(chan struct{}), release: make(chan struct{})}
	defer close(network.release)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() {
		_, err := prepareOrdinaryRoute(ctx, ordinaryAWGJob("server.example:51821"), network, func(context.Context, []string) error {
			t.Error("cancelled preparation reached route mutation")
			return nil
		})
		done <- err
	}()
	<-network.entered
	cancel()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("cancelled preparation succeeded")
		}
	case <-time.After(time.Second):
		t.Fatal("cancellation waited for platform DNS")
	}
}

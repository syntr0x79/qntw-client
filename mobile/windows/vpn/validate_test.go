package vpn

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/syntr0x79/qntw-client/internal/external"
	"github.com/syntr0x79/qntw-client/internal/mobileprobe/wire"
	"github.com/xtls/xray-core/app/proxyman"
	"github.com/xtls/xray-core/common/serial"
	"github.com/xtls/xray-core/core"
	"github.com/xtls/xray-core/transport/internet"
	"github.com/xtls/xray-core/transport/internet/reality"
	"github.com/xtls/xray-core/transport/internet/splithttp"
	xtls "github.com/xtls/xray-core/transport/internet/tls"
	"google.golang.org/protobuf/proto"
)

const fixtureID = "11111111-2222-3333-4444-555555555555"

func fixtureOutbound(t *testing.T, query string) *core.OutboundHandlerConfig {
	t.Helper()
	out, err := external.Outbound("vless://" + fixtureID + "@a.example:443?" + query)
	if err != nil {
		t.Fatal(err)
	}
	return out
}
func jobFor(t *testing.T, out *core.OutboundHandlerConfig) json.RawMessage {
	t.Helper()
	encoded, err := proto.Marshal(out)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(wire.Job{ID: "windows-fixture", Engine: "xray", Outbound: encoded})
	if err != nil {
		t.Fatal(err)
	}
	return raw
}
func senderFor(t *testing.T, out *core.OutboundHandlerConfig) *proxyman.SenderConfig {
	t.Helper()
	value, err := out.SenderSettings.GetInstance()
	if err != nil {
		t.Fatal(err)
	}
	return value.(*proxyman.SenderConfig)
}
func TestSupportedImportedProfilesRemainUsable(t *testing.T) {
	links := []string{
		"vless://" + fixtureID + "@a.example:443?type=tcp&security=reality&pbk=AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA&sid=ab&sni=x.example&fp=chrome&flow=xtls-rprx-vision",
		"hy2://pass@a.example:443?sni=a.example",
		"vless://" + fixtureID + "@a.example:443?type=xhttp&security=tls&sni=a.example&mode=packet-up&path=%2Fp&host=h.example",
		"vless://" + fixtureID + "@a.example:443?type=ws&security=tls&sni=a.example&path=%2Fw&host=h.example",
		"vless://" + fixtureID + "@a.example:443?type=grpc&security=tls&sni=a.example&serviceName=svc",
	}
	for i, link := range links {
		out, err := external.Outbound(link)
		if err != nil {
			t.Fatal(err)
		}
		if err = ValidateJob(jobFor(t, out)); err != nil {
			t.Fatalf("profile %d rejected: %v", i, err)
		}
	}
}
func TestPrivilegedTLSFilesAndKeyLogsAreRejected(t *testing.T) {
	for name, security := range map[string]*xtls.Config{
		"certificate":  {Certificate: []*xtls.Certificate{{CertificatePath: `C:\Windows\System32\config\SAM`}}},
		"key":          {Certificate: []*xtls.Certificate{{KeyPath: `C:\private.key`}}},
		"write-keylog": {MasterKeyLog: `C:\Windows\Temp\quantway-keylog`},
		"external-ech": {EchConfigList: "example.invalid+https://example.invalid/dns-query"},
	} {
		t.Run(name, func(t *testing.T) {
			out := fixtureOutbound(t, "type=tcp&security=tls&sni=a.example")
			sender := senderFor(t, out)
			sender.StreamSettings.SecuritySettings = []*serial.TypedMessage{serial.ToTypedMessage(security)}
			out.SenderSettings = serial.ToTypedMessage(sender)
			if ValidateJob(jobFor(t, out)) == nil {
				t.Fatal("privileged file/network option accepted")
			}
		})
	}
}
func TestNestedDownloadTransportCannotSmuggleCertificateFile(t *testing.T) {
	out := fixtureOutbound(t, "type=xhttp&security=tls&sni=a.example&path=%2Fp")
	sender := senderFor(t, out)
	settings, err := sender.StreamSettings.TransportSettings[0].Settings.GetInstance()
	if err != nil {
		t.Fatal(err)
	}
	split := settings.(*splithttp.Config)
	split.DownloadSettings = &internet.StreamConfig{SecurityType: "xray.transport.internet.tls.Config", SecuritySettings: []*serial.TypedMessage{serial.ToTypedMessage(&xtls.Config{Certificate: []*xtls.Certificate{{KeyPath: "/root/secret"}}})}}
	sender.StreamSettings.TransportSettings[0].Settings = serial.ToTypedMessage(split)
	out.SenderSettings = serial.ToTypedMessage(sender)
	if ValidateJob(jobFor(t, out)) == nil {
		t.Fatal("nested path accepted")
	}
}
func TestUnknownTypedDataUnixTransportAndUnsafeSocketOptionsAreRejected(t *testing.T) {
	for name, change := range map[string]func(*core.OutboundHandlerConfig, *proxyman.SenderConfig){
		"unknown-type": func(out *core.OutboundHandlerConfig, _ *proxyman.SenderConfig) {
			out.ProxySettings = &serial.TypedMessage{Type: "xray.proxy.freedom.Config"}
		},
		"unix-transport": func(_ *core.OutboundHandlerConfig, s *proxyman.SenderConfig) {
			s.StreamSettings.ProtocolName = "domainsocket"
		},
		"custom-socket": func(_ *core.OutboundHandlerConfig, s *proxyman.SenderConfig) {
			s.StreamSettings.SocketSettings = &internet.SocketConfig{CustomSockopt: []*internet.CustomSockopt{{Level: "1", Opt: "1", Value: "1"}}}
		},
		"forced-interface": func(_ *core.OutboundHandlerConfig, s *proxyman.SenderConfig) {
			s.StreamSettings.SocketSettings = &internet.SocketConfig{Interface: "untrusted"}
		},
		"dialer-proxy": func(_ *core.OutboundHandlerConfig, s *proxyman.SenderConfig) {
			s.StreamSettings.SocketSettings = &internet.SocketConfig{DialerProxy: "direct"}
		},
		"reality-keylog": func(_ *core.OutboundHandlerConfig, s *proxyman.SenderConfig) {
			s.StreamSettings.SecurityType = "xray.transport.internet.reality.Config"
			s.StreamSettings.SecuritySettings = []*serial.TypedMessage{serial.ToTypedMessage(&reality.Config{MasterKeyLog: "C:/privileged-output"})}
		},
	} {
		t.Run(name, func(t *testing.T) {
			out := fixtureOutbound(t, "type=tcp&security=tls&sni=a.example")
			sender := senderFor(t, out)
			change(out, sender)
			out.SenderSettings = serial.ToTypedMessage(sender)
			if ValidateJob(jobFor(t, out)) == nil {
				t.Fatal("unsafe profile accepted")
			}
		})
	}
	out := fixtureOutbound(t, "type=tcp&security=tls&sni=a.example")
	out.ProtoReflect().SetUnknown([]byte{0xF8, 0x07, 0x01})
	if ValidateJob(jobFor(t, out)) == nil {
		t.Fatal("unknown protobuf fields accepted")
	}
}
func TestAWGIsStrictUserspaceConfigWithoutScriptHooks(t *testing.T) {
	conf := "[Interface]\nPrivateKey = AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA=\nAddress = 10.66.0.2/32\nDNS = 1.1.1.1\n[Peer]\nPublicKey = AQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQE=\nEndpoint = vpn.example:443\nAllowedIPs = 0.0.0.0/0\n"
	check := func(conf string) error {
		raw, _ := json.Marshal(wire.Job{ID: "awg-fixture", Engine: "amneziawg", AWGConf: conf})
		return ValidateJob(raw)
	}
	if err := check(conf); err != nil {
		t.Fatal(err)
	}
	for _, hook := range []string{"PreUp", "PostUp", "PreDown", "PostDown", "SaveConfig", "Table"} {
		if check(strings.Replace(conf, "[Peer]", hook+" = malicious.exe\n[Peer]", 1)) == nil {
			t.Fatalf("%s accepted", hook)
		}
	}
}
func TestRequestBoundsAndHTTPSControlURL(t *testing.T) {
	for _, raw := range []json.RawMessage{nil, []byte(`{}`), []byte(`{"engine":"invalid","job_id":"x"}`), []byte(strings.Repeat(" ", 256<<10+1))} {
		if ValidateJob(raw) == nil {
			t.Fatal("invalid job accepted")
		}
	}
	for _, url := range []string{"http://example.invalid/", "https://u:p@example.invalid/", "file:///C:/Windows", "https://example.invalid/#fragment", "https://[::1]/", "https://bad_host/"} {
		if ValidateControlURL(url) == nil {
			t.Fatalf("unsafe URL accepted: %s", url)
		}
	}
	if err := ValidateControlURL("https://example.invalid/control?v=1"); err != nil {
		t.Fatal(err)
	}
}
func TestSystemDocumentOwnsOnlyFixedTunAndBlocksIPv6(t *testing.T) {
	var document map[string]any
	if err := json.Unmarshal(tunnelDocument(), &document); err != nil {
		t.Fatal(err)
	}
	inbound := document["inbounds"].([]any)[0].(map[string]any)
	settings := inbound["settings"].(map[string]any)
	if inbound["protocol"] != "tun" || settings["name"] != "qtc0" || settings["mtu"] != float64(1280) {
		t.Fatal("unexpected privileged TUN")
	}
	if len(settings["autoSystemRoutingTable"].([]any)) != 0 {
		t.Fatal("core must not own route installation")
	}
	if _, exists := settings["autoOutboundsInterface"]; exists {
		t.Fatal("global auto binding must stay disabled")
	}
	rules := document["routing"].(map[string]any)["rules"].([]any)
	if rules[0].(map[string]any)["outboundTag"] != "block" {
		t.Fatal("IPv6 must fail closed")
	}
}

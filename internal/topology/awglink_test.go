package topology

import (
	"fmt"
	"net/netip"
	"regexp"
	"strings"
	"testing"
)

// awgHasParam ищет строку параметра, а не подстроку: в base64 ключа
// «I2» встречается случайно (PrivateKey = KD7j+NI2Xau…).
func awgHasParam(conf, name string) bool {
	return regexp.MustCompile(`(?m)^` + regexp.QuoteMeta(name) + `\s*=`).MatchString(conf)
}

func awgInbound(t *testing.T) (Inbound, AWGParams) {
	t.Helper()
	p, _ := NewAWGParams(51821)
	_, p.ServerPublicKey, _ = NewAWGKeyPair()
	return Inbound{Tag: "awg-in", Protocol: ProtocolAWG, Host: "us.example.com", Port: 51821, Transport: p.Transport(), Enabled: true}, p
}

func TestBuildAWGLinkRoundTrips(t *testing.T) {
	in, p := awgInbound(t)
	priv, _, _ := NewAWGKeyPair()
	c := AWGClient{PrivateKey: priv, Address: netip.MustParseAddr("10.66.0.7")}
	link, err := BuildAWGLink(in, c, "Авто - Amnezia")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(link, "awg://") {
		t.Fatalf("link %q", link)
	}
	conf, label, err := ParseAWGLink(link)
	if err != nil {
		t.Fatal(err)
	}
	if label != "Авто - Amnezia" {
		t.Fatalf("label %q", label)
	}
	for _, want := range []string{
		"PrivateKey = " + priv, "Address = 10.66.0.7/32", "DNS = 1.1.1.1", "MTU = 1280",
		"Jc = 3", "Jmin = 40", "Jmax = 70", "I1 = <r 48>",
		fmt.Sprintf("S1 = %d", p.S1), fmt.Sprintf("S4 = %d", p.S4),
		"H1 = " + p.H1, "H4 = " + p.H4, "HeaderProtectionKey = " + p.HeaderProtectionKey,
		"PublicKey = " + p.ServerPublicKey, "Endpoint = us.example.com:51821",
		"AllowedIPs = 0.0.0.0/0", "PersistentKeepalive = 25",
	} {
		if !strings.Contains(conf, want+"\n") {
			t.Errorf("conf lacks %q:\n%s", want, conf)
		}
	}
	if strings.Contains(conf, "::/0") {
		t.Error("IPv6 in AllowedIPs: inbounds do not serve v6")
	}
}

func TestBuildAWGLinkRefusesMissingServerKey(t *testing.T) {
	in, p := awgInbound(t)
	p.ServerPublicKey = ""
	in.Transport = p.Transport()
	priv, _, _ := NewAWGKeyPair()
	if _, err := BuildAWGLink(in, AWGClient{PrivateKey: priv, Address: netip.MustParseAddr("10.66.0.2")}, "x"); err == nil {
		t.Fatal("link built before the node reported its key — it would never connect")
	}
}

func TestBuildAWGLinkIsBase64URL(t *testing.T) {
	in, _ := awgInbound(t)
	priv, _, _ := NewAWGKeyPair()
	link, _ := BuildAWGLink(in, AWGClient{PrivateKey: priv, Address: netip.MustParseAddr("10.66.0.2")}, "x")
	body := strings.TrimPrefix(strings.SplitN(link, "#", 2)[0], "awg://")
	if strings.ContainsAny(body, "+/=") {
		t.Fatalf("not base64url without padding: %q", body)
	}
}

// I2…I5 приходят из документа протокола в Params входа (i2…i5) и едут в
// конфиг клиента сразу после I1. Нет их — конфиг и ссылка прежние байт в
// байт: у входов, заведённых до каталога, ничего не меняется.
func TestAWGConfCarriesI2ToI5OnlyWhenSet(t *testing.T) {
	in, p := awgInbound(t)
	priv, _, _ := NewAWGKeyPair()
	c := AWGClient{PrivateKey: priv, Address: netip.MustParseAddr("10.66.0.7")}

	before, err := BuildAWGConf(in, p, c)
	if err != nil {
		t.Fatal(err)
	}
	beforeLink, err := BuildAWGLink(in, c, "US")
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"I2", "I3", "I4", "I5"} {
		if awgHasParam(before, name) {
			t.Fatalf("%s без значения:\n%s", name, before)
		}
	}
	if _, has := in.Transport.Params["i2"]; has {
		t.Fatal("Transport() пишет пустой i2")
	}

	// Без i2…i5 разбор и сборка возвращают тот же текст.
	parsed, err := ParseAWGParams(in.Transport)
	if err != nil {
		t.Fatal(err)
	}
	if again, _ := BuildAWGConf(in, parsed, c); again != before {
		t.Fatalf("конфиг изменился:\n%s\n---\n%s", again, before)
	}

	in.Transport.Params["i2"] = "<b 0x0102>"
	in.Transport.Params["i4"] = "<r 16>"
	parsed, err = ParseAWGParams(in.Transport)
	if err != nil {
		t.Fatal(err)
	}
	if parsed.I2 != "<b 0x0102>" || parsed.I3 != "" || parsed.I4 != "<r 16>" {
		t.Fatalf("разбор: %+v", parsed)
	}
	if rt := parsed.Transport().Params; rt["i2"] != "<b 0x0102>" || rt["i4"] != "<r 16>" || rt["i3"] != nil {
		t.Fatalf("Transport(): %v", rt)
	}
	after, err := BuildAWGConf(in, parsed, c)
	if err != nil {
		t.Fatal(err)
	}
	for name, set := range map[string]bool{"I2": true, "I3": false, "I4": true, "I5": false} {
		if awgHasParam(after, name) != set {
			t.Fatalf("%s: строка есть = %v, ждали %v:\n%s", name, !set, set, after)
		}
	}
	want := strings.Replace(before, "I1 = <r 48>\n", "I1 = <r 48>\nI2 = <b 0x0102>\nI4 = <r 16>\n", 1)
	if after != want {
		t.Fatalf("конфиг:\n%s\nждали\n%s", after, want)
	}
	link, err := BuildAWGLink(in, c, "US")
	if err != nil {
		t.Fatal(err)
	}
	conf, _, err := ParseAWGLink(link)
	if err != nil {
		t.Fatal(err)
	}
	if conf != want || link == beforeLink {
		t.Fatalf("ссылка несёт:\n%s", conf)
	}
}

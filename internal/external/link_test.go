package external

import (
	"errors"
	"net/url"
	"strings"
	"testing"
)

func TestParseLink(t *testing.T) {
	const id = "11111111-2222-3333-4444-555555555555"
	ok := []struct {
		name, raw string
		want      Parsed
	}{
		{"vless reality", "vless://" + id + "@a.example:443?type=tcp&security=reality&pbk=AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA&sid=ab&sni=x&fp=chrome&flow=xtls-rprx-vision#Имя",
			Parsed{ProtoVLESS, "a.example", 443, "vless://" + id + "@a.example:443?type=tcp&security=reality&pbk=AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA&sid=ab&sni=x&fp=chrome&flow=xtls-rprx-vision", "Имя"}},
		{"hysteria2", "hysteria2://pass@b.example:443?sni=b#n",
			Parsed{ProtoHy2, "b.example", 443, "hy2://pass@b.example:443?sni=b", "n"}},
		{"hy2 без имени", "hy2://pass@b.example:8443?sni=b",
			Parsed{ProtoHy2, "b.example", 8443, "hy2://pass@b.example:8443?sni=b", ""}},
		{"vless xhttp", "vless://" + id + "@h.example:443?type=xhttp&security=tls&mode=packet-up&path=%2Fp#x",
			Parsed{ProtoVLESS, "h.example", 443, "vless://" + id + "@h.example:443?type=xhttp&security=tls&mode=packet-up&path=%2Fp", "x"}},
	}
	for _, c := range ok {
		t.Run(c.name, func(t *testing.T) {
			got, err := ParseLink(c.raw)
			if err != nil {
				t.Fatal(err)
			}
			if got != c.want {
				t.Fatalf("got %+v, want %+v", got, c.want)
			}
		})
	}

	bad := []struct{ name, raw, reason string }{
		{"trojan", "trojan://p@a.example:443", "trojan"},
		{"vless insecure", "vless://" + id + "@a.example:443?allowInsecure=1", "insecure"},
		{"hy2 insecure", "hy2://p@a.example:443?insecure=1", "insecure"},
		{"hy2 obfs", "hy2://p@a.example:443?obfs=salamander", "obfs"},
		{"kcp", "vless://" + id + "@a.example:443?type=kcp", "transport"},
		{"headerType", "vless://" + id + "@a.example:443?type=tcp&headerType=http", "headerType"},
		{"stub", "vless://u@0.0.0.0:1", "stub"},
		{"security", "vless://" + id + "@a.example:443?security=xtls", "security"},
		{"stub port", "vless://" + id + "@a.example:1", "stub"},
	}
	for _, c := range bad {
		t.Run(c.name, func(t *testing.T) {
			_, err := ParseLink(c.raw)
			if !errors.Is(err, ErrUnsupported) {
				t.Fatalf("err = %v, want ErrUnsupported", err)
			}
			if got := Reason(err); got != c.reason {
				t.Fatalf("Reason = %q, want %q (%v)", got, c.reason, err)
			}
		})
	}

	for name, raw := range map[string]string{
		"плохой uuid":     "vless://garbage@h.example:443",
		"reality без pbk": "vless://" + id + "@h.example:443?security=reality&sni=x",
		"reality pbk":     "vless://" + id + "@h.example:443?security=reality&pbk=K&sni=x",
	} {
		if _, err := ParseLink(raw); err == nil || errors.Is(err, ErrUnsupported) {
			t.Fatalf("%s: err = %v, want обычная ошибка", name, err)
		}
	}
	if p, err := ParseLink("VLESS://" + id + "@a.example:443"); err != nil || !strings.HasPrefix(p.Link, "vless://") {
		t.Fatalf("схема не в нижнем регистре: %+v %v", p, err)
	}

	_, err := ParseLink("это не ссылка")
	if err == nil || errors.Is(err, ErrUnsupported) {
		t.Fatalf("мусор: err = %v, want обычная ошибка", err)
	}
}

func TestNamed(t *testing.T) {
	got := Named("vless://u@a.example:443?type=tcp#Чужое", "Запасной - xray")
	if got != "vless://u@a.example:443?type=tcp#%D0%97%D0%B0%D0%BF%D0%B0%D1%81%D0%BD%D0%BE%D0%B9%20-%20xray" {
		t.Fatalf("got %q", got)
	}
	got = Named("hy2://p@a.example:443?sni=a", "x y")
	if got != "hy2://p@a.example:443?sni=a#x%20y" {
		t.Fatalf("got %q", got)
	}
	if strings.Count(got, "#") != 1 {
		t.Fatal("fragment не один")
	}
	// Кодирование как у наших ссылок (subfmt withFragment: u.Fragment = имя).
	link := "vless://u@a.example:443?type=tcp&sni=a.example#Чужое"
	for _, label := range []string{"Запасной (xray)!", "a*b/c?d", "⚠ Запасной - xray", "x y"} {
		u, err := url.Parse(link)
		if err != nil {
			t.Fatal(err)
		}
		u.Fragment, u.RawFragment = label, ""
		if got, want := Named(link, label), u.String(); got != want {
			t.Fatalf("Named(%q) = %q, want %q", label, got, want)
		}
	}
	if got := Named("hy2://p@a.example:443?sni=a", "Запасной (hy2)"); !strings.HasSuffix(got, "%20(hy2)") {
		t.Fatalf("скобки закодированы: %q", got)
	}
}

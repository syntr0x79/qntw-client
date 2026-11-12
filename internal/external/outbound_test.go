package external

import (
	"strings"
	"testing"

	"github.com/xtls/xray-core/app/proxyman"
	"google.golang.org/protobuf/proto"
)

func TestOutbound(t *testing.T) {
	const id = "11111111-2222-3333-4444-555555555555"
	pbk := "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA"
	links := map[string]string{
		"reality": "vless://" + id + "@a.example:443?type=tcp&security=reality&pbk=" + pbk + "&sid=ab&sni=x.example&fp=chrome&flow=xtls-rprx-vision",
		"hy2":     "hy2://pass@a.example:443?sni=a.example",
		"xhttp":   "vless://" + id + "@a.example:443?type=xhttp&security=tls&sni=a.example&mode=packet-up&path=%2Fp&host=h.example",
		"ws":      "vless://" + id + "@a.example:443?type=ws&security=tls&sni=a.example&path=%2Fw&host=h.example",
		"grpc":    "vless://" + id + "@a.example:443?type=grpc&security=tls&sni=a.example&serviceName=svc",
	}
	for name, link := range links {
		t.Run(name, func(t *testing.T) {
			cfg, err := Outbound(link)
			if err != nil {
				t.Fatal(err)
			}
			if cfg.Tag != "probe" {
				t.Fatalf("tag = %q", cfg.Tag)
			}
			inst, err := cfg.SenderSettings.GetInstance()
			if err != nil {
				t.Fatal(err)
			}
			if inst.(*proxyman.SenderConfig).StreamSettings == nil {
				t.Fatal("нет streamSettings")
			}
			b, err := proto.Marshal(cfg)
			if err != nil || len(b) == 0 {
				t.Fatalf("marshal: %v, %d", err, len(b))
			}
		})
	}
	// Адрес и учётка попадают в исходящее.
	cfg, err := Outbound(links["hy2"])
	if err != nil {
		t.Fatal(err)
	}
	px, err := cfg.ProxySettings.GetInstance()
	if err != nil {
		t.Fatal(err)
	}
	if s := px.(interface{ String() string }).String(); !strings.Contains(s, "a.example") {
		t.Fatalf("адрес не найден: %s", s)
	}
	ss, err := cfg.SenderSettings.GetInstance()
	if err != nil {
		t.Fatal(err)
	}
	st := ss.(*proxyman.SenderConfig).StreamSettings
	if !strings.Contains(string(st.TransportSettings[0].Settings.Value), "pass") {
		t.Fatal("auth hy2 не попал в транспорт")
	}
	vc, err := Outbound(links["reality"])
	if err != nil {
		t.Fatal(err)
	}
	vp, err := vc.ProxySettings.GetInstance()
	if err != nil {
		t.Fatal(err)
	}
	if s := vp.(interface{ String() string }).String(); !strings.Contains(s, "a.example") || !strings.Contains(s, "port:443") {
		t.Fatalf("адрес/порт vless не в исходящем: %s", s)
	}
	if _, err := Outbound("trojan://p@a.example:443"); err == nil {
		t.Fatal("trojan должен отказывать")
	}
}

package external

import (
	"encoding/json"
	"fmt"
	"net/url"
	"strings"

	"github.com/google/uuid"
	"github.com/xtls/xray-core/core"
	xconf "github.com/xtls/xray-core/infra/conf"
)

// Outbound собирает исходящее xray из чужой ссылки: JSON того же вида, что
// окно «Клиент» протокола каталога, через xconf.OutboundDetourConfig.Build,
// тем же разборщиком, которым узел читает config.json. Tag "probe".
func Outbound(link string) (*core.OutboundHandlerConfig, error) {
	_, cfg, err := parseBuild(link)
	return cfg, err
}

// parseBuild — разбор ссылки и сборка исходящего за один проход.
func parseBuild(link string) (Parsed, *core.OutboundHandlerConfig, error) {
	p, err := parse(link)
	if err != nil {
		return Parsed{}, nil, err
	}
	cfg, err := build(p)
	if err != nil {
		return Parsed{}, nil, err
	}
	return p, cfg, nil
}

func build(p Parsed) (*core.OutboundHandlerConfig, error) {
	u, err := url.Parse(p.Link)
	if err != nil {
		return nil, err
	}
	q := u.Query()
	var out map[string]any
	switch p.Protocol {
	case ProtoHy2:
		out = hy2Outbound(p, u, q)
	default:
		out, err = vlessOutbound(p, u, q)
		if err != nil {
			return nil, err
		}
	}
	out["tag"] = "probe"
	blob, err := json.Marshal(out)
	if err != nil {
		return nil, err
	}
	var detour xconf.OutboundDetourConfig
	if err := json.Unmarshal(blob, &detour); err != nil {
		return nil, fmt.Errorf("external: xray не принял исходящее: %w", err)
	}
	cfg, err := detour.Build()
	if err != nil {
		return nil, fmt.Errorf("external: xray не принял исходящее: %w", err)
	}
	return cfg, nil
}

func hy2Outbound(p Parsed, u *url.URL, q url.Values) map[string]any {
	sni := q.Get("sni")
	if sni == "" {
		sni = p.Host
	}
	auth := u.User.Username()
	if pw, ok := u.User.Password(); ok {
		auth += ":" + pw
	}
	return map[string]any{
		"protocol": "hysteria",
		"settings": map[string]any{"version": 2, "address": p.Host, "port": p.Port},
		"streamSettings": map[string]any{
			"network":          "hysteria",
			"security":         "tls",
			"hysteriaSettings": map[string]any{"version": 2, "auth": auth},
			"tlsSettings":      map[string]any{"serverName": sni, "alpn": []string{"h3"}},
		},
	}
}

func vlessOutbound(p Parsed, u *url.URL, q url.Values) (map[string]any, error) {
	// xray принимает любую короткую строку как учётку (из неё делается uuid5),
	// а у чужого сервера это почти всегда опечатка: требуем настоящий uuid.
	if _, err := uuid.Parse(u.User.Username()); err != nil {
		return nil, fmt.Errorf("external: учётка vless не uuid")
	}
	user := map[string]any{"id": u.User.Username(), "encryption": "none"}
	if enc := q.Get("encryption"); enc != "" {
		user["encryption"] = enc
	}
	if f := q.Get("flow"); f != "" {
		user["flow"] = f
	}
	network := strings.ToLower(q.Get("type"))
	if network == "" || network == "raw" {
		network = "tcp"
	}
	stream := map[string]any{"network": network}

	sni := q.Get("sni")
	fp := q.Get("fp")
	switch sec := strings.ToLower(q.Get("security")); sec {
	case "reality":
		if q.Get("pbk") == "" {
			return nil, fmt.Errorf("external: reality без pbk")
		}
		spx := q.Get("spx")
		if spx == "" {
			spx = "/"
		}
		if fp == "" {
			fp = "chrome"
		}
		stream["security"] = "reality"
		stream["realitySettings"] = map[string]any{
			"serverName": sni, "publicKey": q.Get("pbk"), "shortId": q.Get("sid"),
			"spiderX": spx, "fingerprint": fp,
		}
	case "tls":
		if sni == "" {
			sni = p.Host
		}
		tls := map[string]any{"serverName": sni}
		if fp != "" {
			tls["fingerprint"] = fp
		}
		if a := q.Get("alpn"); a != "" {
			tls["alpn"] = strings.Split(a, ",")
		}
		stream["security"] = "tls"
		stream["tlsSettings"] = tls
	case "", "none":
	default:
		return nil, unsupported("security", "security "+sec)
	}

	path, host := q.Get("path"), q.Get("host")
	switch network {
	case "ws":
		ws := map[string]any{}
		if path != "" {
			ws["path"] = path
		}
		if host != "" {
			ws["host"] = host
		}
		stream["wsSettings"] = ws
	case "httpupgrade":
		hu := map[string]any{}
		if path != "" {
			hu["path"] = path
		}
		if host != "" {
			hu["host"] = host
		}
		stream["httpupgradeSettings"] = hu
	case "xhttp":
		xh := map[string]any{}
		if path != "" {
			xh["path"] = path
		}
		if host != "" {
			xh["host"] = host
		}
		if m := q.Get("mode"); m != "" {
			xh["mode"] = m
		}
		stream["xhttpSettings"] = xh
	case "grpc":
		stream["grpcSettings"] = map[string]any{"serviceName": q.Get("serviceName")}
	}

	return map[string]any{
		"protocol": "vless",
		"settings": map[string]any{"vnext": []any{map[string]any{
			"address": p.Host, "port": p.Port, "users": []any{user},
		}}},
		"streamSettings": stream,
	}, nil
}

// Package external — запасной путь через чужие серверы: разбор ссылок
// vless/hy2 из чужих подписок и сборка исходящего xray из них. Листовой
// пакет: probe, vpn, carousel и beacon его импортируют, он их — нет.
package external

import (
	"errors"
	"fmt"
	"net/url"
	"strconv"
	"strings"
)

// Protocol — протокол чужой ссылки. Поддерживаются только эти два.
type Protocol string

const (
	ProtoVLESS Protocol = "vless"
	ProtoHy2   Protocol = "hy2"
)

// ErrUnsupported — ссылка разобрана, но мы такие не держим (небезопасная,
// с маскировкой, которой у нас нет, заглушка). Не путать с мусором.
var ErrUnsupported = errors.New("external: не поддерживается")

// Parsed — разобранная ссылка. Link нормализован: hysteria2:// -> hy2://,
// fragment снят. UpstreamName — имя, которое дал ей продавец.
type Parsed struct {
	Protocol     Protocol
	Host         string
	Port         int
	Link         string
	UpstreamName string
}

// unsupportedError несёт метку для счётчика отчёта и человеческий текст.
type unsupportedError struct{ label, text string }

func (e *unsupportedError) Error() string { return ErrUnsupported.Error() + ": " + e.text }
func (e *unsupportedError) Unwrap() error { return ErrUnsupported }

func unsupported(label, text string) error { return &unsupportedError{label, text} }

// Reason — метка причины для счётчика отчёта ("trojan", "insecure", "obfs",
// "transport", "headerType", "stub"); для прочих ошибок — "".
func Reason(err error) string {
	var u *unsupportedError
	if errors.As(err, &u) {
		return u.label
	}
	return ""
}

// Транспорты, которые умеем собрать в исходящее.
var transports = map[string]bool{"tcp": true, "raw": true, "ws": true, "grpc": true, "xhttp": true, "httpupgrade": true}

func truthy(s string) bool {
	switch strings.ToLower(s) {
	case "1", "true", "yes":
		return true
	}
	return false
}

// ParseLink разбирает чужую ссылку vless:// или hysteria2:// / hy2://.
// Учётка (uuid или пароль hy2) берётся из ссылки как есть.
//
// Инвариант: всё, что ParseLink принял, Outbound соберёт. Для этого разбор
// заканчивается той же сборкой исходящего; её отказ у поддерживаемой ссылки
// даёт обычную ошибку (не ErrUnsupported), и отчёт считает такую сломанной.
func ParseLink(raw string) (Parsed, error) {
	p, _, err := parseBuild(raw)
	return p, err
}

func parse(raw string) (Parsed, error) {
	raw = strings.TrimSpace(raw)
	u, err := url.Parse(raw)
	if err != nil || u.Scheme == "" {
		return Parsed{}, fmt.Errorf("external: не ссылка")
	}
	scheme := strings.ToLower(u.Scheme)
	var proto Protocol
	switch scheme {
	case "vless":
		proto = ProtoVLESS
	case "hysteria2", "hy2":
		proto = ProtoHy2
	default:
		return Parsed{}, unsupported(scheme, "схема "+scheme)
	}
	q := u.Query()
	for _, k := range []string{"allowInsecure", "allow_insecure", "insecure"} {
		if truthy(q.Get(k)) {
			return Parsed{}, unsupported("insecure", "insecure")
		}
	}
	switch proto {
	case ProtoHy2:
		if q.Get("obfs") != "" {
			return Parsed{}, unsupported("obfs", "hy2 obfs")
		}
	case ProtoVLESS:
		if t := strings.ToLower(q.Get("type")); t != "" && !transports[t] {
			return Parsed{}, unsupported("transport", "транспорт "+t)
		}
		if h := strings.ToLower(q.Get("headerType")); h != "" && h != "none" {
			return Parsed{}, unsupported("headerType", "headerType "+h)
		}
	}
	host := u.Hostname()
	if host == "" {
		return Parsed{}, unsupported("stub", "пустой адрес")
	}
	port, err := strconv.Atoi(u.Port())
	if err != nil {
		return Parsed{}, fmt.Errorf("external: порт ссылки: %q", u.Port())
	}
	if host == "0.0.0.0" || port <= 1 || port > 65535 {
		return Parsed{}, unsupported("stub", "заглушка")
	}
	if u.User == nil || u.User.Username() == "" {
		return Parsed{}, fmt.Errorf("external: в ссылке нет учётки")
	}
	name := u.Fragment
	link := raw
	if i := strings.IndexByte(link, '#'); i >= 0 {
		link = link[:i]
	}
	if proto == ProtoHy2 && scheme == "hysteria2" {
		scheme = "hy2"
	}
	link = scheme + link[len(u.Scheme):]
	return Parsed{Protocol: proto, Host: host, Port: port, Link: link, UpstreamName: name}, nil
}

// Named ставит в ссылку своё имя вместо имени продавца (или добавляет его):
// клиенты показывают fragment. Имя кодируется так же, как у наших ссылок
// (url.URL{Fragment: …}, как subfmt.RouteView.withFragment): пробел — %20,
// кириллица — %XX, а ( ) ! * / ? остаются как есть.
func Named(link, label string) string {
	if i := strings.IndexByte(link, '#'); i >= 0 {
		link = link[:i]
	}
	return link + "#" + (&url.URL{Fragment: label}).EscapedFragment()
}

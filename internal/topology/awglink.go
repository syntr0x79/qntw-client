package topology

import (
	"encoding/base64"
	"fmt"
	"net"
	"net/netip"
	"net/url"
	"strconv"
	"strings"
)

type AWGClient struct {
	PrivateKey string
	Address    netip.Addr
}

// BuildAWGConf — конфиг клиента в формате amneziawg-tools. Этот же текст
// Incy получает целиком внутри awg:// (subscription-format.md INCY-DEV).
func BuildAWGConf(in Inbound, p AWGParams, c AWGClient) (string, error) {
	if p.ServerPublicKey == "" {
		return "", fmt.Errorf("%w: node has not reported its public key yet", ErrBadAWGParams)
	}
	if !c.Address.Is4() {
		return "", fmt.Errorf("%w: client address %s", ErrBadAWGParams, c.Address)
	}
	var b strings.Builder
	w := func(k string, v any) { fmt.Fprintf(&b, "%s = %v\n", k, v) }
	b.WriteString("[Interface]\n")
	w("PrivateKey", c.PrivateKey)
	w("Address", c.Address.String()+"/32")
	w("DNS", p.DNS)
	w("MTU", p.MTU)
	w("Jc", p.Jc)
	w("Jmin", p.Jmin)
	w("Jmax", p.Jmax)
	w("S1", p.S1)
	w("S2", p.S2)
	w("S3", p.S3)
	w("S4", p.S4)
	w("H1", p.H1)
	w("H2", p.H2)
	w("H3", p.H3)
	w("H4", p.H4)
	w("I1", p.I1)
	for i, v := range []string{p.I2, p.I3, p.I4, p.I5} {
		if v != "" {
			w(fmt.Sprintf("I%d", i+2), v)
		}
	}
	w("HeaderProtectionKey", p.HeaderProtectionKey)
	b.WriteString("\n[Peer]\n")
	w("PublicKey", p.ServerPublicKey)
	w("Endpoint", net.JoinHostPort(in.Host, strconv.Itoa(in.Port)))
	w("AllowedIPs", "0.0.0.0/0")
	w("PersistentKeepalive", p.Keepalive)
	return b.String(), nil
}

func BuildAWGLink(in Inbound, c AWGClient, label string) (string, error) {
	if in.Protocol != ProtocolAWG {
		return "", fmt.Errorf("%w: protocol %q", ErrUnsupportedTransport, in.Protocol)
	}
	p, err := ParseAWGParams(in.Transport)
	if err != nil {
		return "", err
	}
	conf, err := BuildAWGConf(in, p, c)
	if err != nil {
		return "", err
	}
	return "awg://" + base64.RawURLEncoding.EncodeToString([]byte(conf)) + "#" + url.PathEscape(label), nil
}

func ParseAWGLink(uri string) (conf, label string, err error) {
	rest, ok := strings.CutPrefix(uri, "awg://")
	if !ok {
		return "", "", fmt.Errorf("%w: not an awg:// link", ErrBadAWGParams)
	}
	body, frag, _ := strings.Cut(rest, "#")
	raw, err := base64.RawURLEncoding.DecodeString(strings.TrimRight(body, "="))
	if err != nil {
		return "", "", fmt.Errorf("%w: %v", ErrBadAWGParams, err)
	}
	label, err = url.PathUnescape(frag)
	if err != nil {
		return "", "", err
	}
	return string(raw), label, nil
}

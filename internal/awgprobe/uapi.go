package awgprobe

// Разбор .conf (формат topology.BuildAWGConf, он же amneziawg-tools) и
// перевод в UAPI amneziawg-go — то, что awg setconf делает на узле. Ключи
// в .conf в base64, в UAPI — в hex; параметры обфускации переходят 1:1,
// их строки (H1 "lo-hi", I1 "<r 48>") разбирает сам amneziawg-go.

import (
	"bufio"
	"context"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"net"
	"net/netip"
	"strconv"
	"strings"
)

// defaultKeepalive — если в .conf нет PersistentKeepalive: keepalive
// заставляет устройство начать рукопожатие сразу после Up, не дожидаясь
// первого пакета из туннеля.
const defaultKeepalive = 25

// fallbackDNS — резолвер внутри туннеля, если DNS в .conf пуст.
var fallbackDNS = netip.MustParseAddr("1.1.1.1")

// obfKeys — ключи [Interface], которые переходят в UAPI без изменений
// (только имя в нижнем регистре). Порядок — порядок в UAPI.
var obfKeys = []string{"Jc", "Jmin", "Jmax", "S1", "S2", "S3", "S4", "H1", "H2", "H3", "H4", "I1", "I2", "I3", "I4", "I5"}

type awgConf struct {
	privateKey string // hex
	address    netip.Addr
	dns        []netip.Addr
	mtu        int
	obf        map[string]string // ключ .conf → значение
	hpk        string            // hex

	publicKey  string // hex
	endpoint   string // host:port как в .conf
	allowedIPs []netip.Prefix
	keepalive  int
}

func parseConf(text string) (*awgConf, error) {
	c := &awgConf{obf: map[string]string{}, mtu: 1280}
	obf := map[string]bool{}
	for _, k := range obfKeys {
		obf[strings.ToLower(k)] = true
	}
	section := ""
	sc := bufio.NewScanner(strings.NewReader(text))
	for n := 1; sc.Scan(); n++ {
		line := strings.TrimSpace(sc.Text())
		if i := strings.IndexByte(line, '#'); i >= 0 {
			line = strings.TrimSpace(line[:i])
		}
		if line == "" {
			continue
		}
		if strings.HasPrefix(line, "[") {
			section = strings.ToLower(line)
			if section != "[interface]" && section != "[peer]" {
				return nil, fmt.Errorf("awgprobe: строка %d: секция %s", n, line)
			}
			continue
		}
		k, v, ok := strings.Cut(line, "=")
		if !ok {
			return nil, fmt.Errorf("awgprobe: строка %d: нет '='", n)
		}
		k, v = strings.TrimSpace(k), strings.TrimSpace(v)
		lk := strings.ToLower(k)
		var err error
		switch {
		case section == "[interface]" && lk == "privatekey":
			c.privateKey, err = keyHex(v)
		case section == "[interface]" && lk == "address":
			var p netip.Prefix
			p, err = netip.ParsePrefix(firstItem(v))
			c.address = p.Addr()
		case section == "[interface]" && lk == "dns":
			for _, s := range strings.Split(v, ",") {
				if s = strings.TrimSpace(s); s == "" {
					continue // «DNS = » — у входа в параметрах нет dns
				}
				a, e := netip.ParseAddr(s)
				if e != nil {
					err = e
					break
				}
				c.dns = append(c.dns, a)
			}
		case section == "[interface]" && lk == "mtu":
			c.mtu, err = strconv.Atoi(v)
		case section == "[interface]" && lk == "headerprotectionkey":
			c.hpk, err = keyHex(v)
		case section == "[interface]" && obf[lk]:
			c.obf[lk] = v
		case section == "[peer]" && lk == "publickey":
			c.publicKey, err = keyHex(v)
		case section == "[peer]" && lk == "endpoint":
			c.endpoint = v
		case section == "[peer]" && lk == "allowedips":
			for _, s := range strings.Split(v, ",") {
				p, e := netip.ParsePrefix(strings.TrimSpace(s))
				if e != nil {
					err = e
					break
				}
				c.allowedIPs = append(c.allowedIPs, p)
			}
		case section == "[peer]" && lk == "persistentkeepalive":
			c.keepalive, err = strconv.Atoi(v)
		default:
			// Незнакомый ключ — значит, .conf не наш или формат разошёлся
			// с BuildAWGConf: молча выбросить параметр обфускации нельзя.
			return nil, fmt.Errorf("awgprobe: строка %d: неизвестный ключ %q в %s", n, k, section)
		}
		if err != nil {
			return nil, fmt.Errorf("awgprobe: строка %d: %s: %w", n, k, err)
		}
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}
	if len(c.dns) == 0 {
		// DNS в .conf пуст или его нет, а имя из ControlURL резолвится внутри
		// туннеля (netstack): без сервера проверка упала бы на каждом входе
		// без dns. Клиент в таком случае ходит своим резолвером — берём тот
		// же 1.1.1.1, что NewAWGParams ставит по умолчанию.
		c.dns = []netip.Addr{fallbackDNS}
	}
	switch {
	case c.privateKey == "":
		return nil, fmt.Errorf("awgprobe: нет PrivateKey")
	case !c.address.IsValid():
		return nil, fmt.Errorf("awgprobe: нет Address")
	case c.publicKey == "":
		return nil, fmt.Errorf("awgprobe: нет PublicKey пира")
	case c.endpoint == "":
		return nil, fmt.Errorf("awgprobe: нет Endpoint пира")
	case len(c.allowedIPs) == 0:
		return nil, fmt.Errorf("awgprobe: нет AllowedIPs пира")
	}
	return c, nil
}

// uapi — текст для device.IpcSet и адрес, куда пойдут пакеты. Имя из
// Endpoint резолвится здесь (resolve), а не в amneziawg-go: так им
// управляет вызывающий — у коробки это резолвер через тот же модем.
func (c *awgConf) uapi(ctx context.Context, resolve func(context.Context, string) (netip.Addr, error)) (string, netip.AddrPort, error) {
	host, portStr, err := net.SplitHostPort(c.endpoint)
	if err != nil {
		return "", netip.AddrPort{}, fmt.Errorf("awgprobe: Endpoint %q: %w", c.endpoint, err)
	}
	port, err := strconv.ParseUint(portStr, 10, 16)
	if err != nil {
		return "", netip.AddrPort{}, fmt.Errorf("awgprobe: Endpoint %q: порт", c.endpoint)
	}
	addr, err := netip.ParseAddr(host)
	if err != nil {
		if resolve == nil {
			resolve = systemResolve
		}
		if addr, err = resolve(ctx, host); err != nil {
			return "", netip.AddrPort{}, fmt.Errorf("awgprobe: резолв %s: %w", host, err)
		}
	}
	ep := netip.AddrPortFrom(addr.Unmap(), uint16(port))

	var b strings.Builder
	w := func(k string, v any) { fmt.Fprintf(&b, "%s=%v\n", k, v) }
	w("private_key", c.privateKey)
	for _, k := range obfKeys {
		if v, ok := c.obf[strings.ToLower(k)]; ok {
			w(strings.ToLower(k), v)
		}
	}
	if c.hpk != "" {
		w("header_protection_key", c.hpk)
	}
	w("public_key", c.publicKey)
	w("endpoint", ep)
	ka := c.keepalive
	if ka == 0 {
		ka = defaultKeepalive
	}
	w("persistent_keepalive_interval", ka)
	for _, p := range c.allowedIPs {
		w("allowed_ip", p)
	}
	return b.String(), ep, nil
}

func systemResolve(ctx context.Context, host string) (netip.Addr, error) {
	addrs, err := net.DefaultResolver.LookupNetIP(ctx, "ip", host)
	if err != nil {
		return netip.Addr{}, err
	}
	// IPv4 первым: узлы AWG слушают на v4, v6 есть не у всех.
	for _, a := range addrs {
		if a.Unmap().Is4() {
			return a.Unmap(), nil
		}
	}
	if len(addrs) == 0 {
		return netip.Addr{}, fmt.Errorf("нет адресов")
	}
	return addrs[0], nil
}

// keyHex — ключ из .conf (base64, как пишет awg genkey) в hex для UAPI.
func keyHex(s string) (string, error) {
	k, err := base64.StdEncoding.DecodeString(s)
	if err != nil || len(k) != 32 {
		return "", fmt.Errorf("ключ не base64 на 32 байта")
	}
	return hex.EncodeToString(k), nil
}

func firstItem(s string) string {
	v, _, _ := strings.Cut(s, ",")
	return strings.TrimSpace(v)
}

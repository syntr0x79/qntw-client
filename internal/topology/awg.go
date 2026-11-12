package topology

// AmneziaWG — вход, который xray не принимает сам: его терминирует
// amneziawg-go на узле, а в xray трафик попадает через TPROXY во входящий
// dokodemo-door (nodecfg). Здесь живёт всё, что про такой вход знает
// топология: параметры протокола и ключи.
//
// Параметры лежат в Transport.Params, как и у REALITY, — так их переносят
// те же admin-ручки и та же колонка. Серверный публичный ключ — в "pbk":
// его сообщает агент через /agent/v1/keys, как REALITY-ключ.

import (
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"math/big"
	"net/netip"
	"sort"

	"golang.org/x/crypto/curve25519"
)

const (
	ProtocolAWG = "awg"
	KindAWG     = "awg"
)

var ErrBadAWGParams = errors.New("bad awg params")

type AWGParams struct {
	ServerPublicKey string
	Subnet          string
	TProxyPort      int
	ListenPort      int

	// Общие: обязаны совпадать у узла и клиента.
	HeaderProtectionKey string
	S1, S2, S3, S4      int
	H1, H2, H3, H4      string

	// Клиентские: узел их не знает, меняются без раскатки.
	Jc, Jmin, Jmax int
	I1             string
	// I2…I5 — только у протокола каталога, который их задал (Params
	// i2…i5); пустые в конфиг клиента не попадают.
	I2, I3, I4, I5 string
	MTU            int
	Keepalive      int
	DNS            string
}

// NewAWGParams — параметры нового входа. Общие генерируются случайно:
// одинаковые у всех, кто поставил AWG по инструкции, значения — ровно то,
// что ищет классификатор.
func NewAWGParams(listenPort int) (AWGParams, error) {
	hpk, _, err := NewAWGKeyPair()
	if err != nil {
		return AWGParams{}, err
	}
	p := AWGParams{
		Subnet: "10.66.0.0/16", TProxyPort: 12345, ListenPort: listenPort,
		HeaderProtectionKey: hpk,
		Jc:                  3, Jmin: 40, Jmax: 70, I1: "<r 48>", MTU: 1280, Keepalive: 25, DNS: "1.1.1.1",
	}
	s := make([]int, 4)
	for i := range s {
		n, err := randInt(12, 64) // ≥ 12 — требование header protection
		if err != nil {
			return AWGParams{}, err
		}
		s[i] = n
	}
	p.S1, p.S2, p.S3, p.S4 = s[0], s[1], s[2], s[3]

	h, err := headerRanges()
	if err != nil {
		return AWGParams{}, err
	}
	p.H1, p.H2, p.H3, p.H4 = h[0], h[1], h[2], h[3]
	return p, nil
}

// headerRanges — четыре непересекающихся диапазона внутри uint32, выше
// стандартных типов WireGuard 1..4. Пространство делится на четыре четверти,
// порядок четвертей перемешивается, внутри каждой берётся случайный отрезок.
func headerRanges() ([4]string, error) {
	const quarter = (1<<32 - 1 - 5) / 4
	order := []int{0, 1, 2, 3}
	for i := len(order) - 1; i > 0; i-- {
		j, err := randInt(0, i)
		if err != nil {
			return [4]string{}, err
		}
		order[i], order[j] = order[j], order[i]
	}
	var out [4]string
	for i, q := range order {
		base := 5 + q*quarter
		lo, err := randInt(base, base+quarter/2)
		if err != nil {
			return out, err
		}
		width, err := randInt(1<<16, quarter/4)
		if err != nil {
			return out, err
		}
		out[i] = fmt.Sprintf("%d-%d", lo, lo+width)
	}
	return out, nil
}

func randInt(lo, hi int) (int, error) {
	n, err := rand.Int(rand.Reader, big.NewInt(int64(hi-lo+1)))
	if err != nil {
		return 0, err
	}
	return lo + int(n.Int64()), nil
}

// NewAWGKeyPair — ключи WireGuard (X25519) в base64, как их пишет `awg genkey`.
func NewAWGKeyPair() (private, public string, err error) {
	var k [32]byte
	if _, err := rand.Read(k[:]); err != nil {
		return "", "", err
	}
	k[0] &= 248
	k[31] = (k[31] & 127) | 64
	priv := base64.StdEncoding.EncodeToString(k[:])
	pub, err := AWGPublicKey(priv)
	return priv, pub, err
}

func AWGPublicKey(private string) (string, error) {
	k, err := base64.StdEncoding.DecodeString(private)
	if err != nil || len(k) != 32 {
		return "", fmt.Errorf("%w: private key", ErrBadAWGParams)
	}
	pub, err := curve25519.X25519(k, curve25519.Basepoint)
	if err != nil {
		return "", err
	}
	return base64.StdEncoding.EncodeToString(pub), nil
}

func (p AWGParams) Transport() Transport {
	params := map[string]any{
		"pbk": p.ServerPublicKey, "subnet": p.Subnet, "tproxy_port": p.TProxyPort, "listen_port": p.ListenPort,
		"hpk": p.HeaderProtectionKey, "s1": p.S1, "s2": p.S2, "s3": p.S3, "s4": p.S4,
		"h1": p.H1, "h2": p.H2, "h3": p.H3, "h4": p.H4,
		"jc": p.Jc, "jmin": p.Jmin, "jmax": p.Jmax, "i1": p.I1, "mtu": p.MTU, "keepalive": p.Keepalive, "dns": p.DNS,
	}
	for k, v := range map[string]string{"i2": p.I2, "i3": p.I3, "i4": p.I4, "i5": p.I5} {
		if v != "" {
			params[k] = v
		}
	}
	return Transport{Kind: KindAWG, Sec: SecNone, Params: params}
}

func ParseAWGParams(tr Transport) (AWGParams, error) {
	if tr.Kind != KindAWG {
		return AWGParams{}, fmt.Errorf("%w: kind %q", ErrBadAWGParams, tr.Kind)
	}
	var p AWGParams
	var errs []string
	str := func(k string) string {
		s, _ := tr.Params[k].(string)
		return s
	}
	num := func(k string) int {
		switch v := tr.Params[k].(type) {
		case int:
			return v
		case float64:
			return int(v)
		}
		errs = append(errs, k)
		return 0
	}
	p.ServerPublicKey = str("pbk")
	p.Subnet, p.HeaderProtectionKey = str("subnet"), str("hpk")
	p.H1, p.H2, p.H3, p.H4 = str("h1"), str("h2"), str("h3"), str("h4")
	p.I1, p.DNS = str("i1"), str("dns")
	p.I2, p.I3, p.I4, p.I5 = str("i2"), str("i3"), str("i4"), str("i5")
	p.TProxyPort, p.ListenPort = num("tproxy_port"), num("listen_port")
	p.S1, p.S2, p.S3, p.S4 = num("s1"), num("s2"), num("s3"), num("s4")
	p.Jc, p.Jmin, p.Jmax = num("jc"), num("jmin"), num("jmax")
	p.MTU, p.Keepalive = num("mtu"), num("keepalive")
	if len(errs) > 0 {
		sort.Strings(errs)
		return AWGParams{}, fmt.Errorf("%w: missing or non-numeric %v", ErrBadAWGParams, errs)
	}
	if _, err := netip.ParsePrefix(p.Subnet); err != nil {
		return AWGParams{}, fmt.Errorf("%w: subnet %q", ErrBadAWGParams, p.Subnet)
	}
	for i, s := range []int{p.S1, p.S2, p.S3, p.S4} {
		if s < 12 {
			return AWGParams{}, fmt.Errorf("%w: S%d=%d, header protection needs >= 12", ErrBadAWGParams, i+1, s)
		}
	}
	if p.HeaderProtectionKey == "" || p.H1 == "" || p.H2 == "" || p.H3 == "" || p.H4 == "" {
		return AWGParams{}, fmt.Errorf("%w: header protection key and H1-H4 are required", ErrBadAWGParams)
	}
	if p.Jmin > p.Jmax || p.MTU < 1280 {
		return AWGParams{}, fmt.Errorf("%w: jmin/jmax or mtu", ErrBadAWGParams)
	}
	return p, nil
}

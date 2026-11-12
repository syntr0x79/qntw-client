package agentproto

import (
	"errors"
	"fmt"
	"net/netip"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// Меш на tinc (спека 2026-10-12-mesh-tinc, решение P4). tincd работает от
// root и исполняет скрипты из своего каталога (в том числе hosts/<имя>-up),
// поэтому агент узла не пишет туда ничего. Желаемый набор соседей он кладёт
// одним файлом TincPeers в свой каталог состояния; root-команда
// `vpnshop-hostagent tinc-apply` проверяет его (TincPeers.Validate) и сама
// пишет hosts/ и tinc.conf (TincHostFile, TincConf).

// TincPeers is the agent's desired tinc peer set (the JSON file).
type TincPeers struct {
	// Self — своё имя в tinc; должно совпасть с тем, что знает документ хоста.
	Self  string     `json:"self"`
	Peers []TincPeer `json:"peers"`
}

// TincPeer is one neighbour: its hosts/<Name> file and a ConnectTo line.
type TincPeer struct {
	Name    string   `json:"name"`
	Pubkey  string   `json:"pubkey"`  // значение Ed25519PublicKey, 43 знака base64 tinc
	Address string   `json:"address"` // IPv4 или имя хоста
	Port    int      `json:"port"`
	Subnets []string `json:"subnets"` // только /32 из разрешённых сетей
}

// Сети меша: TincMeshNet — адреса 10.44.0.x (боевые, на wg0 или tmesh0),
// TincTestNet — сеть проверки, куда tinc-up кладёт адрес на стадиях test и
// routed. Пир y — это 10.44.0.y и 10.45.0.y (решение P2). hostcfg берёт те же
// значения.
const (
	TincMeshNet = "10.44.0.0/24"
	TincTestNet = "10.45.0.0/24"
)

// MaxTincPeers bounds the file: the fleet has ~20 members.
const MaxTincPeers = 1000

var tincNamePattern = regexp.MustCompile(`^n[0-9a-f]{12}$`)

// ValidTincName says whether s has the shape fleet.TincName gives: «n» and
// 12 lowercase hex digits. Nothing else may name a file in hosts/: no «-up»,
// no path, no dot.
func ValidTincName(s string) bool { return tincNamePattern.MatchString(s) }

// tinc пишет base64 по-своему (src/utils.c, b64encode/b64decode в
// release-1.1pre18): алфавит стандартный, но биты идут с младших — первый
// знак несёт младшие шесть бит первого байта. Стандартный декодер дал бы
// другие 32 байта той же длины.
const tincB64Alphabet = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789+/"

// tincB64Decode decodes what tinc's b64encode writes, and only that: the
// original alphabet (tinc reads «-» and «_» too, but never writes them) and
// zero bits past the last byte. One key has exactly one accepted spelling, so
// the UNIQUE on tinc_pubkey sees every duplicate.
func tincB64Decode(s string) ([]byte, bool) {
	if len(s)%4 == 1 {
		return nil, false // шесть бит — не байт
	}
	out := make([]byte, 0, len(s)*3/4)
	for i := 0; i < len(s); i += 4 {
		var triplet uint32
		n := min(4, len(s)-i)
		for j := 0; j < n; j++ {
			v := strings.IndexByte(tincB64Alphabet, s[i+j])
			if v < 0 {
				return nil, false
			}
			triplet |= uint32(v) << (6 * j)
		}
		nb := n * 6 / 8
		if triplet>>(8*nb) != 0 {
			return nil, false // лишние биты: tinc пишет их нулями
		}
		for b := 0; b < nb; b++ {
			out = append(out, byte(triplet>>(8*b)))
		}
	}
	return out, true
}

// ValidTincPubkey: k is the value of an «Ed25519PublicKey = …» line from tinc
// 1.1 (ed25519_key.pub, hosts/<name>) in its one canonical spelling: 43
// characters that decode to the 32 bytes of an Ed25519 public key. Only the
// value — no «Ed25519PublicKey =», no spaces. fleet (the database) and the
// host agent (hosts/ files) check keys with this one function.
func ValidTincPubkey(k string) bool {
	if len(k) != 43 {
		return false
	}
	raw, ok := tincB64Decode(k)
	return ok && len(raw) == 32
}

// hostnameLabel is one LDH label of a host name.
var hostnameLabel = regexp.MustCompile(`^[a-zA-Z0-9]([a-zA-Z0-9-]{0,61}[a-zA-Z0-9])?$`)

// validTincAddress: an IPv4 address or a host name (LDH labels, at least one
// dot, at most 253 characters). Nothing that could end a line or add a word.
func validTincAddress(a string) bool {
	if ip, err := netip.ParseAddr(a); err == nil {
		return ip.Is4()
	}
	if len(a) == 0 || len(a) > 253 || !strings.Contains(a, ".") {
		return false
	}
	for _, l := range strings.Split(a, ".") {
		if !hostnameLabel.MatchString(l) {
			return false
		}
	}
	// Все метки из цифр — это кривой IPv4, а не имя.
	return strings.Trim(a, "0123456789.") != ""
}

// Validate checks the whole file; any bad entry refuses it all. self is the
// node's own tinc name as the host document knows it, own its own subnets
// (hosts/<self>); allowed are the networks a peer's /32 subnets may come
// from (the mesh and the test plane). With StrictSubnets tincd trusts only
// these files, so one /32 belongs to one member: a peer may not claim
// another peer's address or the node's own (решение P5).
func (p TincPeers) Validate(self string, own []string, allowed []netip.Prefix) error {
	var errs []error
	add := func(format string, a ...any) { errs = append(errs, fmt.Errorf(format, a...)) }
	if !ValidTincName(self) {
		return fmt.Errorf("tinc: своё имя %q недопустимо", self)
	}
	if p.Self != self {
		add("tinc: файл соседей для %q, а узел — %q", p.Self, self)
	}
	if len(p.Peers) > MaxTincPeers {
		add("tinc: соседей %d, больше %d", len(p.Peers), MaxTincPeers)
	}
	names, keys := map[string]bool{}, map[string]bool{}
	owner := map[string]string{} // /32 → чей
	for _, s := range own {
		owner[s] = self
	}
	for i, x := range p.Peers {
		w := fmt.Sprintf("tinc: peers[%d]", i)
		switch {
		case !ValidTincName(x.Name):
			add("%s: имя %q недопустимо", w, x.Name)
		case x.Name == self:
			add("%s: это сам узел", w)
		case names[x.Name]:
			add("%s: имя %s повторяется", w, x.Name)
		}
		names[x.Name] = true
		if !ValidTincPubkey(x.Pubkey) {
			add("%s: ключ %q недопустим", w, x.Pubkey)
		} else if keys[x.Pubkey] {
			add("%s: ключ повторяется", w)
		}
		keys[x.Pubkey] = true
		if !validTincAddress(x.Address) {
			add("%s: адрес %q — не IPv4 и не имя хоста", w, x.Address)
		}
		if x.Port < 1 || x.Port > 65535 {
			add("%s: порт %d вне 1..65535", w, x.Port)
		}
		if len(x.Subnets) == 0 {
			add("%s: нет подсетей", w)
		}
		for _, s := range x.Subnets {
			pf, err := netip.ParsePrefix(s)
			ok := err == nil && pf.Bits() == 32 && pf.Addr().Is4() && pf.String() == s
			if ok {
				in := false
				for _, a := range allowed {
					in = in || a.Contains(pf.Addr())
				}
				ok = in
			}
			switch o, taken := owner[s]; {
			case !ok:
				add("%s: подсеть %q — не /32 из разрешённых сетей", w, s)
			case taken && o == x.Name:
				add("%s: подсеть %s повторяется", w, s)
			case taken:
				add("%s: подсеть %s уже у %s", w, s, o)
			default:
				owner[s] = x.Name
			}
		}
	}
	return errors.Join(errs...)
}

// TincConf is tinc.conf: the fixed header (from the host document) and a
// ConnectTo per peer, sorted.
func TincConf(name, iface string, port int, connectTo []string) string {
	var b strings.Builder
	b.WriteString("Name = " + name + "\n")
	b.WriteString("Interface = " + iface + "\n")
	b.WriteString("Mode = router\n")
	b.WriteString("Port = " + strconv.Itoa(port) + "\n")
	b.WriteString("ExperimentalProtocol = yes\n")
	// Подсети узлов — только из host-файлов, которые пишет root (как
	// AllowedIPs у WireGuard): сосед не объявит чужой адрес меша.
	b.WriteString("StrictSubnets = yes\n")
	// Полный граф мета-соединений (спека «Связи»): по умолчанию tinc 1.1
	// держит около трёх соединений и рвёт лишние исходящие, ConnectTo
	// тоже, — тогда пара без своего соединения ходит через соседа, пока
	// UDP не проверен, а попарная блокировка ведёт себя по жребию (стенд
	// test/meshtinc).
	b.WriteString("AutoConnect = no\n")
	// Меш — только /32 узлов, широковещанию в нём делать нечего. Без этого
	// tinc шлёт по дереву соседей пакеты на свои BroadcastSubnet (ff00::/8,
	// 224.0.0.0/4, 255.255.255.255: IPv6 tmesh0 при подъёме), а у них нет
	// TTL: пока соседи по-разному видят граф, дерево даёт петлю, и пакет
	// крутится без конца (на стенде test/meshtinc — миллионы пакетов за
	// минуту между четырьмя узлами).
	b.WriteString("Broadcast = no\n")
	cs := append([]string(nil), connectTo...)
	sort.Strings(cs)
	for _, c := range cs {
		b.WriteString("ConnectTo = " + c + "\n")
	}
	return b.String()
}

// TincHostFile is a hosts/<name> file: Address, Port, Subnet lines and, for a
// peer, Ed25519PublicKey — nothing else (the own file has no key: tincd
// derives it from the private one).
func TincHostFile(address string, port int, subnets []string, pubkey string) string {
	var b strings.Builder
	b.WriteString("Address = " + address + "\n")
	b.WriteString("Port = " + strconv.Itoa(port) + "\n")
	for _, s := range subnets {
		b.WriteString("Subnet = " + s + "\n")
	}
	if pubkey != "" {
		b.WriteString("Ed25519PublicKey = " + pubkey + "\n")
	}
	return b.String()
}

// ParseTincConnectTo returns the ConnectTo names of a tinc.conf TincConf
// wrote; anything else is ignored.
func ParseTincConnectTo(conf string) []string {
	var out []string
	for _, l := range strings.Split(conf, "\n") {
		if n, ok := strings.CutPrefix(l, "ConnectTo = "); ok && ValidTincName(n) {
			out = append(out, n)
		}
	}
	return out
}

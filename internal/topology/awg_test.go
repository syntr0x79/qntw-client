package topology_test

import (
	"strconv"
	"strings"
	"testing"

	"github.com/syntr0x79/qntw-client/internal/topology"
)

func TestNewAWGParamsSatisfiesHeaderProtectionFloor(t *testing.T) {
	for i := 0; i < 200; i++ {
		p, err := topology.NewAWGParams(51821)
		if err != nil {
			t.Fatal(err)
		}
		for n, s := range []int{p.S1, p.S2, p.S3, p.S4} {
			if s < 12 {
				t.Fatalf("S%d = %d, header protection needs >= 12", n+1, s)
			}
		}
		if p.HeaderProtectionKey == "" {
			t.Fatal("no header protection key")
		}
	}
}

func TestNewAWGParamsHeaderRangesDoNotOverlap(t *testing.T) {
	for i := 0; i < 200; i++ {
		p, _ := topology.NewAWGParams(51821)
		type rng struct{ lo, hi uint64 }
		var rs []rng
		for _, h := range []string{p.H1, p.H2, p.H3, p.H4} {
			lo, hi, ok := strings.Cut(h, "-")
			if !ok {
				t.Fatalf("H %q is not a range", h)
			}
			a, _ := strconv.ParseUint(lo, 10, 32)
			b, _ := strconv.ParseUint(hi, 10, 32)
			if a > b || a < 5 { // 1..4 — стандартные типы WireGuard
				t.Fatalf("bad range %q", h)
			}
			rs = append(rs, rng{a, b})
		}
		for x := range rs {
			for y := x + 1; y < len(rs); y++ {
				if rs[x].lo <= rs[y].hi && rs[y].lo <= rs[x].hi {
					t.Fatalf("H ranges overlap: %v", rs)
				}
			}
		}
	}
}

func TestNewAWGParamsClientDefaults(t *testing.T) {
	p, _ := topology.NewAWGParams(51821)
	if p.Jc != 3 || p.Jmin != 40 || p.Jmax != 70 || p.I1 != "<r 48>" || p.MTU != 1280 ||
		p.Keepalive != 25 || p.DNS != "1.1.1.1" || p.Subnet != "10.66.0.0/16" ||
		p.TProxyPort != 12345 || p.ListenPort != 51821 {
		t.Fatalf("defaults drifted: %+v", p)
	}
}

func TestAWGParamsRoundTripThroughTransport(t *testing.T) {
	p, _ := topology.NewAWGParams(51821)
	p.ServerPublicKey = "c2VydmVy"
	got, err := topology.ParseAWGParams(p.Transport())
	if err != nil {
		t.Fatal(err)
	}
	if got != p {
		t.Fatalf("round trip:\n got %+v\nwant %+v", got, p)
	}
}

// Params из базы приходят с числами float64: JSONB → map[string]any.
func TestParseAWGParamsAcceptsJSONNumbers(t *testing.T) {
	p, _ := topology.NewAWGParams(51821)
	tr := p.Transport()
	for k, v := range tr.Params {
		if n, ok := v.(int); ok {
			tr.Params[k] = float64(n)
		}
	}
	if _, err := topology.ParseAWGParams(tr); err != nil {
		t.Fatal(err)
	}
}

func TestParseAWGParamsRejectsSmallS(t *testing.T) {
	p, _ := topology.NewAWGParams(51821)
	p.S3 = 11
	if _, err := topology.ParseAWGParams(p.Transport()); err == nil {
		t.Fatal("S3=11 accepted, header protection would refuse it on the node")
	}
}

func TestAWGKeyPairIsConsistent(t *testing.T) {
	priv, pub, err := topology.NewAWGKeyPair()
	if err != nil {
		t.Fatal(err)
	}
	again, err := topology.AWGPublicKey(priv)
	if err != nil || again != pub {
		t.Fatalf("public key mismatch: %q vs %q (%v)", again, pub, err)
	}
	if len(pub) != 44 {
		t.Fatalf("not a base64 x25519 key: %q", pub)
	}
}

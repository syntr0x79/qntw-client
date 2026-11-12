package external

import "testing"

func TestFlag(t *testing.T) {
	cases := []struct{ name, want string }{
		{"🇳🇱 🤖 ♾️ Netherlands | TCP (AI)", "🇳🇱"},
		{"🇵🇱 🎮 ♾️ Poland | Hysteria2-2", "🇵🇱"},
		{"  🇭🇰 Hong Kong", "🇭🇰"},
		{"Netherlands 🇳🇱", ""},
		{"🤖 🇳🇱 Netherlands", ""},
		{"🇳 broken", ""},
		{"", ""},
	}
	for _, c := range cases {
		if got := Flag(c.name); got != c.want {
			t.Errorf("Flag(%q) = %q, want %q", c.name, got, c.want)
		}
	}
}

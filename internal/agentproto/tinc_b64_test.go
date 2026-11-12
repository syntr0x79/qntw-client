package agentproto

import (
	"bytes"
	"encoding/base64"
	"testing"
)

// Порядок битов tinc не стандартный base64: первый знак — младшие шесть бит
// первого байта. Сверка на паре ключей из одного запуска tinc: в
// ed25519_key.priv (96 байт: 64 закрытых + 32 открытых) открытый ключ лежит
// с байта 64, то есть с другим выравниванием по тройкам, чем в
// ed25519_key.pub; совпасть оба декодирования могут только при верном
// порядке битов.
func TestTincBase64BitOrderMatchesTinc(t *testing.T) {
	const pub = "ACnRZcQGFTzmU7HskVfHnZj7UuSfCpP9znFRMisigpI"
	const priv = "gu1azmY96hXpnwFMTejwsTuOMLJxRbvijJsuY+1gmIX8ugU4KukbFMqbhFIvIThz" +
		"c01izvlOUnyeFhjNF3YFnCIcGlxBZUMNbStfwSW9dcmNuT5K9Jk+0PfWExIyKCmi"
	p, ok := tincB64Decode(pub)
	if !ok || len(p) != 32 {
		t.Fatalf("pub: %x %v", p, ok)
	}
	k, ok := tincB64Decode(priv)
	if !ok || len(k) != 96 {
		t.Fatalf("priv: %d bytes %v", len(k), ok)
	}
	if !bytes.Equal(k[64:], p) {
		t.Fatalf("открытый ключ в .priv %x, в .pub %x", k[64:], p)
	}
	// Закрытая часть — «зажатый» скаляр ed25519: младшие три бита первого
	// байта нули, у 32-го байта старшие биты 01.
	if k[0]&7 != 0 || k[31]&0xc0 != 0x40 {
		t.Fatalf("скаляр не зажат: %02x … %02x — порядок битов не tinc", k[0], k[31])
	}
	// Стандартный декодер дал бы другие байты: проверка не тавтология.
	if std, err := base64.RawStdEncoding.DecodeString(pub); err == nil && bytes.Equal(std, p) {
		t.Fatal("tinc и стандартный base64 совпали — тест ничего не проверяет")
	}
}

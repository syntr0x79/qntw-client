package external

import (
	"strings"
	"unicode/utf8"
)

// Flag — флажок страны в начале имени сервера у продавца («🇳🇱 🤖 Netherlands |
// TCP»): пара символов-букв региона (U+1F1E6–U+1F1FF) первым знаком имени.
// Флажка нет или он не первый — пусто. Остальное имя продавца клиенту не
// уходит, только флажок.
func Flag(upstream string) string {
	s := strings.TrimLeft(upstream, " \t")
	a, n := utf8.DecodeRuneInString(s)
	b, _ := utf8.DecodeRuneInString(s[n:])
	if !regionLetter(a) || !regionLetter(b) {
		return ""
	}
	return string([]rune{a, b})
}

func regionLetter(r rune) bool { return r >= 0x1F1E6 && r <= 0x1F1FF }

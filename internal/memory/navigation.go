package memory

import (
	"math"
	"unicode/utf8"

	"github.com/z-chenhao/assistant/internal/recall/lexical"
)

func NavigationIndexTerms(text string) []string {
	runes := []rune(text)
	return navigationTerms(runes[:min(4096, len(runes))], 96, 1024)
}

func NavigationQueryTerms(text string) []string {
	if len(text) > 4096 {
		text = text[len(text)-4096:]
		for len(text) > 0 && !utf8.RuneStart(text[0]) {
			text = text[1:]
		}
	}
	return navigationTerms([]rune(text), 256, math.MaxInt)
}

func navigationTerms(runes []rune, maxTerms, remainingBytes int) []string {
	out := []string{}
	seen := map[string]bool{}
	for len(runes) > 0 && len(out) < maxTerms {
		n := min(200, len(runes))
		for _, term := range lexical.Terms(string(runes[:n])) {
			if !seen[term] && len(out) < maxTerms && len(term) <= remainingBytes {
				seen[term] = true
				out = append(out, term)
				remainingBytes -= len(term)
			}
		}
		runes = runes[n:]
	}
	return out
}

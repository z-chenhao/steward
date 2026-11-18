package memory

import (
	"slices"
	"strings"
	"testing"
)

func TestMemoryNavigationKeepsIndexAndQueryWindows(t *testing.T) {
	text := "firstmarker " + strings.Repeat("padding ", 600) + "lastmarker"
	index, query := NavigationIndexTerms(text), NavigationQueryTerms(text)
	if !slices.Contains(index, "firstmarker") || slices.Contains(index, "lastmarker") ||
		slices.Contains(query, "firstmarker") || !slices.Contains(query, "lastmarker") {
		t.Fatalf("wrong index or query window: %q, %q", index, query)
	}
	var chinese strings.Builder
	for i := range 60 {
		chinese.WriteRune(rune(0x4e00 + i))
	}
	if got := len(NavigationQueryTerms(chinese.String())); got != 48 {
		t.Fatalf("short query returned %d bigrams, want 48", got)
	}
	for i := 60; i < 1000; i++ {
		chinese.WriteRune(rune(0x4e00 + i))
	}
	if got := len(NavigationIndexTerms(chinese.String())); got != 96 {
		t.Fatalf("index returned %d terms, want 96", got)
	}
	if got := len(NavigationQueryTerms(chinese.String())); got != 240 {
		t.Fatalf("query returned %d bigrams, want 240", got)
	}
}

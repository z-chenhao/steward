package recall

import (
	"strings"
	"testing"
	"unicode/utf8"
)

func TestExcerptIsBoundedVerbatimAndKeepsReadPosition(t *testing.T) {
	text := strings.Repeat("无关背景资料。", 400) + "The annual design workshop was on April 18 in Oslo. " + strings.Repeat("后续资料。", 300)
	h := Excerpt(Hit{Content: text, SourceRef: "conversation:exact"}, "design workshop April", 240)
	if len(h.Content) > 240 || !utf8.ValidString(h.Content) || !strings.Contains(h.Content, "April 18") || h.Offset == 0 || text[h.Offset:h.Offset+len(h.Content)] != h.Content || h.TotalBytes != len(text) {
		t.Fatal(h)
	}
}
func TestPrecisionKeepsWordsAndCompleteQuotedReferencesDistinct(t *testing.T) {
	if Relevance(Terms("art"), "We started research in particle physics") != 0 {
		t.Fatal("substring was treated as an event match")
	}
	if Relevance(Terms("art"), "An art exhibition") <= 0 {
		t.Fatal("lost a real word")
	}
	if QuotedMatch(`"session_ac7_4"`, "session_ac7_2") || !QuotedMatch(`"session_ac7_4"`, "Original session_ac7_4 evidence") {
		t.Fatal("reference suffix lost")
	}
}

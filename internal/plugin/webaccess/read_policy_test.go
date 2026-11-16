package webaccess

import (
	"strings"
	"testing"
	"unicode/utf8"
)

func TestWebAccessReadPolicyPreservesSelectorsModesAndByteWindows(t *testing.T) {
	text := "前言 İSTANBUL 中文证据 Fact one\nFact two🧭" + strings.Repeat("后文 ", 10000)
	doc := Document{Version: 1, Pages: []Page{{Text: text}, {Text: "second page only"}}}
	for _, q := range []ReadRequest{
		{}, {Offset: 1, Limit: 1}, {Offset: 2, Limit: 7}, {Offset: len(text), Limit: 1},
		{URLIndex: 1}, {URLIndex: 2}, {URLIndex: -1}, {Offset: len(text) + 1}, {Limit: 32001},
		{FindText: "istanbul", FindMode: "case-insensitive", Limit: 400},
		{FindText: "Fact one Fact two", FindMode: "fuzzy", Limit: 1},
		{FindText: "absent", FindMode: "exact", Offset: 5},
		{FindText: "second page only", FindMode: "exact"},
		{FindText: "Fact one", FindMode: "invalid"}, {FindMode: "ignored without find"},
		{FindText: strings.Repeat("x", 501)}, {FindText: "  ", FindMode: "fuzzy"},
	} {
		p := planContentRead(doc, q)
		original, err := ReadContent(doc, q.URLIndex, q.QueryIndex, q.Offset, q.Limit, q.FindText, q.FindMode)
		if err != nil {
			if p.ErrorCode != err.Error() {
				t.Fatal(q, p, err)
			}
			continue
		}
		if p.ErrorCode != "" || p.Offset != original.Offset || p.NextOffset != original.NextOffset || (p.Found == nil) != (original.Found == nil) || p.Found != nil && *p.Found != *original.Found {
			t.Fatal(q, p, original)
		}
		if original.Found == nil || *original.Found {
			source, _ := EvidenceText(doc, q.URLIndex, q.QueryIndex)
			if source[p.Offset:p.NextOffset] != original.Text || !utf8.ValidString(original.Text) {
				t.Fatal(q, p)
			}
		}
	}
	// Ordered fuzzy matching may span more than one maximum-size window. V1
	// still reports the first match and a partial window, never a false absence.
	long := Document{Version: 1, Pages: []Page{{Text: "alpha" + strings.Repeat("x", 40000) + " omega"}}}
	p := planContentRead(long, ReadRequest{FindText: "alpha omega", FindMode: "fuzzy"})
	if p.Found == nil || !*p.Found || p.NextOffset != 32000 || p.MatchEnd != len(long.Pages[0].Text) {
		t.Fatal(p)
	}
	query := Document{Version: 1, Queries: []SearchResult{{Query: "<original query>"}}}
	selected, err := EvidenceText(query, 0, 0)
	if err != nil || !strings.Contains(selected, `\u003coriginal query\u003e`) {
		t.Fatal(selected, err)
	}
	if p := planContentRead(Document{Version: 2}, ReadRequest{}); p.ErrorCode != "INVALID_ARGUMENT" {
		t.Fatal(p)
	}
}

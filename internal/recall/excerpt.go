package recall

import (
	"sort"
	"strings"
	"unicode/utf8"
)

// Excerpt is a contiguous byte window of the original, never a generated summary.
// Offsets let a caller read the exact source instead of searching the same ID.
func Excerpt(hit Hit, query string, limit int) Hit {
	text := hit.Content
	if len(hit.Evidence) > 2 {
		hit.Evidence = hit.Evidence[:2]
	}
	hit.TotalBytes = len(text)
	if len(text) <= limit {
		return hit
	}
	terms := Terms(query)
	best, start := -1.0, 0
	lower := strings.ToLower(text)
	for _, term := range terms {
		for offset, n := 0, 0; offset < len(lower) && n < 64; n++ {
			found := strings.Index(lower[offset:], term)
			if found < 0 {
				break
			}
			at := offset + found
			offset = at + len(term)
			left := max(0, min(len(text)-limit, at-limit/3))
			right := min(len(text), left+limit)
			score := Relevance(terms, text[left:right])
			if score > best {
				best, start = score, left
			}
		}
	}
	for start > 0 && !utf8.RuneStart(text[start]) {
		start--
	}
	end := min(len(text), start+limit)
	for end > start && end < len(text) && !utf8.RuneStart(text[end]) {
		end--
	}
	hit.Offset, hit.Content = start, text[start:end]
	// Proof quotes are original evidence, separate from the derived excerpt.
	if len(hit.Evidence) > 2 {
		hit.Evidence = hit.Evidence[:2]
	}
	return hit
}

// QuotedMatch honors explicit phrases, including complete references whose
// suffix would otherwise disappear during tokenization.
func QuotedMatch(query, text string) bool {
	parts := strings.Split(query, `"`)
	for i := 1; i < len(parts)-1; i += 2 {
		phrase := strings.TrimSpace(parts[i])
		if phrase != "" && !strings.Contains(strings.ToLower(text), strings.ToLower(phrase)) {
			return false
		}
	}
	return true
}

// RelevantEvidence keeps exact Host-validated quotes; a wiki's first quotes may
// concern a different section. Stable ties preserve the original attribution.
func RelevantEvidence(in []Evidence, query string, limit int) []Evidence {
	out := append([]Evidence(nil), in...)
	terms := Terms(query)
	sort.SliceStable(out, func(i, j int) bool { return Relevance(terms, out[i].Quote) > Relevance(terms, out[j].Quote) })
	if len(out) > limit {
		out = out[:limit]
	}
	return out
}

package recall

import (
	"strings"
	"unicode"
)

// Terms is the bounded lexical policy for local retrieval, not a language model
// or a cross-language semantic promise. CJK bigrams work without unicode61 CJK
// segmentation; ordinary request/format words must not retrieve unrelated work.
func Terms(text string) []string {
	stop := map[string]bool{}
	for _, s := range strings.Fields("a an the and or to of in on for is are was be my me i you your please this that with from about user task anchor request now can would should could do it use show give want 调研 研究 现在 给我 你给 我想 想要 请问 一下 一个 这个 那个 什么 怎么 如何 帮我 用户 需要 可以 进行 使用 内容 请求 展示 确认 提供 请用") {
		stop[s] = true
	}
	seen := map[string]bool{}
	out := []string{}
	add := func(s string) {
		if s != "" && !stop[s] && !seen[s] && len(out) < 48 {
			seen[s] = true
			out = append(out, s)
		}
	}
	for _, part := range strings.FieldsFunc(strings.ToLower(text), func(r rune) bool { return !unicode.IsLetter(r) && !unicode.IsNumber(r) }) {
		var latin, cjk []rune
		flush := func() {
			if len(latin) > 1 {
				add(string(latin))
			}
			latin = nil
			if len(cjk) == 1 {
				add(string(cjk))
			} else {
				for i := 0; i+1 < len(cjk); i++ {
					add(string(cjk[i : i+2]))
				}
			}
			cjk = nil
		}
		for _, r := range part {
			if unicode.Is(unicode.Han, r) {
				if len(latin) > 0 {
					flush()
				}
				cjk = append(cjk, r)
			} else {
				if len(cjk) > 0 {
					flush()
				}
				latin = append(latin, r)
			}
		}
		flush()
	}
	return out
}

// Relevance measures lexical coverage on [0,1]. Long Latin identifiers anchor a
// mixed-language query, so an incidental Chinese request word is insufficient.
func Relevance(terms []string, content string) float64 {
	if len(terms) == 0 {
		return 0
	}
	text := strings.ToLower(content)
	total, matched, anchors, anchorHits := 0., 0., 0, 0
	for _, term := range terms {
		weight := 1.
		anchor := len(term) >= 4 && term[0] < 128
		if anchor {
			weight = 3
			anchors++
		}
		total += weight
		if containsTerm(text, term) {
			matched += weight
			if anchor {
				anchorHits++
			}
		}
	}
	if anchors > 0 && anchorHits == 0 {
		return 0
	}
	return matched / total
}

// Latin words match tokens, not incidental substrings such as art in "started".
// CJK retrieval intentionally uses character bigrams.
func containsTerm(text, term string) bool {
	if term == "" {
		return false
	}
	if term[0] >= 128 {
		return strings.Contains(text, term)
	}
	for offset := 0; offset < len(text); {
		n := strings.Index(text[offset:], term)
		if n < 0 {
			return false
		}
		at := offset + n
		end := at + len(term)
		offset = end
		word := func(b byte) bool { return b >= 'a' && b <= 'z' || b >= '0' && b <= '9' || b == '_' }
		if (at == 0 || !word(text[at-1])) && (end == len(text) || !word(text[end])) {
			return true
		}
	}
	return false
}

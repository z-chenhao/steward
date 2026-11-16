package taskcontrol

import (
	"math"
	"sort"
	"strings"
	"unicode"

	"github.com/z-chenhao/assistant/internal/plugin/taskcontrol/contract"
)

// Candidate text includes the anchor and retained instructions. Rank combines
// lexical matches and local distributional similarity: documents matching query
// terms supply co-occurring context terms, which retrieve related documents
// without exact query overlap. No provider, network, credentials or model wake
// is needed. This small-corpus ranker makes no cross-language embedding claim.

func terms(text string) map[string]float64 {
	out := map[string]float64{}
	words := strings.FieldsFunc(strings.ToLower(text), func(r rune) bool { return !unicode.IsLetter(r) && !unicode.IsNumber(r) })
	for _, word := range words {
		if len([]rune(word)) > 1 {
			out[word]++
		}
		// CJK has no whitespace word boundaries. Index adjacent characters as well.
		rs := []rune(word)
		for i := 0; i+1 < len(rs); i++ {
			if unicode.Is(unicode.Han, rs[i]) && unicode.Is(unicode.Han, rs[i+1]) {
				out[string(rs[i:i+2])]++
			}
		}
	}
	return out
}
func Rank(f contract.SearchFacts) contract.SearchPlan {
	candidates, query, limit := f.Candidates, f.Query, f.Limit
	vectors := make([]map[string]float64, len(candidates))
	df := map[string]float64{}
	q := terms(query)
	queryTerms := sortedTerms(q)
	for i, c := range candidates {
		vectors[i] = terms(c.Text)
		for w := range vectors[i] {
			df[w]++
		}
	}
	idf := func(w string) float64 { return 1 + math.Log(1+float64(len(candidates))/(1+df[w])) }
	// Limit expansion to terms observed in documents with actual lexical evidence.
	expanded := map[string]float64{}
	for _, v := range vectors {
		overlap := 0.0
		for _, w := range queryTerms {
			if v[w] > 0 {
				overlap += idf(w)
			}
		}
		if overlap > 0 {
			norm := math.Sqrt(float64(len(v)))
			for _, w := range sortedTerms(v) {
				expanded[w] += overlap * idf(w) / norm
			}
		}
	}
	for _, w := range queryTerms {
		expanded[w] += 2 * idf(w)
	}
	type scored struct {
		index int
		score float64
	}
	queryNorm := 0.0
	for _, w := range sortedTerms(expanded) {
		queryNorm += expanded[w] * expanded[w]
	}
	ranked := []scored{}
	for i := range candidates {
		lexical := 0.0
		dot, norm := 0.0, 0.0
		for _, w := range sortedTerms(vectors[i]) {
			n := vectors[i][w]
			weight := (1 + math.Log(n)) * idf(w)
			norm += weight * weight
			dot += weight * expanded[w]
			if q[w] > 0 {
				lexical += idf(w)
			}
		}
		semantic := 0.0
		if norm > 0 && queryNorm > 0 {
			semantic = dot / math.Sqrt(norm*queryNorm)
		}
		if query != "" && lexical == 0 && semantic < 0.15 {
			continue
		}
		ranked = append(ranked, scored{i, lexical + semantic})
	}
	statusRank := map[string]int{"active": 0, "paused": 1, "completed": 2, "cancelled": 3}
	sort.Slice(ranked, func(i, j int) bool {
		a, b := ranked[i], ranked[j]
		if statusRank[candidates[a.index].Status] != statusRank[candidates[b.index].Status] {
			return statusRank[candidates[a.index].Status] < statusRank[candidates[b.index].Status]
		}
		if a.score != b.score {
			return a.score > b.score
		}
		if candidates[a.index].UpdatedAt != candidates[b.index].UpdatedAt {
			return candidates[a.index].UpdatedAt > candidates[b.index].UpdatedAt
		}
		return a.index < b.index
	})
	out := make([]int, 0, min(limit, len(ranked)))
	for _, v := range ranked {
		if len(out) == limit {
			break
		}
		out = append(out, v.index)
	}
	return contract.SearchPlan{Indices: out}
}

func sortedTerms(v map[string]float64) []string {
	out := make([]string, 0, len(v))
	for w := range v {
		out = append(out, w)
	}
	sort.Strings(out)
	return out
}

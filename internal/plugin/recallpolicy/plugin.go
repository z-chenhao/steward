// Package recallpolicy owns independent recall merge, excerpt and follow-up
// query policy. It cannot search storage, grant scope or emit source references.
package recallpolicy

import (
	"context"
	"errors"
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/z-chenhao/assistant/internal/plugin/recallpolicy/contract"
	"github.com/z-chenhao/assistant/pkg/rsiplugin"
)

func Plugin() rsiplugin.Handlers {
	input := func(ctx context.Context, c rsiplugin.Call) ([]byte, error) {
		var req contract.Request
		if err := rsiplugin.Decode(c.Input, &req); err != nil {
			return nil, err
		}
		if len(c.Inputs) != 1 || c.Inputs[0].ID != req.InputID {
			return nil, errors.New("missing authorized recall facts")
		}
		return rsiplugin.ReadInput(ctx, c.Inputs[0])
	}
	return rsiplugin.Handlers{PluginID: contract.PluginID, Version: "1.0.1", Services: map[string]func(context.Context, rsiplugin.Call) (rsiplugin.Result, error){
		contract.Contract: func(ctx context.Context, c rsiplugin.Call) (rsiplugin.Result, error) {
			raw, err := input(ctx, c)
			if err != nil {
				return rsiplugin.Result{}, err
			}
			var f contract.Facts
			if err = rsiplugin.DecodeInput(raw, &f); err != nil {
				return rsiplugin.Result{}, err
			}
			if len(f.Candidates) > 100 || len(f.Query) > 4096 || f.ExcerptBytes < 0 || f.ExcerptBytes > 65536 {
				return rsiplugin.Result{}, errors.New("invalid recall bounds")
			}
			return rsiplugin.JSONResult(ctx, Select(f))
		},
		contract.QueryContract: func(ctx context.Context, c rsiplugin.Call) (rsiplugin.Result, error) {
			raw, err := input(ctx, c)
			if err != nil {
				return rsiplugin.Result{}, err
			}
			var f contract.QueryFacts
			if err = rsiplugin.DecodeInput(raw, &f); err != nil {
				return rsiplugin.Result{}, err
			}
			return rsiplugin.JSONResult(ctx, contract.QueryPlan{Query: Query(f)})
		},
	}}
}

func Select(f contract.Facts) contract.Plan {
	order := make([]int, len(f.Candidates))
	for i := range order {
		order[i] = i
	}
	rank := func(c contract.Candidate) int {
		if c.SourceType == "conversation" {
			return 0
		}
		if c.Layer == "representations" {
			return 2
		}
		return 1
	}
	sort.SliceStable(order, func(i, j int) bool {
		a, b := f.Candidates[order[i]], f.Candidates[order[j]]
		if f.EvidenceFirst && a.Relevance == b.Relevance && rank(a) != rank(b) {
			return rank(a) < rank(b)
		}
		return a.Score > b.Score
	})
	out := contract.Plan{Entries: []contract.Selection{}}
	for _, i := range order {
		c := f.Candidates[i]
		p := contract.Selection{Index: i, Length: len(c.Content), Evidence: []int{}}
		if f.ExcerptBytes > 0 {
			p.Offset, p.Length = excerpt(c.Content, f.Query, f.ExcerptBytes)
			proofs := make([]int, len(c.Quotes))
			for i := range proofs {
				proofs[i] = i
			}
			if f.EvidenceFirst {
				terms := Terms(f.Query)
				sort.SliceStable(proofs, func(i, j int) bool {
					return Relevance(terms, c.Quotes[proofs[i]]) > Relevance(terms, c.Quotes[proofs[j]])
				})
			}
			p.Evidence = append(p.Evidence, proofs[:min(2, len(proofs))]...)
		}
		out.Entries = append(out.Entries, p)
	}
	return out
}

func excerpt(text, query string, limit int) (int, int) {
	if len(text) <= limit {
		return 0, len(text)
	}
	terms := Terms(query)
	best, start := -1., 0
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
	return start, end - start
}
func clip(s string, n int) string {
	if len(s) <= n {
		return s
	}
	s = s[:n]
	for !utf8.ValidString(s) {
		s = s[:len(s)-1]
	}
	return s
}
func Query(f contract.QueryFacts) string {
	if f.Previous == "" && f.Answer == "" {
		return f.Latest
	}
	latest := clip(f.Latest, 2048)
	return latest + clip("\nRecent conversation:\n"+clip(f.Previous, 1000)+"\n"+clip(f.Answer, 1800), 4096-len(latest))
}

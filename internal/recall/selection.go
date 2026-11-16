package recall

import (
	"context"
	"encoding/json"
	"strings"
	"unicode/utf8"
)

// selectManaged enforces provenance, exact substring/evidence reconstruction,
// de-duplication and output budgets independently of evolvable order/excerpts.
func SelectManaged(ctx context.Context, q Query, candidates []Candidate, budget int, selector func(context.Context, Query, []Candidate) ([]Selection, error)) ([]Hit, error) {
	plan, err := selector(ctx, q, candidates)
	if err != nil {
		return nil, err
	}
	if len(plan) != len(candidates) {
		return nil, ErrInvalid
	}
	visited := map[int]bool{}
	for _, p := range plan {
		if p.Index < 0 || p.Index >= len(candidates) || visited[p.Index] {
			return nil, ErrInvalid
		}
		visited[p.Index] = true
	}
	memorySources, conversationSources := map[string]bool{}, map[string]bool{}
	seen := map[string]bool{}
	out := []Hit{}
	used := 0
	for _, p := range plan {
		h := candidates[p.Index].Hit
		if p.Offset < 0 || p.Length < 0 || p.Offset > len(h.Content) || p.Length > len(h.Content)-p.Offset {
			return nil, ErrInvalid
		}
		if q.ExcerptBytes == 0 && (p.Offset != 0 || p.Length != len(h.Content) || len(p.Evidence) > 0) {
			return nil, ErrInvalid
		}
		if q.ExcerptBytes > 0 {
			if p.Length > q.ExcerptBytes || len(p.Evidence) > 2 || !utf8.ValidString(h.Content[p.Offset:p.Offset+p.Length]) {
				return nil, ErrInvalid
			}
			proof := []Evidence{}
			seenProof := map[int]bool{}
			for _, i := range p.Evidence {
				if i < 0 || i >= len(h.Evidence) || seenProof[i] {
					return nil, ErrInvalid
				}
				seenProof[i] = true
				proof = append(proof, h.Evidence[i])
			}
			h.TotalBytes = len(h.Content)
			h.Offset = p.Offset
			h.Content = h.Content[p.Offset : p.Offset+p.Length]
			h.Evidence = proof
		}
		if h.SourceType == "conversation" && !q.EvidenceFirst && memorySources[h.SourceRef] {
			continue
		}
		if h.SourceType == "memory" && !q.EvidenceFirst {
			echo := false
			for _, ref := range h.Sources {
				echo = echo || conversationSources[ref]
			}
			if echo {
				continue
			}
		}
		text := strings.ToLower(strings.Join(strings.Fields(candidates[p.Index].Hit.Content), " "))
		if seen[h.SourceRef] && h.SourceRef != "" || seen["text:"+text] {
			continue
		}
		raw, _ := json.Marshal(h)
		if used+len(raw) > budget {
			continue
		}
		if h.SourceType == "memory" && (h.TotalBytes == 0 || h.TotalBytes <= len(h.Content)) {
			for _, ref := range h.Sources {
				memorySources[ref] = true
			}
		} else if h.SourceType == "conversation" && (h.TotalBytes == 0 || h.TotalBytes <= len(h.Content)) {
			conversationSources[h.SourceRef] = true
		}
		out = append(out, h)
		used += len(raw)
		seen[h.SourceRef] = true
		seen["text:"+text] = true
		if len(out) >= q.Count() {
			break
		}
	}
	return out, nil
}

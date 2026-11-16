package recall_test

import (
	"context"
	"encoding/json"
	"fmt"
	"math/rand/v2"
	"strings"
	"testing"
	"time"

	"github.com/z-chenhao/assistant/internal/plugin/recallpolicy"
	"github.com/z-chenhao/assistant/internal/plugin/recallpolicy/contract"
	"github.com/z-chenhao/assistant/internal/recall"
)

func selectionPolicy(_ context.Context, q recall.Query, candidates []recall.Candidate) ([]recall.Selection, error) {
	f := contract.Facts{Query: q.Query, EvidenceFirst: q.EvidenceFirst, ExcerptBytes: q.ExcerptBytes, Candidates: []contract.Candidate{}}
	for _, v := range candidates {
		quotes := []string{}
		for _, e := range v.Hit.Evidence {
			quotes = append(quotes, e.Quote)
		}
		f.Candidates = append(f.Candidates, contract.Candidate{Content: v.Hit.Content, Layer: v.Hit.Layer, SourceType: v.Hit.SourceType, Relevance: v.Hit.Relevance, Score: v.Score, Quotes: quotes})
	}
	p := recallpolicy.Select(f)
	out := make([]recall.Selection, len(p.Entries))
	for i, v := range p.Entries {
		out[i] = recall.Selection{Index: v.Index, Offset: v.Offset, Length: v.Length, Evidence: v.Evidence}
	}
	return out, nil
}

func TestRecallPluginSelectionPreservesNativeEvidenceAndBudgets(t *testing.T) {
	rng := rand.New(rand.NewPCG(41, 94))
	ctx := context.Background()
	scope := recall.Scope{UserID: "u", AgentID: "a"}
	for trial := 0; trial < 150; trial++ {
		a, b := []recall.Hit{}, []recall.Hit{}
		for i := 0; i < rng.IntN(30)+1; i++ {
			key := fmt.Sprint(rng.IntN(8))
			text := "launch 庆功宴 evidence " + strings.Repeat("prefix 资料 ", rng.IntN(100)) + key
			hit := recall.Hit{Content: text, SourceType: "memory", SourceRef: "memory:" + key, Sources: []string{"conversation:" + key}, Relevance: float64(rng.IntN(4)) / 4, OccurredAt: time.Unix(int64(i), 0), Layer: "representations", Evidence: []recall.Evidence{{SourceRef: "original:0", Quote: "other event"}, {SourceRef: "original:1", Quote: "launch evidence"}, {SourceRef: "original:2", Quote: "庆功宴 confirmed"}}}
			a = append(a, hit)
			hit.SourceType = "conversation"
			hit.SourceRef = "conversation:" + key
			hit.Sources = nil
			hit.Content = "original " + text
			b = append(b, hit)
		}
		q := recall.Query{Query: "launch 庆功宴", Limit: rng.IntN(20) + 1, EvidenceFirst: trial%2 == 0, ExcludeRefs: []string{"conversation:3"}, SeenRefs: []string{"memory:1"}}
		if trial%3 != 0 {
			q.ExcerptBytes = 80 + rng.IntN(1400)
		}
		s := recall.Service{Memory: provider{hits: a}, Conversation: provider{hits: b}}
		budget := 100 + rng.IntN(14000)
		want, err := s.Search(ctx, scope, q, budget)
		if err != nil {
			t.Fatal(err)
		}
		candidates, err := s.Collect(ctx, scope, q)
		if err != nil {
			t.Fatal(err)
		}
		got, err := recall.SelectManaged(ctx, q, candidates, budget, selectionPolicy)
		if err != nil {
			t.Fatal(trial, err)
		}
		w, _ := json.Marshal(want)
		g, _ := json.Marshal(got)
		if string(w) != string(g) {
			t.Fatalf("trial %d selection/provenance changed\n%s\n%s", trial, w, g)
		}
	}
}
func TestRecallPluginCannotInventSourcesRewriteTextOrForgeProof(t *testing.T) {
	c := []recall.Candidate{{Hit: recall.Hit{Content: "真实 evidence", SourceRef: "original", SourceType: "conversation", Evidence: []recall.Evidence{{Quote: "exact quote"}}}}}
	for _, p := range [][]recall.Selection{
		{}, {{Index: 1, Length: 3}}, {{Index: 0, Offset: 999, Length: 1}}, {{Index: 0, Length: 1}}, {{Index: 0, Length: len(c[0].Hit.Content), Evidence: []int{1}}}, {{Index: 0, Length: len(c[0].Hit.Content), Evidence: []int{0, 0}}},
	} {
		_, err := recall.SelectManaged(context.Background(), recall.Query{Query: "evidence", ExcerptBytes: 100}, c, 4096, func(context.Context, recall.Query, []recall.Candidate) ([]recall.Selection, error) { return p, nil })
		if err == nil {
			t.Fatal("forged source/window/proof accepted", p)
		}
	}
}

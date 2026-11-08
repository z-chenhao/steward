package sqlite

import (
	"context"
	"errors"
	"fmt"
	"github.com/z-chenhao/assistant/internal/memory"
	"github.com/z-chenhao/assistant/internal/recall"
	"strings"
	"testing"
	"time"
)

func TestMemoryDocumentPruningDropsHistoricalSupportButForgetKeepsLineage(t *testing.T) {
	s, scope := semanticFixture(t)
	ctx := context.Background()
	sources := []memory.SourceRef{}
	for i := 0; i < 256; i++ {
		sources = append(sources, memory.SourceRef{Type: "conversation", Ref: fmt.Sprintf("conversation:source-%03d", i)})
	}
	d, e := s.WriteDocument(ctx, scope, 0, "old-head", "old-head", time.Now(), sources, memory.DocumentWrite{Layer: memory.Injected, Title: "Index", Content: "An old index containing many topics."})
	if e != nil {
		t.Fatal(e)
	}
	updated, e := s.WriteDocument(ctx, scope, 0, "pruned-head", "pruned-head", time.Now(), []memory.SourceRef{{Type: "conversation", Ref: "conversation:new-correction"}}, memory.DocumentWrite{Ref: d.Ref, Revision: 1, Layer: memory.Injected, Title: "Index", Content: "Pruned index: only the new corrected topic remains."})
	if e != nil {
		t.Fatal("pruned head could not be updated", e)
	}
	current, e := s.ReadDocument(ctx, scope, 0, updated.Ref)
	if e != nil || len(current.Sources) != 1 {
		t.Fatal(current, e)
	}
	if e = s.Forget(ctx, scope, "old-head", time.Now()); e != nil {
		t.Fatal(e)
	}
	if _, e = s.ReadDocument(ctx, scope, 0, updated.Ref); !errors.Is(e, recall.ErrConflict) {
		t.Fatal("Forget failed to revoke successor", e)
	}
}

func TestMemoryDreamDocumentRetainsSemanticCorrectionIdentity(t *testing.T) {
	s, scope := semanticFixture(t)
	ctx := context.Background()
	at := time.Now().Add(-time.Hour)
	quote := "我喜欢无糖绿茶"
	src := memory.SourceRef{Type: "conversation", Ref: "conversation:old-pref"}
	in := memory.CurationInput{Observation: memory.Observation{EventID: "old-pref", Source: src, Author: "user", Text: quote, At: at}}
	if e := s.MergeSemantic(ctx, scope, in, memory.Proposal{Facts: []memory.Candidate{{Operation: "add", Facet: "drinks.preference", Category: "preferences", Content: "用户偏好无糖绿茶", Quote: quote}}}); e != nil {
		t.Fatal(e)
	}
	facts, e := s.SemanticContext(ctx, scope, 0)
	if e != nil || len(facts) != 1 {
		t.Fatal(facts, e)
	}
	doc, e := s.WriteDocument(ctx, scope, 0, "dream:rewrite", "dream-note", time.Now().Add(-time.Minute), []memory.SourceRef{src}, memory.DocumentWrite{Ref: facts[0].Ref, Revision: 1, Layer: memory.Notes, Title: "当前饮品偏好", Content: "用户当前偏好无糖绿茶"})
	if e != nil {
		t.Fatal(e)
	}
	candidates, e := s.SemanticCandidates(ctx, scope, 0, "我现在更喜欢红茶")
	if e != nil {
		t.Fatal(e)
	}
	if len(candidates) != 1 || candidates[0].Ref != doc.Ref {
		t.Fatal("converted fact missing from correction candidates", candidates)
	}
	fresh := memory.CurationInput{Observation: memory.Observation{EventID: "new-pref", Source: memory.SourceRef{Type: "conversation", Ref: "conversation:new-pref"}, Author: "user", Text: "我现在更喜欢红茶", At: time.Now()}, Existing: candidates}
	if e = s.MergeSemantic(ctx, scope, fresh, memory.Proposal{Facts: []memory.Candidate{{Operation: "replace", Target: doc.Ref, Facet: "drinks.preference", Category: "preferences", Content: "用户偏好红茶", Quote: fresh.Observation.Text}}}); e != nil {
		t.Fatal(e)
	}
	if _, e = s.ReadDocument(ctx, scope, 0, doc.Ref); !errors.Is(e, recall.ErrConflict) {
		t.Fatal("old preference remains current", e)
	}
	green, e := s.Search(ctx, scope, recall.Query{Query: "绿茶"})
	if e != nil {
		t.Fatal(e)
	}
	red, e := s.Search(ctx, scope, recall.Query{Query: "红茶"})
	if e != nil {
		t.Fatal(e)
	}
	if len(green) != 0 || len(red) != 1 {
		t.Fatal("conflicting current preferences", green, red)
	}
}

func TestMemoryRepairsPreFixDocumentFactSuccessorWithoutRewritingOriginal(t *testing.T) {
	s, scope := semanticFixture(t)
	ctx := context.Background()
	at := time.Now().Add(-time.Hour)
	quote := "我喜欢无糖绿茶"
	in := memory.CurationInput{Observation: memory.Observation{EventID: "repair-original", Source: memory.SourceRef{Type: "conversation", Ref: "conversation:repair-original"}, Author: "user", Text: quote, At: at}}
	if e := s.MergeSemantic(ctx, scope, in, memory.Proposal{Facts: []memory.Candidate{{Operation: "add", Facet: "drinks.preference", Category: "preferences", Content: quote, Quote: quote}}}); e != nil {
		t.Fatal(e)
	}
	facts, _ := s.SemanticContext(ctx, scope, 0)
	old := facts[0].Ref
	doc, e := s.WriteDocument(ctx, scope, 0, "dream:old-bug", "old-bug", time.Now(), []memory.SourceRef{in.Observation.Source}, memory.DocumentWrite{Ref: old, Revision: 1, Layer: memory.Notes, Title: "Drink", Content: quote})
	if e != nil {
		t.Fatal(e)
	}
	// Reconstruct the deployed pre-fix shape: document has no semantic metadata,
	// and the fact head still names its superseded predecessor.
	if _, e = s.db.Exec(`DELETE FROM memory_semantic_record WHERE memory_id='old-bug'; UPDATE memory_fact SET memory_id=?`, strings.TrimPrefix(old, "memory:")); e != nil {
		t.Fatal(e)
	}
	var sealed string
	s.db.QueryRow(`SELECT content FROM memory_record WHERE memory_id='old-bug'`).Scan(&sealed)
	if e = s.ConfigureVault(s.vault); e != nil {
		t.Fatal(e)
	}
	facts, e = s.SemanticCandidates(ctx, scope, 0, quote)
	if e != nil || len(facts) != 1 || facts[0].Ref != doc.Ref || facts[0].Facet != "drinks.preference" {
		t.Fatal(facts, e)
	}
	var after string
	s.db.QueryRow(`SELECT content FROM memory_record WHERE memory_id='old-bug'`).Scan(&after)
	if sealed != after {
		t.Fatal("repair rewrote immutable body")
	}
	if e = s.ConfigureVault(s.vault); e != nil {
		t.Fatal(e)
	}
	var n int
	s.db.QueryRow(`SELECT count(*) FROM memory_document_receipt`).Scan(&n)
	if n != 1 {
		t.Fatal("repair duplicated document receipts")
	}
}

package sqlite

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/z-chenhao/assistant/internal/conversation"
	"github.com/z-chenhao/assistant/internal/llm"
	"github.com/z-chenhao/assistant/internal/memory"
	"github.com/z-chenhao/assistant/internal/recall"
	"github.com/z-chenhao/assistant/internal/tool"
)

func TestMemoryDreamSelectsRelevantEvidenceAndSkipsIdenticalRevision(t *testing.T) {
	s, r, store, actor, p := semanticRuntime(t)
	ctx := context.Background()
	quote := "CSV import verified: UTF-8 without BOM, draft only."
	receipt := mustSubmit(t, actor, "verified", quote)
	var raw string
	if e := s.db.QueryRow(`SELECT message_json FROM conversation_intent WHERE message_id=?`, receipt.MessageID).Scan(&raw); e != nil {
		t.Fatal(e)
	}
	var original conversation.Message
	if e := json.Unmarshal([]byte(raw), &original); e != nil {
		t.Fatal(e)
	}
	scope := recall.Scope{UserID: "local", AgentID: mustOwner(t, s)}
	sources := []memory.SourceRef{{Type: "conversation", Ref: "conversation:" + receipt.MessageID}}
	proof := []recall.Evidence{{SourceRef: sources[0].Ref, Author: "user", At: original.CreatedAt, Quote: quote}}
	doc, e := store.WriteDocument(ctx, scope, 0, "verified-doc", "verified-doc", time.Now(), sources, memory.DocumentWrite{Layer: memory.Notes, Title: "CSV import", Content: quote, Evidence: proof})
	if e != nil {
		t.Fatal(e)
	}
	doc, e = store.ReadDocument(ctx, scope, 0, doc.Ref)
	if e != nil {
		t.Fatal(e)
	}
	for i := 0; i < 20; i++ {
		key := fmt.Sprintf("weather-%d", i)
		if _, e = store.WriteDocument(ctx, scope, 0, key, key, time.Now(), sources, memory.DocumentWrite{Layer: memory.Notes, Title: key, Content: strings.Repeat("unrelated weather forecast ", 20)}); e != nil {
			t.Fatal(e)
		}
	}
	p.reply = func(in memory.CurationInput) llm.GenerationResult {
		if in.Mode != "dream" {
			return llm.GenerationResult{Text: `{"facts":[]}`}
		}
		if len(in.Documents) > 4 {
			t.Fatal("unrelated recent documents consumed model context", len(in.Documents))
		}
		found := false
		for _, d := range in.Documents {
			found = found || d.Ref == tool.ObjectID(doc.Ref)
		}
		if !found {
			t.Fatal("relevant older note omitted")
		}
		proposal := memory.Proposal{Done: true, Documents: []memory.DocumentWrite{{Ref: doc.Ref, Revision: doc.Revision, Layer: doc.Layer, Title: doc.Title, Content: doc.Content, Support: []memory.SupportQuote{{Observation: 0, Quote: quote}}}}}
		body, _ := json.Marshal(proposal)
		return llm.GenerationResult{Text: string(body), Usage: &llm.TokenUsage{InputTokens: 80, OutputTokens: 30}}
	}
	if _, e = r.ManageMemory(ctx, "targeted", memory.Mutation{Operation: "dream", Content: "CSV import"}); e != nil {
		t.Fatal(e)
	}
	if e = r.CurateMemory(ctx); e != nil {
		t.Fatal(e)
	}
	if scalar(t, s, `SELECT count(*) FROM memory_dream_change WHERE operation='unchanged'`) != 1 || scalar(t, s, `SELECT count(*) FROM memory_dream_change WHERE operation='write'`) != 0 {
		t.Fatal("unchanged revision rewritten")
	}
	status, e := r.MemoryStatus(ctx)
	if e != nil {
		t.Fatal(e)
	}
	found := false
	for _, item := range status {
		m := item["metrics"].(memory.MaintenanceMetrics)
		found = found || m.UnchangedWrites == 1 && m.Generations == 1 && m.KnownUsageGenerations == 1 && m.InputTokens == 80
	}
	if !found {
		t.Fatal("missing honest maintenance metrics", status)
	}
}

func TestMemoryVerifiedFeedbackCorrectsHypothesisForSubsequentSearch(t *testing.T) {
	s, r, store, actor, p := semanticRuntime(t)
	ctx := context.Background()
	scope := recall.Scope{UserID: "local", AgentID: mustOwner(t, s)}
	doc, e := store.WriteDocument(ctx, scope, 0, "hypothesis", "hypothesis", time.Now(), []memory.SourceRef{{Type: "feedback", Ref: "feedback:experiment"}}, memory.DocumentWrite{Layer: memory.Notes, Title: "CSV import lesson", Content: "Tentative, untested hypothesis: change the separator."})
	if e != nil {
		t.Fatal(e)
	}
	quote := "CSV import experiment passed with UTF-8 without BOM; changing the separator failed."
	mustSubmit(t, actor, "experiment-feedback", quote)
	p.reply = func(in memory.CurationInput) llm.GenerationResult {
		if in.Mode != "dream" {
			return llm.GenerationResult{Text: `{"facts":[]}`}
		}
		proposal := memory.Proposal{Done: true, Documents: []memory.DocumentWrite{{Ref: doc.Ref, Revision: doc.Revision, Layer: doc.Layer, Title: doc.Title, Content: "Verified CSV import rule: UTF-8 without BOM. Separator hypothesis disproved.", Support: []memory.SupportQuote{{Observation: 0, Quote: quote}}}}}
		raw, _ := json.Marshal(proposal)
		return llm.GenerationResult{Text: string(raw), Usage: &llm.TokenUsage{InputTokens: 80, OutputTokens: 30}}
	}
	if _, e = r.ManageMemory(ctx, "correct", memory.Mutation{Operation: "dream", Content: "Correct CSV import against experiment feedback"}); e != nil {
		t.Fatal(e)
	}
	if e = r.CurateMemory(ctx); e != nil {
		t.Fatal(e)
	}
	hits, e := store.Search(ctx, scope, recall.Query{Query: "CSV import verified", EvidenceFirst: true})
	if e != nil || len(hits) != 1 || !strings.Contains(hits[0].Content, "without BOM") || hits[0].SourceRef == doc.Ref || len(hits[0].Evidence) != 1 || hits[0].Evidence[0].Author != "user" {
		t.Fatal(hits, e)
	}
	if _, e = store.ReadDocument(ctx, scope, 0, doc.Ref); e == nil {
		t.Fatal("disproved hypothesis still current")
	}
	if e = r.CurateMemory(ctx); e != nil {
		t.Fatal(e)
	}
	if scalar(t, s, `SELECT count(*) FROM memory_dream_change WHERE operation='write'`) != 1 {
		t.Fatal("replay duplicated correction")
	}
}

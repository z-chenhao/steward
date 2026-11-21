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

func TestMemoryDreamWikiCorrectionKeepsCurrentBudgetAndOriginalDateEvidence(t *testing.T) {
	ctx := context.Background()
	s, r, _, actor, curator := semanticRuntime(t)
	installCuratorPlugin(t, s, r)
	registry, err := tool.NewRegistry([]tool.Definition{memory.Definition()})
	if err != nil {
		t.Fatal(err)
	}
	r.registry, actor.Registry = registry, registry
	main := actor.Provider.(*loopProvider)
	main.respond = func(llm.GenerationRequest) (llm.GenerationResult, error) {
		return llm.GenerationResult{Text: "Received."}, nil
	}

	const oldBudget = "Atlas budget is 180,000."
	const newBudget = "Atlas budget is now 210,000."
	const deadline = "Atlas delivery is December 12."
	const wikiTitle = "Atlas project"
	curator.reply = func(in memory.CurationInput) llm.GenerationResult {
		if in.Mode != "dream" {
			return llm.GenerationResult{Text: `{"facts":[]}`}
		}
		heads := map[string]memory.Document{}
		for _, doc := range in.Documents {
			heads[doc.Title] = doc
		}
		note := func(title, quote string) memory.DocumentWrite {
			for i, obs := range in.Observations {
				if obs.Author == "user" && strings.Contains(obs.Text, quote) {
					head := heads[title]
					return memory.DocumentWrite{Layer: memory.Notes, Ref: head.Ref, Revision: head.Revision, Title: title, Content: quote, Support: []memory.SupportQuote{{Observation: i, Quote: quote}}}
				}
			}
			t.Fatalf("missing original observation %q", quote)
			return memory.DocumentWrite{}
		}
		wiki := func(current string) memory.DocumentWrite {
			head := heads[wikiTitle]
			return memory.DocumentWrite{Layer: memory.Representations, Ref: head.Ref, Revision: head.Revision, Title: wikiTitle,
				Content: current + "\n" + deadline, Links: []string{heads["Atlas budget"].Ref, heads["Atlas delivery"].Ref}}
		}
		proposal := memory.Proposal{Done: true}
		switch {
		case in.Goal == "Atlas baseline" && in.Pass == 0:
			proposal.Documents = []memory.DocumentWrite{note("Atlas budget", oldBudget), note("Atlas delivery", deadline)}
			proposal.Done = false
		case in.Goal == "Atlas baseline":
			proposal.Documents = []memory.DocumentWrite{wiki(oldBudget)}
		case in.Goal == "Atlas budget correction":
			// The wiki depends on both notes, only the budget note is revised.
			proposal.Documents = []memory.DocumentWrite{wiki(newBudget), note("Atlas budget", newBudget)}
		default:
			t.Fatalf("unexpected maintenance goal %q", in.Goal)
		}
		raw, err := json.Marshal(proposal)
		if err != nil {
			t.Fatal(err)
		}
		return llm.GenerationResult{Text: string(raw)}
	}
	dream := func(goal string) {
		t.Helper()
		if _, err := r.ManageMemory(ctx, goal, memory.Mutation{Operation: "dream", Content: goal}); err != nil {
			t.Fatal(err)
		}
		if err := r.CurateMemory(ctx); err != nil {
			t.Fatal(err)
		}
	}
	mustSubmit(t, actor, "original-budget", oldBudget)
	dateInput := mustSubmit(t, actor, "original-delivery", deadline)
	mustDrain(t, actor)
	dream("Atlas baseline")
	correction := mustSubmit(t, actor, "correct-budget", newBudget)
	mustDrain(t, actor)
	dream("Atlas budget correction")
	if scalar(t, s, `SELECT count(*) FROM memory_mutation WHERE source_type='dream' AND failed_at IS NOT NULL`) != 0 {
		t.Fatal("accepted budget correction failed before publishing the updated wiki")
	}

	// A new user input opens the current wiki through the actual model tools.
	var wikiRef string
	var wikiRead bool
	main.respond = func(req llm.GenerationRequest) (llm.GenerationResult, error) {
		messages, err := llm.ResolveMemoryMessages(ctx, req)
		if err != nil {
			return llm.GenerationResult{}, err
		}
		last := messages[len(messages)-1]
		switch last.ToolCallID {
		case "list-wiki":
			var list struct {
				Documents []memory.Document `json:"documents"`
			}
			if json.Unmarshal([]byte(last.Text), &list) != nil || len(list.Documents) != 1 || list.Documents[0].Revision != 2 {
				return llm.GenerationResult{}, fmt.Errorf("current project revision unavailable: %s", last.Text)
			}
			wikiRef = list.Documents[0].Ref
			args, _ := json.Marshal(memory.Mutation{Operation: "read", MemoryRef: wikiRef})
			return llm.GenerationResult{ToolCalls: []llm.ToolCall{{ID: "read-wiki", Name: memory.ManageName, Arguments: string(args)}}}, nil
		case "read-wiki":
			var doc memory.Document
			if json.Unmarshal([]byte(last.Text), &doc) != nil || doc.Content != newBudget+"\n"+deadline {
				return llm.GenerationResult{}, fmt.Errorf("wrong revision content: %s", last.Text)
			}
			want := map[string]string{dateInput.MessageID: deadline, correction.MessageID: newBudget}
			if len(doc.Sources) != 2 || len(doc.Evidence) != 2 {
				return llm.GenerationResult{}, fmt.Errorf("current wiki cites superseded or missing sources: %+v / %+v", doc.Sources, doc.Evidence)
			}
			for _, src := range doc.Sources {
				if src.Type != "conversation" || want[src.Ref] == "" {
					return llm.GenerationResult{}, fmt.Errorf("unexpected wiki source: %+v", src)
				}
			}
			for _, ev := range doc.Evidence {
				if want[ev.SourceRef] != ev.Quote || ev.Author != "user" || ev.At.IsZero() {
					return llm.GenerationResult{}, fmt.Errorf("wrong original attribution: %+v", ev)
				}
				delete(want, ev.SourceRef)
			}
			if len(want) != 0 {
				return llm.GenerationResult{}, fmt.Errorf("missing original evidence: %v", want)
			}
			wikiRead = true
			return llm.GenerationResult{Text: "Checked the project wiki."}, nil
		default:
			return llm.GenerationResult{ToolCalls: []llm.ToolCall{{ID: "list-wiki", Name: memory.ManageName, Arguments: `{"operation":"list","layer":"representations"}`}}}, nil
		}
	}
	mustSubmit(t, actor, "ask-again", "Check the budget and delivery date in the Atlas project wiki.")
	mustDrain(t, actor)
	if !wikiRead {
		t.Fatal("the next input did not read the corrected wiki")
	}
	if _, err := r.ManageMemory(ctx, "forget-project", memory.Mutation{Operation: "forget", Category: "all"}); err != nil {
		t.Fatal(err)
	}
	for _, request := range []memory.Mutation{
		{Operation: "read", MemoryRef: wikiRef},
		{Operation: "read", Layer: memory.Transcripts, MemoryRef: dateInput.MessageID},
		{Operation: "read", Layer: memory.Transcripts, MemoryRef: correction.MessageID},
	} {
		if _, err := r.ManageMemory(ctx, "revoked-"+request.MemoryRef, request); err == nil {
			t.Fatal("read after Forget", request)
		}
	}
}

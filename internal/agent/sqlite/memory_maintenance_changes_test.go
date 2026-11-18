package sqlite

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/z-chenhao/assistant/internal/llm"
	"github.com/z-chenhao/assistant/internal/memory"
	"github.com/z-chenhao/assistant/internal/recall"
)

func TestMemoryInspectionDreamChangesAndLateForget(t *testing.T) {
	s, r, vault, actor, provider := semanticRuntime(t)
	ctx := context.Background()
	scope := recall.Scope{UserID: "local", AgentID: mustOwner(t, s)}
	start := time.Now().Add(-time.Minute)
	prior := func(id string) memory.Document {
		doc, err := vault.WriteDocument(ctx, scope, 0, "seed-"+id, id, start, []memory.SourceRef{{Type: "feedback", Ref: "feedback:" + id}}, memory.DocumentWrite{Layer: memory.Notes, Title: id, Content: "original plan for " + id})
		if err != nil {
			t.Fatal(err)
		}
		return doc
	}
	old, obsolete := prior("old-plan"), prior("obsolete-plan")
	const quote = "Meet at 2 pm in the east room."
	mustSubmit(t, actor, "meeting-confirmation", quote)
	provider.reply = func(in memory.CurationInput) llm.GenerationResult {
		if in.Mode != "dream" {
			return llm.GenerationResult{Text: `{"facts":[]}`}
		}
		support := []memory.SupportQuote{{Observation: 0, Quote: quote}}
		proposal := memory.Proposal{Done: true, Documents: []memory.DocumentWrite{
			{Layer: memory.Notes, Title: "Time", Content: quote, Support: support},
			{Ref: old.Ref, Layer: old.Layer, Revision: old.Revision, Title: "Meeting", Content: quote, Support: support},
			{Ref: obsolete.Ref, Layer: obsolete.Layer, Revision: obsolete.Revision, Retire: true},
		}}
		raw, _ := json.Marshal(proposal)
		return llm.GenerationResult{Text: string(raw)}
	}
	if _, err := r.ManageMemory(ctx, "review-meeting", memory.Mutation{Operation: "dream"}); err != nil {
		t.Fatal(err)
	}
	if err := r.CurateMemory(ctx); err != nil {
		t.Fatal(err)
	}
	list, err := r.InspectMemory(ctx, memory.InspectionQuery{View: "maintenance"}, vault)
	if err != nil || len(list.Maintenance) != 1 || len(list.Maintenance[0].Changes) != 0 {
		t.Fatal(list, err)
	}
	q := memory.InspectionQuery{View: "maintenance", Ref: strings.Replace(list.Maintenance[0].Ref, "memory-event:", "memory:maintenance-", 1)}
	read := func() []memory.MaintenanceChange {
		page, err := r.InspectMemory(ctx, q, vault)
		if err != nil || len(page.Maintenance) != 1 {
			t.Fatal(page, err)
		}
		return page.Maintenance[0].Changes
	}
	changes := read()
	if len(changes) != 3 || changes[0].Operation != "created" || changes[1].Operation != "replaced" || changes[1].Ref == old.Ref || changes[2] != (memory.MaintenanceChange{Ref: obsolete.Ref, Operation: "retired"}) {
		t.Fatal(changes)
	}
	for _, c := range changes {
		doc, err := r.InspectMemory(ctx, memory.InspectionQuery{Ref: c.Ref}, vault)
		if err != nil || len(doc.Items) != 1 || doc.Items[0].Ref != c.Ref {
			t.Fatal(c, doc, err)
		}
	}
	s.MainBoundary = func(point string) {
		if point == "after_memory_maintenance_inspection" {
			if err := vault.Forget(ctx, scope, strings.TrimPrefix(changes[0].Ref, "memory:"), time.Now()); err != nil {
				t.Fatal(err)
			}
		}
	}
	stale, err := r.InspectMemory(ctx, q, vault)
	s.MainBoundary = nil
	if !errors.Is(err, memory.ErrInspectionUnavailable) || len(stale.Maintenance) != 0 {
		t.Fatal(stale, err)
	}
	for _, item := range read() {
		if item.Ref == changes[0].Ref {
			t.Fatal("forgotten ref disclosed", item)
		}
	}
	if _, err := r.ManageMemory(ctx, "forget-all", memory.Mutation{Operation: "forget", Category: "all"}); err != nil {
		t.Fatal(err)
	}
	if changes := read(); len(changes) != 0 {
		t.Fatal("old-epoch identities", changes)
	}
}

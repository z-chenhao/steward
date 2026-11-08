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
)

func TestMemoryDreamFailureDiagnosticsAndSuccessfulRetry(t *testing.T) {
	s, r, _, a, p := semanticRuntime(t)
	ctx := context.Background()
	mustSubmit(t, a, "diagnostic-source", "请你研究 PRIVATE_DIAGNOSTIC_CANARY 的方法")
	if err := r.Flush(ctx); err != nil {
		t.Fatal(err)
	}
	p.reply = func(in memory.CurationInput) llm.GenerationResult {
		if p.calls > 1 {
			return llm.GenerationResult{Text: `{"facts":[],"documents":[],"done":true}`}
		}
		raw, _ := json.Marshal(memory.Proposal{Done: true, Documents: []memory.DocumentWrite{{Layer: memory.Notes, Title: "Private", Content: "PRIVATE_DIAGNOSTIC_CANARY", Support: []memory.SupportQuote{{Observation: 0, Quote: "NOT_IN_ORIGINAL"}}}}})
		return llm.GenerationResult{Text: string(raw)}
	}
	p.beforeReply = func(_ context.Context, req llm.GenerationRequest) {
		if p.calls > 1 && !strings.Contains(req.Messages[0].Text, "Supporting quote is empty or is not an exact substring") {
			t.Fatal("retry lost host validation feedback")
		}
	}
	if _, err := r.ManageMemory(ctx, "diagnostic-dream", memory.Mutation{Operation: "dream"}); err != nil {
		t.Fatal(err)
	}
	if err := r.CurateMemory(ctx); !errors.Is(err, memory.ErrProposal) {
		t.Fatal(err)
	}
	page, err := r.InspectMemory(ctx, memory.InspectionQuery{View: "maintenance"}, nil)
	if err != nil || len(page.Maintenance) != 1 {
		t.Fatal(page, err)
	}
	ref := "memory:maintenance-" + strings.TrimPrefix(page.Maintenance[0].Ref, "memory-event:")
	read := func() memory.MaintenanceRecord {
		page, err := r.InspectMemory(ctx, memory.InspectionQuery{View: "maintenance", Ref: ref}, nil)
		if err != nil || len(page.Maintenance) != 1 {
			t.Fatal(page, err)
		}
		return page.Maintenance[0]
	}
	failed := read()
	if len(failed.Generations) != 1 || !strings.Contains(failed.Generations[0].Failure, "exact substring") {
		t.Fatal("missing safe failure detail", failed)
	}
	raw, _ := json.Marshal(failed)
	if strings.Contains(string(raw), "PRIVATE_DIAGNOSTIC_CANARY") || strings.Contains(string(raw), "NOT_IN_ORIGINAL") {
		t.Fatal("inspection leaked proposal or control payload")
	}
	if err := r.CurateMemory(ctx); err != nil {
		t.Fatal(err)
	}
	applied := read()
	if applied.Status != "applied" || applied.Error != "" || len(applied.Generations) != 2 || applied.Generations[1].Failure == "" {
		t.Fatal("successful retry lost history or retained current error", applied)
	}
	if scalar(t, s, `SELECT count(*) FROM memory_mutation WHERE last_error IS NOT NULL`) != 0 {
		t.Fatal("success persisted stale error")
	}
	// Revoke between decryption and the final owner/epoch check.
	s.MainBoundary = func(point string) {
		if point != "after_memory_maintenance_inspection" {
			return
		}
		if _, err := s.db.Exec(`INSERT INTO memory_privacy(agent_id,epoch,updated_at) VALUES(?,1,?) ON CONFLICT(agent_id) DO UPDATE SET epoch=epoch+1,updated_at=excluded.updated_at`, mustOwner(t, s), time.Now().UnixMicro()); err != nil {
			t.Fatal(err)
		}
	}
	stale, err := r.InspectMemory(ctx, memory.InspectionQuery{View: "maintenance", Ref: ref}, nil)
	if !errors.Is(err, memory.ErrInspectionNotFound) || len(stale.Maintenance) != 0 {
		t.Fatal("concurrent privacy change returned stale diagnostics", stale, err)
	}
	s.MainBoundary = nil
	for _, g := range read().Generations {
		if g.Failure != "" {
			t.Fatal("old-epoch diagnostic was decrypted")
		}
	}
}

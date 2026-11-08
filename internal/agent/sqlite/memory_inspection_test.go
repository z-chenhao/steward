package sqlite

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/z-chenhao/assistant/internal/artifact"
	"github.com/z-chenhao/assistant/internal/conversation"
	"github.com/z-chenhao/assistant/internal/memory"
	memorysqlite "github.com/z-chenhao/assistant/internal/memory/sqlite"
	"github.com/z-chenhao/assistant/internal/recall"
)

func TestMemoryInspectionContentSourcesPaginationAndRetiredPrivacy(t *testing.T) {
	ctx := context.Background()
	s, r, history, a, _ := loopFixture(t)
	store, _ := evidenceMemoryVault(t, s)
	owner := idAgent(t, s)
	input := mustSubmit(t, a, "memory-ui-source", "I prefer 茉莉茶 <img src=x onerror=unsafe()>")
	source := "conversation:" + input.MessageID
	if err := history.Append(ctx, []conversation.Message{{MessageID: "foreign-memory-source", ConversationID: "other", Role: "user", Content: conversation.Text("PRIVATE_OTHER_OWNER"), Source: "cli", CreatedAt: time.Now()}}); err != nil {
		t.Fatal(err)
	}
	for i := range 5 {
		fact := evidenceFact(fmt.Sprintf("inspection-%d", i), owner, memory.SourceRef{Type: "conversation", Ref: source}, memory.SourceRef{Type: "conversation", Ref: "conversation:foreign-memory-source"})
		fact.Content = fmt.Sprintf("真实记忆 茉莉茶 %d <img src=x>", i)
		if err := store.Apply(ctx, fact); err != nil {
			t.Fatal(err)
		}
	}
	foreign := evidenceFact("foreign-memory", "other")
	foreign.Content = "PRIVATE_OTHER_OWNER"
	if err := store.Apply(ctx, foreign); err != nil {
		t.Fatal(err)
	}
	q := memory.InspectionQuery{Search: "茉莉茶", Limit: 2}
	seen := map[string]bool{}
	firstCursor := ""
	for {
		page, err := r.InspectMemory(ctx, q, store)
		if err != nil || page.Total != 5 || len(page.Items) > 2 {
			t.Fatal(page, err)
		}
		for _, item := range page.Items {
			if seen[item.Ref] || !strings.Contains(item.Content, "真实记忆") || item.ContentState != "available" || len(item.Sources) != 1 || item.Sources[0].Ref != source {
				t.Fatal(item)
			}
			seen[item.Ref] = true
		}
		if firstCursor == "" {
			firstCursor = page.Next
		}
		q.Cursor = page.Next
		if q.Cursor == "" {
			break
		}
	}
	if len(seen) != 5 {
		t.Fatal(seen)
	}
	if _, err := r.InspectMemory(ctx, memory.InspectionQuery{Cursor: firstCursor, Tier: "core"}, store); !errors.Is(err, memory.ErrInspectionInvalid) {
		t.Fatal("cursor accepted a different query", err)
	}
	detail, err := r.InspectMemory(ctx, memory.InspectionQuery{Ref: "memory:inspection-0", Source: source}, store)
	if err != nil || !strings.Contains(detail.Items[0].Sources[0].Text, "I prefer 茉莉茶") {
		t.Fatal(detail, err)
	}
	if _, err = r.InspectMemory(ctx, memory.InspectionQuery{Ref: "memory:inspection-0", Source: "conversation:foreign-memory-source"}, store); !errors.Is(err, memory.ErrInspectionNotFound) {
		t.Fatal("unrelated source was accepted", err)
	}
	replacement := evidenceFact("inspection-new", owner)
	replacement.Supersedes = "inspection-0"
	if err = store.Apply(ctx, replacement); err != nil {
		t.Fatal(err)
	}
	retired, err := r.InspectMemory(ctx, memory.InspectionQuery{Ref: "memory:inspection-0"}, store)
	if err != nil || len(retired.Items) != 1 || retired.Items[0].Content != "" || retired.Items[0].ContentState != "retired" || retired.Items[0].ReplacedBy != "memory:inspection-new" {
		t.Fatal(retired, err)
	}
	if err = store.Forget(ctx, recall.Scope{UserID: foreign.UserID, AgentID: owner}, "inspection-new", time.Now()); err != nil {
		t.Fatal(err)
	}
	for _, ref := range []string{"memory:inspection-0", "memory:inspection-new", "memory:foreign-memory", "memory:missing"} {
		if _, err = r.InspectMemory(ctx, memory.InspectionQuery{Ref: ref}, store); !errors.Is(err, memory.ErrInspectionNotFound) {
			t.Fatal(ref, err)
		}
	}
}
func TestMemoryInspectionUnavailableKeyAndReadFence(t *testing.T) {
	ctx := context.Background()
	s, r, _, _, _ := loopFixture(t)
	store, _ := evidenceMemoryVault(t, s)
	owner := idAgent(t, s)
	if err := store.Apply(ctx, evidenceFact("sealed-inspection", owner)); err != nil {
		t.Fatal(err)
	}
	keyless, err := memorysqlite.Open(ctx, filepath.Dir(s.path))
	if err != nil {
		t.Fatal(err)
	}
	defer keyless.Close()
	page, err := r.InspectMemory(ctx, memory.InspectionQuery{}, keyless)
	if err != nil || len(page.Items) != 1 || page.Items[0].Content != "" || page.Items[0].ContentState != "unavailable" {
		t.Fatal(page, err)
	}
	if _, err = r.InspectMemory(ctx, memory.InspectionQuery{Search: "evidence"}, keyless); !errors.Is(err, memory.ErrInspectionUnavailable) {
		t.Fatal("search silently omitted unreadable memory", err)
	}
	s.MainBoundary = func(name string) {
		if name == "after_memory_inspection" {
			if _, err := s.db.ExecContext(ctx, `INSERT INTO memory_privacy(agent_id,epoch,updated_at) VALUES(?,1,?) ON CONFLICT(agent_id) DO UPDATE SET epoch=epoch+1,updated_at=excluded.updated_at`, owner, time.Now().UnixMicro()); err != nil {
				t.Fatal(err)
			}
		}
	}
	defer func() { s.MainBoundary = nil }()
	page, err = r.InspectMemory(ctx, memory.InspectionQuery{}, store)
	if !errors.Is(err, memory.ErrInspectionNotFound) || len(page.Items) != 0 {
		t.Fatal("privacy change returned prior content", page, err)
	}
}

func TestMemoryInspectionRechecksConcurrentReplacementAndForget(t *testing.T) {
	for _, change := range []string{"replacement", "forget"} {
		t.Run(change, func(t *testing.T) {
			ctx := context.Background()
			s, r, _, _, _ := loopFixture(t)
			store, _ := evidenceMemoryVault(t, s)
			owner := idAgent(t, s)
			if err := store.Apply(ctx, evidenceFact("concurrent-memory", owner)); err != nil {
				t.Fatal(err)
			}
			s.MainBoundary = func(point string) {
				if point != "after_memory_inspection" {
					return
				}
				var err error
				if change == "replacement" {
					next := evidenceFact("concurrent-new", owner)
					next.Supersedes = "concurrent-memory"
					err = store.Apply(ctx, next)
				} else {
					err = store.Forget(ctx, recall.Scope{UserID: artifact.LocalUser, AgentID: owner}, "concurrent-memory", time.Now())
				}
				if err != nil {
					t.Fatal(err)
				}
			}
			page, err := r.InspectMemory(ctx, memory.InspectionQuery{}, store)
			if !errors.Is(err, memory.ErrInspectionUnavailable) || len(page.Items) != 0 {
				t.Fatal("concurrent mutation returned stale content", page, err)
			}
		})
	}
}
func TestMemoryInspectionMaintenanceIsScopedAndContainsNoPayload(t *testing.T) {
	ctx := context.Background()
	s, r, _, _, _ := loopFixture(t)
	owner := idAgent(t, s)
	at := time.Now().UnixMicro()
	for i, status := range []string{"pending", "applied", "rejected"} {
		var applied, failed any
		if status == "applied" {
			applied = at
		}
		if status == "rejected" {
			failed = at
		}
		_, err := s.db.ExecContext(ctx, `INSERT INTO memory_mutation(event_id,agent_id,operation,source_type,source_ref,payload,accepted_at,applied_at,failed_at,last_error,attempts) VALUES(?,?,'remember','feedback',?,'PRIVATE_CONTROL_PAYLOAD',?,?,?,'PROVIDER_UNAVAILABLE',1)`, fmt.Sprintf("event-%d", i), owner, fmt.Sprintf("control:%d", i), at+int64(i), applied, failed)
		if err != nil {
			t.Fatal(err)
		}
	}
	if _, err := s.db.ExecContext(ctx, `INSERT INTO memory_mutation(event_id,agent_id,operation,source_type,source_ref,payload,accepted_at) VALUES('foreign-event','another-agent','forget','feedback','control:foreign','PRIVATE_OTHER_OWNER',?)`, at); err != nil {
		t.Fatal(err)
	}
	page, err := r.InspectMemory(ctx, memory.InspectionQuery{View: "maintenance", Limit: 2}, nil)
	if err != nil || page.Total != 3 || len(page.Maintenance) != 2 || page.Next == "" || page.Maintenance[0].Status != "rejected" || page.Maintenance[1].Status != "applied" {
		t.Fatal(page, err)
	}
	next, err := r.InspectMemory(ctx, memory.InspectionQuery{View: "maintenance", Limit: 2, Cursor: page.Next}, nil)
	if err != nil || len(next.Maintenance) != 1 || next.Maintenance[0].Status != "pending" {
		t.Fatal(next, err)
	}
	if page.Maintenance[1].Error != "" {
		t.Fatal("legacy applied receipt still reports a current error")
	}
	for _, item := range append(page.Maintenance, next.Maintenance...) {
		if strings.Contains(fmt.Sprintf("%+v", item), "PRIVATE_") {
			t.Fatal("maintenance leaked payload", item)
		}
	}
}

func TestMemoryInspectionPagesVisibleSourcesAndUTF8Evidence(t *testing.T) {
	ctx := context.Background()
	s, r, history, _, _ := loopFixture(t)
	store, _ := evidenceMemoryVault(t, s)
	owner := idAgent(t, s)
	refs := []memory.SourceRef{}
	body := strings.Repeat("茉莉茶", 600)
	for i := range 23 {
		id := fmt.Sprintf("memory-many-source-%02d", i)
		if err := history.Append(ctx, []conversation.Message{{MessageID: id, ConversationID: owner, Role: "user", Content: conversation.Text(body), Source: "cli", CreatedAt: time.Now()}}); err != nil {
			t.Fatal(err)
		}
		refs = append(refs, memory.SourceRef{Type: "conversation", Ref: "conversation:" + id})
	}
	if err := store.Apply(ctx, evidenceFact("many-sources", owner, refs...)); err != nil {
		t.Fatal(err)
	}
	page, err := r.InspectMemory(ctx, memory.InspectionQuery{Ref: "memory:many-sources", Source: refs[0].Ref}, store)
	if err != nil {
		t.Fatal(err)
	}
	item := page.Items[0]
	if item.SourceCount != 23 || len(item.Sources) != 20 || item.NextSourcesOffset == nil || *item.NextSourcesOffset != 20 || item.Sources[0].NextOffset == nil {
		t.Fatal(item)
	}
	tail, err := r.InspectMemory(ctx, memory.InspectionQuery{Ref: "memory:many-sources", Source: refs[0].Ref, Offset: *item.Sources[0].NextOffset}, store)
	if err != nil || !strings.Contains(item.Sources[0].Text+tail.Items[0].Sources[0].Text, body) {
		t.Fatal("UTF-8 evidence continuation changed the source", err)
	}
	last, err := r.InspectMemory(ctx, memory.InspectionQuery{Ref: "memory:many-sources", SourcesOffset: *item.NextSourcesOffset, Source: refs[22].Ref}, store)
	if err != nil || len(last.Items[0].Sources) != 3 || last.Items[0].SourceCount != 23 || last.Items[0].NextSourcesOffset != nil {
		t.Fatal(last, err)
	}
	if _, err = r.InspectMemory(ctx, memory.InspectionQuery{Ref: "memory:many-sources", Source: refs[22].Ref}, store); !errors.Is(err, memory.ErrInspectionNotFound) {
		t.Fatal("source selection escaped its page", err)
	}
}

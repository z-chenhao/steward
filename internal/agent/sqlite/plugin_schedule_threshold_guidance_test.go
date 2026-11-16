package sqlite

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/z-chenhao/assistant/internal/agent"
	"github.com/z-chenhao/assistant/internal/memory"
	memorysqlite "github.com/z-chenhao/assistant/internal/memory/sqlite"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/z-chenhao/assistant/internal/automation"
	"github.com/z-chenhao/assistant/internal/llm"
	"github.com/z-chenhao/assistant/internal/rsi"
	"github.com/z-chenhao/assistant/internal/tool"
)

func TestScheduleThresholdGuidanceFreezesRealRootRequestsAcrossLifecycle(t *testing.T) {
	s, r, actor, provider := automationFixture(t)
	ctx := context.Background()
	h := installScheduleOwnerMethod(t, s, r)
	first, err := h.Store.Status(ctx)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2030, 1, 2, 12, 0, 0, 0, time.UTC)
	limit := int64(1800)
	id := createAutomation(t, r, now, &limit)
	phase := 0
	versions := []string{"1.0.1", "1.0.2", "1.0.1"}
	prefixes := map[string][]llm.Message{}
	originalSnapshots := map[string]string{}
	provider.respond = func(req llm.GenerationRequest) (llm.GenerationResult, error) {
		if !isRoot(req) {
			return llm.GenerationResult{Text: "Reported accepted observation"}, nil
		}
		expected := versions[phase]
		var observed *llm.ToolSpec
		for i := range req.Tools {
			if req.Tools[i].Name == automation.ObserveName {
				observed = &req.Tools[i]
			}
		}
		if observed == nil || !strings.HasPrefix(observed.Description, "Observation method "+expected+". ") || observed.InputSchema != automation.ObserveSchema {
			t.Fatal("Root wording or fixed schema drift", observed, expected)
		}
		body, err := json.Marshal(req.Messages)
		if err != nil {
			t.Fatal(err)
		}
		for _, text := range []string{"Root observation method " + expected + ". ", "Automation scheduled occurrence:", "HKG-CDG 2026-12-01 price", "Only entering below 1800"} {
			if !strings.Contains(string(body), text) {
				t.Fatal("Root method replaced protected facts or used another version", text)
			}
		}
		if prefix, ok := prefixes[req.RunID]; ok {
			before, _ := json.Marshal(prefix)
			if len(req.Messages) < len(prefix) {
				t.Fatal("continuation lost original messages")
			}
			after, _ := json.Marshal(req.Messages[:len(prefix)])
			if string(after) != string(before) {
				t.Fatal("tool continuation rewrote original Root prefix")
			}
			return llm.GenerationResult{Text: "Observation checked"}, nil
		}
		prefixes[req.RunID] = append([]llm.Message(nil), req.Messages...)
		var raw string
		if err = s.db.QueryRow(`SELECT c.tools_json FROM run r JOIN capability_snapshot c ON c.snapshot_id=r.capability_snapshot_id WHERE r.run_id=?`, req.RunID).Scan(&raw); err != nil {
			t.Fatal(err)
		}
		var specs []tool.Spec
		if err = json.Unmarshal([]byte(raw), &specs); err != nil {
			t.Fatal(err)
		}
		found := false
		for _, spec := range specs {
			if spec.Name == automation.ObserveName {
				found = true
				if spec.Permission != "automation_observe" || spec.ReplayPolicy != "safe" || spec.Version != 1 || spec.InputSchema != automation.ObserveSchema || spec.ResourceHash == "" {
					t.Fatal("method changed observation authority", spec)
				}
			}
		}
		if !found {
			t.Fatal("original observation capability missing")
		}
		originalSnapshots[req.RunID] = raw
		switch phase {
		case 0:
			installScheduleMethodVersion(t, s, r, "1.0.2", 4)
		case 1:
			current, err := h.Store.Status(ctx)
			if err != nil {
				t.Fatal(err)
			}
			activateScheduleMethod(t, h, "root-guidance-exact-rollback", current.Revision, first.Revision, 2)
		case 2:
			current, err := h.Store.Status(ctx)
			if err != nil {
				t.Fatal(err)
			}
			if _, err = h.Store.Deactivate(ctx, "root-guidance-withdraw", current.Revision); err != nil {
				t.Fatal(err)
			}
		}
		return llm.GenerationResult{ToolCalls: []llm.ToolCall{{ID: "root-observation", Name: automation.ObserveName, Arguments: fmtValue(1800)}}}, nil
	}
	for phase = 0; phase < 3; phase++ {
		day := now.AddDate(0, 0, phase+1)
		r.SetTestAutomationClock(day)
		if err = r.ScheduleAutomations(ctx, day); err != nil {
			t.Fatal(err)
		}
		mustDrain(t, actor)
		if scalar(t, s, `SELECT count(*) FROM automation_fire WHERE observed_value=1800`) != phase+1 {
			t.Fatal("real Root observation missing", phase)
		}
	}
	if len(prefixes) != 3 || scalar(t, s, `SELECT count(*) FROM automation_fire WHERE notify=1`) != 1 {
		t.Fatal("independent inclusive version or original quiet policy lost")
	}
	for run, original := range originalSnapshots {
		var retained string
		if err = s.db.QueryRow(`SELECT c.tools_json FROM run r JOIN capability_snapshot c ON c.snapshot_id=r.capability_snapshot_id WHERE r.run_id=?`, run).Scan(&retained); err != nil || retained != original {
			t.Fatal("lifecycle rewrote frozen capability", run, err)
		}
	}
	// New work after withdrawal must not recover native guidance. The old intent
	// can still be cancelled locally, without selecting or repairing a package.
	day := now.AddDate(0, 0, 4)
	r.SetTestAutomationClock(day)
	if err = r.ScheduleAutomations(ctx, day); err != nil {
		t.Fatal(err)
	}
	if err = r.admitAutomations(ctx); !errors.Is(err, rsi.ErrNotFound) {
		t.Fatal("withdrawn Root guidance used native wording", err)
	}
	if scalar(t, s, `SELECT count(*) FROM run WHERE worker_position='root'`) != 3 {
		t.Fatal("withdrawal admitted replacement Root")
	}
	var version int64
	if err = s.db.QueryRow(`SELECT version FROM automation WHERE automation_id=?`, id).Scan(&version); err != nil {
		t.Fatal(err)
	}
	result, err := r.ManageAutomation(ctx, "root-guidance-local-cancel", automation.Request{Operation: "cancel", Ref: id, Version: version}, day)
	if err != nil || result.Error != nil || scalar(t, s, `SELECT count(*) FROM automation WHERE automation_id=? AND enabled=0`, id) != 1 {
		t.Fatal("local cancellation depended on withdrawn guidance", result, err)
	}
}

func TestScheduleThresholdHistoricalRootRetainsOriginalWordingAfterActivation(t *testing.T) {
	s, r, actor, provider := automationFixture(t)
	ctx := context.Background()
	h := installScheduleMethodVersion(t, s, r, "1.0.0", 2)
	original, err := h.Store.Status(ctx)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2030, 1, 2, 12, 0, 0, 0, time.UTC)
	limit := int64(1800)
	createAutomation(t, r, now, &limit)
	day := now.AddDate(0, 0, 1)
	r.SetTestAutomationClock(day)
	if err = r.ScheduleAutomations(ctx, day); err != nil {
		t.Fatal(err)
	}
	if err = r.admitAutomations(ctx); err != nil {
		t.Fatal(err)
	}
	installScheduleMethodVersion(t, s, r, "1.0.2", 4)
	seen := 0
	provider.respond = func(req llm.GenerationRequest) (llm.GenerationResult, error) {
		if !isRoot(req) {
			return llm.GenerationResult{Text: "Reported"}, nil
		}
		seen++
		body, _ := json.Marshal(req.Messages)
		if strings.Contains(string(body), "Root observation method") || !strings.Contains(string(body), "Only entering below 1800") {
			t.Fatal("historical Root adopted current instruction")
		}
		found := false
		for _, spec := range req.Tools {
			if spec.Name == automation.ObserveName {
				found = true
				if !strings.HasPrefix(spec.Description, "Record an observed integer value") || strings.Contains(spec.Description, "Observation method") {
					t.Fatal("old tool wording followed activation", spec)
				}
			}
		}
		if !found {
			t.Fatal("old observation tool missing")
		}
		if req.Messages[len(req.Messages)-1].Role == "tool" {
			return llm.GenerationResult{Text: "Old observation checked"}, nil
		}
		return llm.GenerationResult{ToolCalls: []llm.ToolCall{{ID: "old-root-observe", Name: automation.ObserveName, Arguments: fmtValue(1800)}}}, nil
	}
	mustDrain(t, actor)
	if seen != 2 || scalar(t, s, `SELECT count(*) FROM automation_fire WHERE observed_value=1800 AND notify=0`) != 1 {
		t.Fatal("old Root followed newly activated inclusive policy", seen)
	}
	if scalar(t, s, `SELECT count(*) FROM run r JOIN rsi_run b USING(run_id) WHERE r.worker_position='root' AND b.graph_revision=? AND NOT EXISTS(SELECT 1 FROM json_each(b.managed_services_json) s WHERE s.value='steward.schedule-threshold/1')`, original.Revision) != 1 {
		t.Fatal("activation rewrote old Root method boundary")
	}
}

func TestScheduleThresholdOldMetadataCannotRestoreNativeGuidanceForNewRoot(t *testing.T) {
	s, r, _, provider := automationFixture(t)
	ctx := context.Background()
	installScheduleOwnerMethod(t, s, r)
	h := installScheduleMethodVersion(t, s, r, "1.0.0", 2)
	selected, err := h.Store.Status(ctx)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2030, 1, 2, 12, 0, 0, 0, time.UTC)
	limit := int64(1800)
	createAutomation(t, r, now, &limit)
	if err = r.ScheduleAutomations(ctx, now.AddDate(0, 0, 1)); err != nil {
		t.Fatal(err)
	}
	if err = r.admitAutomations(ctx); !errors.Is(err, rsi.ErrNotFound) {
		t.Fatal("new Root silently adopted historical native guidance", err)
	}
	if scalar(t, s, `SELECT count(*) FROM task`)+scalar(t, s, `SELECT count(*) FROM run`) != 0 || len(provider.requests) != 0 {
		t.Fatal("missing metadata admitted work or released private input")
	}
	current, err := h.Store.Status(ctx)
	if err != nil || current.Revision != selected.Revision {
		t.Fatal("failed admission repaired original selection", current, err)
	}
}

func TestScheduleThresholdColdRootGuidanceForgetDoesNotResurrectIntent(t *testing.T) {
	s, r, _, provider := automationFixture(t)
	ctx := context.Background()
	installScheduleOwnerMethod(t, s, r)
	now := time.Date(2030, 1, 2, 12, 0, 0, 0, time.UTC)
	limit := int64(1800)
	createAutomation(t, r, now, &limit)
	if err := r.ScheduleAutomations(ctx, now.AddDate(0, 0, 1)); err != nil {
		t.Fatal(err)
	}
	m, err := memorysqlite.Open(ctx, filepath.Dir(s.path))
	if err != nil {
		t.Fatal(err)
	}
	defer m.Close()
	r.ConfigureMemory(m, memory.Vault{})
	r.scheduleGuidance.Lock()
	r.scheduleGuidance.graph = ""
	r.scheduleGuidance.Unlock()
	reached := false
	s.MainBoundary = func(point string) {
		if point == "before_schedule_guidance_retry" && !reached {
			reached = true
			receipt, err := r.ManageMemory(ctx, "root-metadata-forget", memory.Mutation{Operation: "forget", Category: "health"})
			if err != nil || receipt["accepted"] != true {
				t.Fatal(receipt, err)
			}
		}
	}
	err = r.admitAutomations(ctx)
	s.MainBoundary = nil
	if !reached || !errors.Is(err, agent.ErrClaimLost) {
		t.Fatal("cold Root metadata retry did not reject original privacy epoch", reached, err)
	}
	if scalar(t, s, `SELECT count(*) FROM task`)+scalar(t, s, `SELECT count(*) FROM run`)+scalar(t, s, `SELECT count(*) FROM task_instruction`) != 0 || len(provider.requests) != 0 {
		t.Fatal("metadata retry resurrected revoked Root context")
	}
	// First retry after the real privacy change consumes the old fire as skipped.
	if err = r.admitAutomations(ctx); err != nil {
		t.Fatal(err)
	}
	if scalar(t, s, `SELECT count(*) FROM automation_fire WHERE state='skipped'`) != 1 || scalar(t, s, `SELECT count(*) FROM run`) != 0 {
		t.Fatal("revoked intent was not skipped on the dependent retry")
	}
}

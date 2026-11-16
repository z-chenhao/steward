package sqlite

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/z-chenhao/assistant/internal/agent"
	"github.com/z-chenhao/assistant/internal/automation"
	"github.com/z-chenhao/assistant/internal/llm"
	"github.com/z-chenhao/assistant/internal/memory"
	memorysqlite "github.com/z-chenhao/assistant/internal/memory/sqlite"
	"github.com/z-chenhao/assistant/internal/rsi"
	"github.com/z-chenhao/assistant/internal/tool"
	"path/filepath"
)

// A real configured fire admits its Root, freezes capabilities, then accepts
// model-generated observations through the normal claim/ToolInvocation path.
func thresholdExecution(t *testing.T, version string, values ...int64) (*Store, *Runs, rsi.Host, *tools, []tool.Invocation) {
	t.Helper()
	ctx := context.Background()
	s, r, _, provider := automationFixture(t)
	h := installScheduleOwnerMethod(t, s, r)
	if version == "1.0.2" {
		h = installScheduleMethodVersion(t, s, r, version, 4)
	}
	now := time.Date(2030, 1, 2, 12, 0, 0, 0, time.UTC)
	limit := int64(1800)
	createAutomation(t, r, now, &limit)
	if err := r.ScheduleAutomations(ctx, now.AddDate(0, 0, 1)); err != nil {
		t.Fatal(err)
	}
	if err := r.admitAutomations(ctx); err != nil {
		t.Fatal(err)
	}
	var root string
	if err := s.db.QueryRow(`SELECT root_run_id FROM automation_fire WHERE state='admitted'`).Scan(&root); err != nil {
		t.Fatal(err)
	}
	claim, err := r.Claim(ctx, root)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = r.Release(ctx, claim) })
	if err = r.Initialize(ctx, claim); err != nil {
		t.Fatal(err)
	}
	run, err := r.Load(ctx, claim)
	if err != nil {
		t.Fatal(err)
	}
	generation, err := r.Generation(ctx, claim, run)
	if err != nil {
		t.Fatal(err)
	}
	calls := make([]llm.ToolCall, len(values))
	for i, value := range values {
		calls[i] = llm.ToolCall{ID: newID(), Name: automation.ObserveName, Arguments: fmtValue(value)}
	}
	provider.respond = func(llm.GenerationRequest) (llm.GenerationResult, error) {
		return llm.GenerationResult{ToolCalls: calls}, nil
	}
	if _, err = provider.Generate(ctx, generation.Request, nil); err != nil {
		t.Fatal(err)
	}
	store := r.ToolStore(claim).(*tools)
	invs, err := store.Accept(ctx, generation.Request.GenerationID, calls, run.Tools)
	if err != nil {
		t.Fatal(err)
	}
	for i := range invs {
		invs[i], err = store.Prepare(ctx, invs[i].ID)
		if err != nil {
			t.Fatal(err)
		}
		if err = store.Start(ctx, invs[i].ID); err != nil {
			t.Fatal(err)
		}
	}
	return s, r, h, store, invs
}

func TestScheduleThresholdActualObservationAndStoppedReplay(t *testing.T) {
	s, _, h, store, invs := thresholdExecution(t, "1.0.1", 1750, 1750, 1751)
	ctx := context.Background()
	result, err := store.executeAutomation(ctx, invs[0])
	if err != nil || result.Error != nil || scalar(t, s, `SELECT count(*) FROM automation_fire WHERE observed_value=1750 AND notify=1`) != 1 {
		t.Fatal("managed observation not accepted", result, err)
	}
	if err = h.Store.Stop(ctx, true); err != nil {
		t.Fatal(err)
	}
	// First dependent operation after stopping: already accepted same-fire data.
	result, err = store.executeAutomation(ctx, invs[1])
	if err != nil || result.Error != nil || scalar(t, s, `SELECT count(*) FROM event_outbox WHERE type='ai.steward.automation.observed.v1'`) != 1 {
		t.Fatal("accepted observation replay consulted stopped producer", result, err)
	}
	result, err = store.executeAutomation(ctx, invs[2])
	if err != nil || result.Error == nil || result.Error.Code != "NON_DETERMINISTIC_REPLAY" {
		t.Fatal("conflicting observation replay accepted", result, err)
	}
}

func TestScheduleThresholdGuardianBeforeCommitAndImmediateResume(t *testing.T) {
	s, _, h, store, invs := thresholdExecution(t, "1.0.1", 1750)
	ctx := context.Background()
	selected, err := h.Store.Status(ctx)
	if err != nil {
		t.Fatal(err)
	}
	s.MainBoundary = func(point string) {
		if point == "before_automation_observe_commit" {
			if err := h.Store.Stop(ctx, true); err != nil {
				t.Fatal(err)
			}
		}
	}
	_, err = store.executeAutomation(ctx, invs[0])
	s.MainBoundary = nil
	if !errors.Is(err, rsi.ErrRevoked) || scalar(t, s, `SELECT count(*) FROM automation_fire WHERE observed_value IS NOT NULL OR notify=1`) != 0 {
		t.Fatal("Guardian failed to reject prepared transition", err)
	}
	if err = h.Store.Stop(ctx, false); err != nil {
		t.Fatal(err)
	}
	// No select/update/reload repairs the execution after real resume.
	result, err := store.executeAutomation(ctx, invs[0])
	if err != nil || result.Error != nil || scalar(t, s, `SELECT count(*) FROM automation_fire WHERE observed_value=1750 AND notify=1`) != 1 {
		t.Fatal("first resumed observation failed", result, err)
	}
	current, err := h.Store.Status(ctx)
	if err != nil || current.Revision != selected.Revision || scalar(t, s, `SELECT count(*) FROM rsi_run WHERE run_id=? AND graph_revision=?`, store.claim.RunID(), selected.Revision) != 1 {
		t.Fatal("resume changed original Run selection", current, err)
	}
}

func TestScheduleThresholdOriginalRootKeepsMethodAfterUpgrade(t *testing.T) {
	s, r, h, store, invs := thresholdExecution(t, "1.0.1", 1800)
	ctx := context.Background()
	original, err := h.Store.Status(ctx)
	if err != nil {
		t.Fatal(err)
	}
	installScheduleMethodVersion(t, s, r, "1.0.2", 4)
	result, err := store.executeAutomation(ctx, invs[0])
	if err != nil || result.Error != nil || scalar(t, s, `SELECT count(*) FROM automation_fire WHERE observed_value=1800 AND notify=0`) != 1 {
		t.Fatal("old Root followed upgraded inclusive threshold", result, err)
	}
	if scalar(t, s, `SELECT count(*) FROM rsi_run WHERE run_id=? AND graph_revision=? AND EXISTS(SELECT 1 FROM json_each(managed_services_json) WHERE value='steward.schedule-threshold/1')`, store.claim.RunID(), original.Revision) != 1 {
		t.Fatal("original method identity missing")
	}
}

func TestScheduleThresholdIndependentRootKeepsMethodAfterExactRollbackAndDeactivate(t *testing.T) {
	s, _, h, store, invs := thresholdExecution(t, "1.0.2", 1800)
	ctx := context.Background()
	current, err := h.Store.Status(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var original string
	if err = s.db.QueryRow(`SELECT g.revision FROM rsi_graph g,json_each(g.graph_json,'$.bindings') b JOIN rsi_package p ON p.digest=json_extract(b.value,'$.package_digest') WHERE json_extract(p.manifest_json,'$.plugin_id')='steward.schedule' AND json_extract(p.manifest_json,'$.version')='1.0.1'`).Scan(&original); err != nil {
		t.Fatal(err)
	}
	activateScheduleMethod(t, h, "threshold-exact-rollback", current.Revision, original, 2)
	if _, err = h.Store.Deactivate(ctx, "threshold-deactivate", original); err != nil {
		t.Fatal(err)
	}
	// First dependent observation after withdrawal retains its actual v1.0.2 Root.
	result, err := store.executeAutomation(ctx, invs[0])
	if err != nil || result.Error != nil || scalar(t, s, `SELECT count(*) FROM automation_fire WHERE observed_value=1800 AND notify=1`) != 1 {
		t.Fatal("frozen independent threshold fell back to native/original policy", result, err)
	}
	if scalar(t, s, `SELECT count(*) FROM rsi_run WHERE run_id=? AND graph_revision=?`, store.claim.RunID(), current.Revision) != 1 {
		t.Fatal("rollback rewrote retained Root")
	}
}

func TestScheduleThresholdRealForgetBetweenDecisionAndCommit(t *testing.T) {
	s, r, _, store, invs := thresholdExecution(t, "1.0.1", 1750)
	ctx := context.Background()
	m, err := memorysqlite.Open(ctx, filepath.Dir(s.path))
	if err != nil {
		t.Fatal(err)
	}
	defer m.Close()
	r.ConfigureMemory(m, memory.Vault{})
	s.MainBoundary = func(point string) {
		if point == "before_automation_observe_commit" {
			receipt, err := r.ManageMemory(ctx, "threshold-forget", memory.Mutation{Operation: "forget", Category: "health"})
			if err != nil || receipt["accepted"] != true {
				t.Fatal(receipt, err)
			}
		}
	}
	_, err = store.executeAutomation(ctx, invs[0])
	s.MainBoundary = nil
	if !errors.Is(err, agent.ErrClaimLost) || scalar(t, s, `SELECT count(*) FROM automation_fire WHERE observed_value IS NOT NULL OR notify=1`) != 0 {
		t.Fatal("revoked observation committed", err)
	}
}

func TestScheduleThresholdOwnerEditBetweenDecisionAndCommit(t *testing.T) {
	s, r, _, store, invs := thresholdExecution(t, "1.0.1", 1750)
	ctx := context.Background()
	s.MainBoundary = func(point string) {
		if point != "before_automation_observe_commit" {
			return
		}
		var id string
		var version int64
		if err := s.db.QueryRow(`SELECT automation_id,version FROM automation`).Scan(&id, &version); err != nil {
			t.Fatal(err)
		}
		limit := int64(1900)
		result, err := r.ManageAutomation(ctx, "threshold-owner-edit", automation.Request{Operation: "update", Ref: id, Version: version, Instruction: "Updated threshold intent", Type: "cron", Spec: "0 8 * * *", Timezone: "Asia/Shanghai", Below: &limit}, time.Date(2030, 1, 3, 12, 0, 0, 0, time.UTC))
		if err != nil || result.Error != nil {
			t.Fatal(result, err)
		}
	}
	result, err := store.executeAutomation(ctx, invs[0])
	s.MainBoundary = nil
	denied := errors.Is(err, agent.ErrConflict) || err == nil && result.Error != nil && result.Error.Code == "PERMISSION_DENIED"
	if !denied || scalar(t, s, `SELECT count(*) FROM automation_fire WHERE observed_value IS NOT NULL OR notify=1`) != 0 || scalar(t, s, `SELECT count(*) FROM automation WHERE below_value=1900 AND schedule_version=2`) != 1 {
		t.Fatal("stale observation was not denied while retaining the owner edit", result, err)
	}
}

package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/z-chenhao/assistant/internal/agent"
	"github.com/z-chenhao/assistant/internal/automation"
	"github.com/z-chenhao/assistant/internal/memory"
	memorysqlite "github.com/z-chenhao/assistant/internal/memory/sqlite"
	"github.com/z-chenhao/assistant/internal/rsi"
)

func TestAutomationTaskLabelUsesIndependentMethodAndRecoversAfterGuardianResume(t *testing.T) {
	s, r, _, p := automationFixture(t)
	ctx := context.Background()
	now := time.Date(2030, 1, 2, 0, 0, 0, 0, time.UTC)
	id := createAutomation(t, r, now, nil)
	if err := r.ScheduleAutomations(ctx, now.AddDate(0, 0, 1)); err != nil {
		t.Fatal(err)
	}
	h := installTaskPlugin(t, s, r)
	baseline, err := h.Store.Status(ctx)
	if err != nil {
		t.Fatal(err)
	}
	graph, _ := selectTaskVersion(t, h, taskVersionPackage(t, "1.1.0"), "automation-label-upgrade", baseline.Revision, true)
	s.MainBoundary = func(point string) {
		if point == "before_automation_label_commit" {
			if err := h.Store.Stop(ctx, true); err != nil {
				t.Fatal(err)
			}
		}
	}
	err = r.admitAutomations(ctx)
	s.MainBoundary = nil
	if !errors.Is(err, rsi.ErrRevoked) || scalar(t, s, `SELECT count(*) FROM task`) != 0 || scalar(t, s, `SELECT count(*) FROM automation_fire WHERE task_title IS NOT NULL`) != 0 {
		t.Fatal("stopped admission partially accepted title/Task", err)
	}
	if err = h.Store.Stop(ctx, false); err != nil {
		t.Fatal(err)
	}
	// First dependent action after real recovery; no reselect or fixture repair.
	if err = r.admitAutomations(ctx); err != nil {
		t.Fatal(err)
	}
	expected := "[v1.1] HKG-CDG 2026-12-01 price"
	if scalar(t, s, `SELECT count(*) FROM task WHERE title=? AND anchor_task='HKG-CDG 2026-12-01 price'`, expected) != 1 || scalar(t, s, `SELECT count(*) FROM automation_fire WHERE automation_id=? AND task_title=? AND task_label_graph=? AND task_label_managed=1 AND state='admitted'`, id, expected, graph.Revision) != 1 {
		t.Fatal("native title used or original brief rewritten")
	}
	if scalar(t, s, `SELECT count(*) FROM automation_fire f JOIN rsi_run r ON r.run_id=f.root_run_id WHERE f.automation_id=? AND r.graph_revision=?`, id, graph.Revision) != 1 || len(p.requests) != 0 {
		t.Fatal("new Task graph drifted or title invoked model")
	}
	if _, err = s.db.Exec(`UPDATE automation_fire SET task_title='replacement' WHERE automation_id=?`, id); err == nil {
		t.Fatal("accepted title provenance mutated")
	}
	if err = h.Store.Stop(ctx, true); err != nil {
		t.Fatal(err)
	}
	if err = r.admitAutomations(ctx); err != nil || scalar(t, s, `SELECT count(*) FROM task`) != 1 {
		t.Fatal("accepted admission replay consulted stopped producer", err)
	}
	current, err := h.Store.Status(ctx)
	if err != nil || current.Revision != graph.Revision {
		t.Fatal("resume changed original selection", current, err)
	}
}

func TestAutomationTaskLabelActualForgetAfterPreparationSkipsFire(t *testing.T) {
	s, r, _, _ := automationFixture(t)
	ctx := context.Background()
	m, err := memorysqlite.Open(ctx, filepath.Dir(s.path))
	if err != nil {
		t.Fatal(err)
	}
	defer m.Close()
	r.ConfigureMemory(m, memory.Vault{})
	now := time.Date(2030, 1, 2, 0, 0, 0, 0, time.UTC)
	createAutomation(t, r, now, nil)
	if err = r.ScheduleAutomations(ctx, now.AddDate(0, 0, 1)); err != nil {
		t.Fatal(err)
	}
	h := installTaskPlugin(t, s, r)
	s.MainBoundary = func(point string) {
		if point == "before_automation_label_commit" {
			receipt, err := r.ManageMemory(ctx, "automation-label-forget", memory.Mutation{Operation: "forget", Category: "health"})
			if err != nil || receipt["accepted"] != true {
				t.Fatal(receipt, err)
			}
		}
	}
	err = r.admitAutomations(ctx)
	s.MainBoundary = nil
	if err != nil || scalar(t, s, `SELECT count(*) FROM task`) != 0 || scalar(t, s, `SELECT count(*) FROM automation_fire WHERE state='skipped' AND task_title IS NULL`) != 1 {
		t.Fatal("revoked label/Task accepted", err)
	}
	if err = h.Store.Stop(ctx, true); err != nil {
		t.Fatal(err)
	}
	if err = r.admitAutomations(ctx); err != nil {
		t.Fatal("revoked intent released to stopped producer", err)
	}
}

func TestAutomationTaskLabelConcurrentSelectionRejectsUnacceptedResult(t *testing.T) {
	s, r, _, _ := automationFixture(t)
	ctx := context.Background()
	now := time.Date(2030, 1, 2, 0, 0, 0, 0, time.UTC)
	createAutomation(t, r, now, nil)
	if err := r.ScheduleAutomations(ctx, now.AddDate(0, 0, 1)); err != nil {
		t.Fatal(err)
	}
	h := installTaskPlugin(t, s, r)
	baseline, err := h.Store.Status(ctx)
	if err != nil {
		t.Fatal(err)
	}
	upgraded := false
	s.MainBoundary = func(point string) {
		if point == "before_automation_label_commit" && !upgraded {
			selectTaskVersion(t, h, taskVersionPackage(t, "1.1.0"), "automation-label-race-upgrade", baseline.Revision, true)
			upgraded = true
		}
	}
	err = r.admitAutomations(ctx)
	s.MainBoundary = nil
	if !upgraded || !errors.Is(err, agent.ErrConflict) || scalar(t, s, `SELECT count(*) FROM task`) != 0 || scalar(t, s, `SELECT count(*) FROM automation_fire WHERE task_title IS NOT NULL`) != 0 {
		t.Fatal("selection change accepted stale result", err)
	}
	if err = r.admitAutomations(ctx); err != nil || scalar(t, s, `SELECT count(*) FROM task WHERE title LIKE '[v1.1] %'`) != 1 {
		t.Fatal("new pure attempt did not use actual new selection", err)
	}
}

func TestV77AutomationTaskLabelMigrationPreservesHistoricalFire(t *testing.T) {
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	db.SetMaxOpenConns(1)
	ctx := context.Background()
	if err = migrate(ctx, db, migrations[:77]); err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(`INSERT INTO automation(automation_id,agent_id,instruction,trigger_type,trigger_spec,timezone,enabled,created_at,updated_at,version) VALUES('automation:old','owner','original','cron','0 8 * * *','UTC',1,1,1,3); INSERT INTO automation_fire(event_id,automation_id,schedule_version,fire_at,instruction,state) VALUES('old-fire','automation:old',1,100,'original fire','pending')`); err != nil {
		t.Fatal(err)
	}
	if err = migrate(ctx, db, migrations); err != nil {
		t.Fatal(err)
	}
	var count int
	if err = db.QueryRow(`SELECT count(*) FROM automation_fire WHERE event_id='old-fire' AND instruction='original fire' AND fire_at=100 AND state='pending' AND task_title IS NULL AND task_label_graph IS NULL AND task_label_managed IS NULL`).Scan(&count); err != nil || count != 1 {
		t.Fatal("migration invented historical title provenance", count, err)
	}
}

func TestAutomationTaskLabelDeactivationRejectsNewTitleButAllowsCancellation(t *testing.T) {
	s, r, _, _ := automationFixture(t)
	ctx := context.Background()
	h := installTaskPlugin(t, s, r)
	selected, err := h.Store.Status(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = h.Store.Deactivate(ctx, "automation-title-deactivate", selected.Revision); err != nil {
		t.Fatal(err)
	}
	now := time.Date(2030, 1, 2, 0, 0, 0, 0, time.UTC)
	id := createAutomation(t, r, now, nil)
	if err = r.ScheduleAutomations(ctx, now.AddDate(0, 0, 1)); err != nil {
		t.Fatal(err)
	}
	if err = r.admitAutomations(ctx); !errors.Is(err, rsi.ErrNotFound) || scalar(t, s, `SELECT count(*) FROM task`) != 0 || scalar(t, s, `SELECT count(*) FROM automation_fire WHERE state='pending' AND task_title IS NULL`) != 1 {
		t.Fatal("deactivation recovered native title", err)
	}
	var version int64
	if err = s.db.QueryRow(`SELECT version FROM automation WHERE automation_id=?`, id).Scan(&version); err != nil {
		t.Fatal(err)
	}
	if result, err := r.ManageAutomation(ctx, "cancel-without-title", automation.Request{Operation: "cancel", Ref: id, Version: version}, now); err != nil || result.Error != nil {
		t.Fatal("local cancellation depended on title producer", result, err)
	}
	if err = r.admitAutomations(ctx); err != nil || scalar(t, s, `SELECT count(*) FROM automation_fire WHERE state='skipped' AND task_title IS NULL`) != 1 {
		t.Fatal("cancelled pending fire blocked on title", err)
	}
}

func TestAutomationTaskLabelExactRollbackRetainsAcceptedTask(t *testing.T) {
	s, r, _, _ := automationFixture(t)
	ctx := context.Background()
	now := time.Date(2030, 1, 2, 0, 0, 0, 0, time.UTC)
	h := installTaskPlugin(t, s, r)
	baseline, err := h.Store.Status(ctx)
	if err != nil {
		t.Fatal(err)
	}
	upgraded, _ := selectTaskVersion(t, h, taskVersionPackage(t, "1.1.0"), "title-before-exact-rollback", baseline.Revision, true)
	first := createAutomation(t, r, now, nil)
	if err = r.ScheduleAutomations(ctx, now.AddDate(0, 0, 1)); err != nil {
		t.Fatal(err)
	}
	if err = r.admitAutomations(ctx); err != nil {
		t.Fatal(err)
	}
	if err = h.Propose(ctx, rsi.Candidate{ID: "title-exact-rollback", Baseline: upgraded.Revision, Target: baseline.Revision, PolicyID: "task-owner-fixture", Goal: "restore exact original task label package"}); err != nil {
		t.Fatal(err)
	}
	decision, err := h.Verify(ctx, "title-exact-rollback")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = h.Activate(ctx, rsi.Activation{OperationID: "title-exact-rollback", Expected: upgraded.Revision, Target: baseline.Revision, DecisionID: decision}); err != nil {
		t.Fatal(err)
	}
	if _, err = r.ManageAutomation(ctx, "after-label-rollback", automation.Request{Operation: "create", Instruction: "New rollback intent", Type: "cron", Spec: "* * * * *", Timezone: "UTC"}, now); err != nil {
		t.Fatal(err)
	}
	if err = r.ScheduleAutomations(ctx, now.AddDate(0, 0, 1)); err != nil {
		t.Fatal(err)
	}
	if err = r.admitAutomations(ctx); err != nil {
		t.Fatal(err)
	}
	if scalar(t, s, `SELECT count(*) FROM automation_fire f JOIN task t ON t.task_id=(SELECT task_id FROM automation WHERE automation_id=f.automation_id) WHERE f.automation_id=? AND f.task_label_graph=? AND t.title='[v1.1] HKG-CDG 2026-12-01 price'`, first, upgraded.Revision) != 1 {
		t.Fatal("rollback rewrote accepted task/label graph")
	}
	if scalar(t, s, `SELECT count(*) FROM task WHERE title='New rollback intent'`) != 1 || scalar(t, s, `SELECT count(*) FROM automation_fire WHERE task_title='New rollback intent' AND task_label_graph=?`, baseline.Revision) != 1 {
		t.Fatal("new task did not use exact rollback graph")
	}
}

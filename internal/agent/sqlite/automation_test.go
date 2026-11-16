package sqlite

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/z-chenhao/assistant/internal/agent"
	"github.com/z-chenhao/assistant/internal/automation"
	"github.com/z-chenhao/assistant/internal/llm"
	"github.com/z-chenhao/assistant/internal/memory"
	memorysqlite "github.com/z-chenhao/assistant/internal/memory/sqlite"
	"github.com/z-chenhao/assistant/internal/task"
	"github.com/z-chenhao/assistant/internal/tool"
)

// Historical upgrade fixtures first remove the later, empty Automation schema.
const removeAutomationFixtureSchema = removeChannelFixtureSchema + `DROP TABLE automation_request; DROP TABLE automation_fire; DROP TABLE automation; `

func automationFixture(t *testing.T) (*Store, *Runs, agent.Actor, *loopProvider) {
	s, r, _, a, p := loopFixture(t)
	echo, _ := tool.FixtureRegistry().Resolve("mcp__fixture__echo")
	defs := append([]tool.Definition{echo}, automation.Definitions()...)
	registry, e := tool.NewRegistry(defs)
	if e != nil {
		t.Fatal(e)
	}
	r.registry = registry
	a.Registry = registry
	return s, r, a, p
}
func createAutomation(t *testing.T, r *Runs, now time.Time, below *int64) string {
	t.Helper()
	result, e := r.ManageAutomation(context.Background(), "create", automation.Request{Operation: "create", Instruction: "HKG-CDG 2026-12-01 price", Type: "cron", Spec: "0 8 * * *", Timezone: "Asia/Shanghai", Below: below}, now)
	if e != nil || result.Error != nil {
		t.Fatalf("create %v %+v", e, result)
	}
	var v struct {
		Ref string `json:"automationRef"`
	}
	if json.Unmarshal([]byte(result.Text), &v) != nil || v.Ref == "" {
		t.Fatal(result)
	}
	return v.Ref
}
func TestAutomationThresholdCrossingsAndCancel(t *testing.T) {
	ctx := context.Background()
	s, r, a, p := automationFixture(t)
	now, _ := time.Parse(time.RFC3339, "2026-10-15T12:00:00+08:00")
	threshold := int64(1800)
	id := createAutomation(t, r, now, &threshold)
	price := int64(0)
	p.respond = func(req llm.GenerationRequest) (llm.GenerationResult, error) {
		worker := isRoot(req)
		last := req.Messages[len(req.Messages)-1]
		if worker && last.Role != "tool" {
			return llm.GenerationResult{ToolCalls: []llm.ToolCall{{ID: "price", Name: automation.ObserveName, Arguments: fmtValue(price)}, {ID: "second-source", Name: automation.ObserveName, Arguments: fmtValue(price)}}}, nil
		}
		return llm.GenerationResult{Text: "observed price"}, nil
	}
	for i, value := range []int64{2200, 2190, 1750, 1740, 2050, 1780} {
		price = value
		day := now.AddDate(0, 0, i+1)
		r.SetTestAutomationClock(day)
		if e := r.ScheduleAutomations(ctx, day); e != nil {
			t.Fatal(e)
		}
		if e := r.ScheduleAutomations(ctx, day); e != nil {
			t.Fatal(e)
		}
		mustDrain(t, a)
		if got := scalar(t, s, `SELECT count(*) FROM run WHERE kind='worker'`); got != i+1 {
			t.Fatalf("day %d roots %d", i, got)
		}
	}
	if scalar(t, s, `SELECT count(*) FROM task_update`) != 2 || scalar(t, s, `SELECT count(*) FROM automation_fire WHERE notify=1`) != 2 {
		t.Fatal("notifications must only be Oct18/Oct21")
	}
	if scalar(t, s, `SELECT count(*) FROM task WHERE status='active' AND completion_policy='ongoing'`) != 1 {
		t.Fatal("ongoing Task lost")
	}
	var version int64
	s.db.QueryRow(`SELECT version FROM automation WHERE automation_id=?`, id).Scan(&version)
	// Editing the schedule preserves an already-inside threshold state. A new
	// daily check below the same threshold must remain quiet.
	edit := automation.Request{Operation: "update", Ref: id, Version: version, Instruction: "HKG-CDG 2026-12-01 price", Type: "cron", Spec: "0 9 * * *", Timezone: "Asia/Shanghai", Below: &threshold}
	if result, e := r.ManageAutomation(ctx, "edit-time", edit, now.AddDate(0, 0, 6)); e != nil || result.Error != nil {
		t.Fatal(result, e)
	}
	price = 1700
	r.SetTestAutomationClock(now.AddDate(0, 0, 7))
	mustDrain(t, a)
	if scalar(t, s, `SELECT count(*) FROM task_update`) != 2 {
		t.Fatal("schedule edit repeated threshold notification")
	}
	s.db.QueryRow(`SELECT version FROM automation WHERE automation_id=?`, id).Scan(&version)
	if result, e := r.ManageAutomation(ctx, "cancel", automation.Request{Operation: "cancel", Ref: id, Version: version}, now.AddDate(0, 0, 7)); e != nil || result.Error != nil {
		t.Fatal(e, result)
	}
	r.SetTestAutomationClock(now.AddDate(0, 0, 20))
	mustDrain(t, a)
	if scalar(t, s, `SELECT count(*) FROM run WHERE kind='worker'`) != 7 {
		t.Fatal("cancel created a Root")
	}
}
func fmtValue(v int64) string {
	raw, _ := json.Marshal(struct {
		Value int64 `json:"value"`
	}{v})
	return string(raw)
}
func TestAutomationAtomicFireDelayedCancelAndCAS(t *testing.T) {
	ctx := context.Background()
	s, r, _, _ := automationFixture(t)
	now := time.Now().UTC()
	id := createAutomation(t, r, now, nil)
	fire := now.AddDate(0, 0, 3)
	// Transaction failure after Outbox write must leave schedule and fires intact.
	_, e := s.db.Exec(`CREATE TRIGGER reject_fire BEFORE INSERT ON event_outbox WHEN NEW.type='ai.steward.automation_fired.v1' BEGIN SELECT RAISE(ABORT,'fault'); END;`)
	if e != nil {
		t.Fatal(e)
	}
	if e = r.ScheduleAutomations(ctx, fire); !errors.Is(e, agent.ErrStorage) {
		t.Fatalf("fault %v", e)
	}
	if scalar(t, s, `SELECT count(*) FROM automation_fire`) != 0 {
		t.Fatal("partial fire")
	}
	s.db.Exec(`DROP TRIGGER reject_fire`)
	if e = r.ScheduleAutomations(ctx, fire); e != nil {
		t.Fatal(e)
	}
	if scalar(t, s, `SELECT count(*) FROM run`) != 0 {
		t.Fatal("Scheduler executed work")
	}
	// Missed days coalesce; schedule advances past the scan time.
	if scalar(t, s, `SELECT count(*) FROM automation_fire`) != 1 {
		t.Fatal("missed fire storm")
	}
	if result, e := r.ManageAutomation(ctx, "stale", automation.Request{Operation: "cancel", Ref: id, Version: 1}, fire); e != nil || result.Error == nil {
		t.Fatal("stale CAS accepted", result, e)
	}
	var v int64
	s.db.QueryRow(`SELECT version FROM automation WHERE automation_id=?`, id).Scan(&v)
	if _, e = r.ManageAutomation(ctx, "cancel", automation.Request{Operation: "cancel", Ref: id, Version: v}, fire); e != nil {
		t.Fatal(e)
	}
	if e = r.admitAutomations(ctx); e != nil {
		t.Fatal(e)
	}
	if scalar(t, s, `SELECT count(*) FROM run`) != 0 || scalar(t, s, `SELECT count(*) FROM automation_fire WHERE state='skipped'`) != 1 {
		t.Fatal("delayed cancel admitted work")
	}
}
func TestAutomationConcurrentSchedulersDeduplicate(t *testing.T) {
	ctx := context.Background()
	s, r, _, _ := automationFixture(t)
	now := time.Now()
	createAutomation(t, r, now, nil)
	var wg sync.WaitGroup
	errs := make(chan error, 8)
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); errs <- r.ScheduleAutomations(ctx, now.AddDate(0, 0, 1)) }()
	}
	wg.Wait()
	close(errs)
	for e := range errs {
		if e != nil {
			t.Fatal(e)
		}
	}
	if scalar(t, s, `SELECT count(*) FROM automation_fire`) != 1 {
		t.Fatal("duplicate fire")
	}
}

func TestAutomationScheduleEditInvalidatesDelayedFireAndFreezesAtAdmission(t *testing.T) {
	ctx := context.Background()
	s, r, _, _ := automationFixture(t)
	now := time.Now()
	id := createAutomation(t, r, now, nil)
	fire := now.AddDate(0, 0, 1)
	if e := r.ScheduleAutomations(ctx, fire); e != nil {
		t.Fatal(e)
	}
	var v int64
	if e := s.db.QueryRow(`SELECT version FROM automation WHERE automation_id=?`, id).Scan(&v); e != nil {
		t.Fatal(e)
	}
	q := automation.Request{Operation: "update", Ref: id, Version: v, Instruction: "Edited reminder", Type: "at", Spec: fire.Add(time.Hour).Format(time.RFC3339), Timezone: "UTC"}
	result, e := r.ManageAutomation(ctx, "edit", q, fire)
	if e != nil || result.Error != nil {
		t.Fatal(e, result)
	}
	if e = r.admitAutomations(ctx); e != nil {
		t.Fatal(e)
	}
	if scalar(t, s, `SELECT count(*) FROM run`) != 0 {
		t.Fatal("old schedule admitted")
	}
	if e = r.ScheduleAutomations(ctx, fire.Add(2*time.Hour)); e != nil {
		t.Fatal(e)
	}
	// Profile changes after trigger but before admission affect only this new Run.
	input := modelInput()
	input.Name = "new admission model"
	input.ModelID = "new-model"
	profile := activateLoop(t, s, input)
	if e = r.admitAutomations(ctx); e != nil {
		t.Fatal(e)
	}
	if scalar(t, s, `SELECT count(*) FROM run r JOIN model_snapshot m ON m.snapshot_id=r.model_snapshot_id WHERE m.profile_id=?`, profile.ProfileID) != 1 {
		t.Fatal("snapshot was not frozen at admission")
	}
	if scalar(t, s, `SELECT count(*) FROM automation_fire WHERE state='skipped'`) != 1 {
		t.Fatal("old schedule not fenced")
	}
	if e = r.admitAutomations(ctx); e != nil {
		t.Fatal(e)
	}
	if scalar(t, s, `SELECT count(*) FROM run`) != 1 {
		t.Fatal("duplicate admission")
	}
	var root string
	if e = s.db.QueryRow(`SELECT run_id FROM run`).Scan(&root); e != nil {
		t.Fatal(e)
	}
	claim, e := r.Claim(ctx, root)
	if e != nil {
		t.Fatal(e)
	}
	if e = s.db.QueryRow(`SELECT version FROM automation WHERE automation_id=?`, id).Scan(&v); e != nil {
		t.Fatal(e)
	}
	if _, e = r.ManageAutomation(ctx, "cancel", automation.Request{Operation: "cancel", Ref: id, Version: v}, fire); e != nil {
		t.Fatal(e)
	}
	if _, e = r.Load(ctx, claim); !errors.Is(e, agent.ErrClaimLost) {
		t.Fatalf("cancel did not fence old owner %v", e)
	}
}

// An edit can commit after the Root result or after Reporter has queued its
// update. The Task epoch stays fixed; the fire lineage must fence both queues.
func TestAutomationScheduleEditFencesCommittedFollowups(t *testing.T) {
	for _, reported := range []bool{false, true} {
		t.Run(fmt.Sprintf("reported=%t", reported), func(t *testing.T) {
			ctx := context.Background()
			s, r, a, p := automationFixture(t)
			p.respond = func(llm.GenerationRequest) (llm.GenerationResult, error) {
				return llm.GenerationResult{Text: "reminder result"}, nil
			}
			now := time.Now().UTC()
			id := createAutomation(t, r, now, nil)
			fire := now.AddDate(0, 0, 1)
			r.SetTestAutomationClock(fire)
			if err := r.ScheduleAutomations(ctx, fire); err != nil {
				t.Fatal(err)
			}
			if err := r.admitAutomations(ctx); err != nil {
				t.Fatal(err)
			}
			var root string
			if err := s.db.QueryRow(`SELECT root_run_id FROM automation_fire`).Scan(&root); err != nil {
				t.Fatal(err)
			}
			claim, err := r.Claim(ctx, root)
			if err != nil {
				t.Fatal(err)
			}
			if err = executeTaskRun(t, a, r, ctx, claim); err != nil {
				t.Fatal(err)
			}
			if scalar(t, s, `SELECT count(*) FROM task_update`) != 1 {
				t.Fatal("result not committed")
			}
			if reported {
				if err = r.Report(ctx); err != nil {
					t.Fatal(err)
				}
				if scalar(t, s, `SELECT count(*) FROM main_inbox WHERE run_id IS NULL`) != 1 {
					t.Fatal("report not queued")
				}
			}
			var version int64
			if err = s.db.QueryRow(`SELECT version FROM automation WHERE automation_id=?`, id).Scan(&version); err != nil {
				t.Fatal(err)
			}
			editedFire := fire.AddDate(0, 0, 2)
			result, err := r.ManageAutomation(ctx, "edit", automation.Request{Operation: "update", Ref: id, Version: version, Instruction: "Edited reminder", Type: "at", Spec: editedFire.Format(time.RFC3339), Timezone: "UTC"}, fire)
			if err != nil || result.Error != nil {
				t.Fatal(result, err)
			}
			mustDrain(t, a)
			mustDrain(t, a)
			if scalar(t, s, `SELECT count(*) FROM run WHERE kind='main'`) != 0 || scalar(t, s, `SELECT count(*) FROM event_inbox WHERE consumer='task_reporter'`) != 1 {
				t.Fatal("obsolete update was not consumed without Main")
			}
			if scalar(t, s, `SELECT count(*) FROM main_inbox WHERE run_id IS NOT NULL`) != 0 {
				t.Fatal("obsolete inbox promoted")
			}
			r.SetTestAutomationClock(editedFire)
			mustDrain(t, a)
			mustDrain(t, a)
			if scalar(t, s, `SELECT count(*) FROM run WHERE kind='main' AND status='completed'`) != 1 || scalar(t, s, `SELECT count(*) FROM main_inbox WHERE run_id IS NOT NULL`) != 1 {
				t.Fatal("edited intent did not complete exactly one Main followup")
			}
		})
	}
}

func TestAutomationForgetSkipsRevokedTaskWithoutBlockingActor(t *testing.T) {
	ctx := context.Background()
	s, r, a, p := automationFixture(t)
	m, err := memorysqlite.Open(ctx, filepath.Dir(s.path))
	if err != nil {
		t.Fatal(err)
	}
	defer m.Close()
	r.ConfigureMemory(m, memory.Vault{})
	p.respond = func(llm.GenerationRequest) (llm.GenerationResult, error) {
		return llm.GenerationResult{Text: "reminder result"}, nil
	}
	now := time.Now().UTC()
	id := createAutomation(t, r, now, nil)
	first := now.AddDate(0, 0, 1)
	r.SetTestAutomationClock(first)
	mustDrain(t, a)
	var oldTask string
	if err = s.db.QueryRow(`SELECT task_id FROM automation WHERE automation_id=?`, id).Scan(&oldTask); err != nil {
		t.Fatal(err)
	}
	if scalar(t, s, `SELECT count(*) FROM run WHERE task_id=?`, oldTask) != 1 {
		t.Fatal("first recurring occurrence did not finish")
	}
	forget, err := r.ManageMemory(ctx, "forget-health", memory.Mutation{Operation: "forget", Category: "health"})
	if err != nil || forget["accepted"] != true {
		t.Fatal(forget, err)
	}
	p.requests = nil
	fresh := mustSubmit(t, a, "fresh", "Fresh input after Forget")
	second := first.AddDate(0, 0, 1)
	r.SetTestAutomationClock(second)
	mustDrain(t, a)
	mustDrain(t, a)
	if scalar(t, s, `SELECT count(*) FROM automation_fire WHERE state='skipped'`) != 1 || scalar(t, s, `SELECT count(*) FROM event_inbox WHERE consumer='automation_actor'`) != 2 {
		t.Fatal("revoked fire not durably skipped and consumed")
	}
	if scalar(t, s, `SELECT count(*) FROM run WHERE run_id=? AND status='completed'`, fresh.RunID) != 1 {
		t.Fatal("revoked fire blocked fresh Main")
	}
	if scalar(t, s, `SELECT count(*) FROM task WHERE task_id=? AND status='active'`, oldTask) != 1 || scalar(t, s, `SELECT count(*) FROM run WHERE task_id=?`, oldTask) != 1 {
		t.Fatal("Task was cancelled or revoked context revived")
	}
	third := second.AddDate(0, 0, 1)
	result, err := r.ManageAutomation(ctx, "fresh-automation", automation.Request{Operation: "create", Instruction: "Fresh scheduled intent", Type: "at", Spec: third.Format(time.RFC3339), Timezone: "UTC"}, second)
	if err != nil || result.Error != nil {
		t.Fatal(result, err)
	}
	r.SetTestAutomationClock(third)
	mustDrain(t, a)
	if scalar(t, s, `SELECT count(*) FROM run WHERE kind='worker' AND status='completed'`) != 2 || scalar(t, s, `SELECT count(*) FROM automation_fire WHERE state='skipped'`) != 2 || scalar(t, s, `SELECT count(*) FROM automation_fire WHERE state='pending'`) != 0 {
		t.Fatal("fresh Automation blocked or revoked occurrence executed")
	}
	for _, req := range p.requests {
		raw, _ := encoded(req)
		if strings.Contains(raw, "HKG-CDG 2026-12-01 price") {
			t.Fatal("revoked Task context imported", raw)
		}
	}
}

func TestAutomationListAfterForgetUsesSafeControlProjection(t *testing.T) {
	ctx := context.Background()
	s, r, a, p := automationFixture(t)
	m, err := memorysqlite.Open(ctx, filepath.Dir(s.path))
	if err != nil {
		t.Fatal(err)
	}
	defer m.Close()
	r.ConfigureMemory(m, memory.Vault{})
	now, _ := time.Parse(time.RFC3339, "2026-10-15T12:00:00+08:00")
	r.SetTestAutomationClock(now)
	marker := "OLD_HEALTH_AUTOMATION_CONTEXT"
	threshold := int64(1800)
	q := automation.Request{Operation: "create", Instruction: marker, Type: "at", Spec: "2026-12-01T08:00:00+08:00", Timezone: "Asia/Shanghai", Below: &threshold}
	oldReceipt, err := r.ManageAutomation(ctx, "old-intent", q, now)
	if err != nil || oldReceipt.Error != nil {
		t.Fatal(oldReceipt, err)
	}
	var receipt struct {
		Ref string `json:"automationRef"`
	}
	if err := json.Unmarshal([]byte(oldReceipt.Text), &receipt); err != nil {
		t.Fatal(err)
	}
	oldID := receipt.Ref
	before, err := r.ManageAutomation(ctx, "before", automation.Request{Operation: "list"}, now)
	if err != nil || !strings.Contains(before.Text, marker) {
		t.Fatal("fresh view missing", before, err)
	}
	forgotten, err := r.ManageMemory(ctx, "forget-health", memory.Mutation{Operation: "forget", Category: "health"})
	if err != nil || forgotten["accepted"] != true {
		t.Fatal(forgotten, err)
	}
	retry, err := r.ManageAutomation(ctx, "old-intent", q, now)
	if err != nil || retry.Text != oldReceipt.Text {
		t.Fatal("receipt drift", retry, err)
	}
	edit := q
	edit.Operation = "update"
	edit.Ref = oldID
	edit.Version = 1
	edit.Spec = "2026-12-02T08:00:00+08:00"
	if result, err := r.ManageAutomation(ctx, "edit-old", edit, now); err != nil || result.Error != nil {
		t.Fatal(result, err)
	}
	freshQ := q
	freshQ.Instruction = "Fresh independent intent"
	if result, err := r.ManageAutomation(ctx, "new-intent", freshQ, now); err != nil || result.Error != nil {
		t.Fatal(result, err)
	}
	p.respond = func(req llm.GenerationRequest) (llm.GenerationResult, error) {
		if req.Messages[len(req.Messages)-1].Role == "tool" {
			return llm.GenerationResult{Text: "management complete"}, nil
		}
		return llm.GenerationResult{ToolCalls: []llm.ToolCall{{ID: "list", Name: automation.ManageName, Arguments: `{"operation":"list"}`}}}, nil
	}
	main := mustSubmit(t, a, "fresh-list", `Automation: {"operation":"list"}`)
	mustDrain(t, a)
	mustDrain(t, a)
	if len(p.requests) != 2 || scalar(t, s, `SELECT count(*) FROM run WHERE run_id=? AND status='completed'`, main.RunID) != 1 {
		t.Fatal("real Main/Tool cycle incomplete")
	}
	var raw string
	if err := s.db.QueryRow(`SELECT result_json FROM tool_invocation WHERE run_id=? AND tool_name=? AND status='succeeded'`, main.RunID, automation.ManageName).Scan(&raw); err != nil {
		t.Fatal(err)
	}
	var result tool.Result
	var views []automation.View
	if err := json.Unmarshal([]byte(raw), &result); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal([]byte(result.Text), &views); err != nil {
		t.Fatal(err)
	}
	if len(views) != 2 {
		t.Fatal("management handles lost", views)
	}
	var old automation.View
	for _, v := range views {
		if v.Ref == oldID {
			old = v
			if v.Instruction != "" || v.Type != "" || v.Spec != "" || v.Timezone != "" || v.Next != nil || v.Below != nil || v.Version != 2 || !v.Enabled {
				t.Fatal("revoked intent fields exposed or controls lost", v)
			}
		} else if v.Instruction != freshQ.Instruction || v.Type != freshQ.Type || v.Spec != freshQ.Spec || v.Timezone != freshQ.Timezone || v.Next == nil || v.Below == nil || *v.Below != threshold {
			t.Fatal("fresh view changed", v)
		}
	}
	if old.Ref == "" || strings.Contains(raw, marker) {
		t.Fatal("old context exposed or cancellation handle missing")
	}
	for _, req := range p.requests {
		data, _ := encoded(req)
		if strings.Contains(data, marker) {
			t.Fatal("revoked context in model request")
		}
	}
	if scalar(t, s, `SELECT count(*) FROM automation WHERE automation_id=? AND instruction=? AND intent_privacy_epoch=0 AND version=2`, oldID, marker) != 1 || scalar(t, s, `SELECT count(*) FROM task`) != 0 || scalar(t, s, `SELECT count(*) FROM automation_fire`) != 0 {
		t.Fatal("list changed audit/provenance or executed work")
	}
	cancelArgs, _ := encoded(automation.Request{Operation: "cancel", Ref: old.Ref, Version: old.Version})
	p.respond = func(req llm.GenerationRequest) (llm.GenerationResult, error) {
		if req.Messages[len(req.Messages)-1].Role == "tool" {
			return llm.GenerationResult{Text: "cancelled"}, nil
		}
		return llm.GenerationResult{ToolCalls: []llm.ToolCall{{ID: "cancel", Name: automation.ManageName, Arguments: cancelArgs}}}, nil
	}
	mustSubmit(t, a, "cancel-old", "Cancel the revoked schedule using its control handle")
	mustDrain(t, a)
	if scalar(t, s, `SELECT count(*) FROM automation WHERE automation_id=? AND enabled=0 AND instruction=? AND intent_privacy_epoch=0`, oldID, marker) != 1 {
		t.Fatal("safe model cancellation failed or audit changed")
	}
}

func TestAutomationLinkedTaskProjectionAfterForget(t *testing.T) {
	ctx := context.Background()
	s, r, a, p := automationFixture(t)
	echo, _ := tool.FixtureRegistry().Resolve("mcp__fixture__echo")
	defs := append([]tool.Definition{echo}, automation.Definitions()...)
	defs = append(defs, task.Definitions()...)
	registry, err := tool.NewRegistry(defs)
	if err != nil {
		t.Fatal(err)
	}
	r.registry = registry
	a.Registry = registry
	m, err := memorysqlite.Open(ctx, filepath.Dir(s.path))
	if err != nil {
		t.Fatal(err)
	}
	defer m.Close()
	r.ConfigureMemory(m, memory.Vault{})
	now := time.Now().UTC()
	createAutomation(t, r, now, nil)
	r.SetTestAutomationClock(now.AddDate(0, 0, 1))
	p.respond = func(llm.GenerationRequest) (llm.GenerationResult, error) {
		return llm.GenerationResult{Text: "HKG-CDG 2026-12-01 price result"}, nil
	}
	mustDrain(t, a)
	if scalar(t, s, `SELECT count(*) FROM task`) != 1 {
		t.Fatal("linked Task fixture missing")
	}
	if _, err := r.ManageMemory(ctx, "forget", memory.Mutation{Operation: "forget", Category: "health"}); err != nil {
		t.Fatal(err)
	}
	p.requests = nil
	p.respond = func(req llm.GenerationRequest) (llm.GenerationResult, error) {
		if req.Messages[len(req.Messages)-1].Role == "tool" {
			return llm.GenerationResult{Text: "safe management"}, nil
		}
		return llm.GenerationResult{ToolCalls: []llm.ToolCall{{ID: "list", Name: automation.ManageName, Arguments: `{"operation":"list"}`}, {ID: "search", Name: task.SearchName, Arguments: `{"scope":"all"}`}}}, nil
	}
	fresh := mustSubmit(t, a, "fresh-search", "List Automation controls and search current Tasks")
	mustDrain(t, a)
	mustDrain(t, a)
	var raw string
	if err := s.db.QueryRow(`SELECT result_json FROM tool_invocation WHERE run_id=? AND tool_name=? AND status='succeeded'`, fresh.RunID, task.SearchName).Scan(&raw); err != nil {
		t.Fatal(err)
	}
	var result tool.Result
	if err := json.Unmarshal([]byte(raw), &result); err != nil || result.Text != "[]" {
		t.Fatal("revoked Automation Task searchable", raw, err)
	}
	if len(p.requests) != 2 {
		t.Fatal("Main Tool cycle incomplete")
	}
	for _, req := range p.requests {
		data, _ := encoded(req)
		if strings.Contains(data, "HKG-CDG 2026-12-01 price") {
			t.Fatal("Automation-derived context reentered Main")
		}
	}
}

// No Task may launder an Automation accepted before Forget into fresh context.
func TestAutomationFirstFireForgetSkipsIntentWithoutTask(t *testing.T) {
	for _, pending := range []bool{false, true} {
		t.Run(fmt.Sprintf("pending=%t", pending), func(t *testing.T) {
			ctx := context.Background()
			s, r, a, p := automationFixture(t)
			m, err := memorysqlite.Open(ctx, filepath.Dir(s.path))
			if err != nil {
				t.Fatal(err)
			}
			defer m.Close()
			r.ConfigureMemory(m, memory.Vault{})
			p.respond = func(llm.GenerationRequest) (llm.GenerationResult, error) {
				return llm.GenerationResult{Text: "fresh result"}, nil
			}
			// Scheduling time deliberately differs from the privacy wall clock.
			now, _ := time.Parse(time.RFC3339, "2026-10-15T12:00:00+08:00")
			id := createAutomation(t, r, now, nil)
			due := now.AddDate(0, 0, 1)
			r.SetTestAutomationClock(due)
			if pending {
				if err := r.ScheduleAutomations(ctx, due); err != nil {
					t.Fatal(err)
				}
			}
			receipt, err := r.ManageMemory(ctx, "forget", memory.Mutation{Operation: "forget", Category: "health"})
			if err != nil || receipt["accepted"] != true {
				t.Fatal(receipt, err)
			}
			// Replaying the accepted create receipt cannot renew its provenance.
			if got := createAutomation(t, r, now, nil); got != id {
				t.Fatal("receipt drift")
			}
			fresh := mustSubmit(t, a, "fresh", "Fresh input after Forget")
			mustDrain(t, a)
			mustDrain(t, a)
			if scalar(t, s, `SELECT count(*) FROM automation_fire WHERE state='skipped' AND root_run_id IS NULL`) != 1 || scalar(t, s, `SELECT count(*) FROM event_inbox WHERE consumer='automation_actor'`) != 1 || scalar(t, s, `SELECT count(*) FROM task`) != 0 {
				t.Fatal("pre-Forget intent created Task/Root or was not durably skipped")
			}
			if scalar(t, s, `SELECT count(*) FROM run WHERE run_id=? AND status='completed'`, fresh.RunID) != 1 {
				t.Fatal("fresh Main blocked")
			}
			var version int64
			if err := s.db.QueryRow(`SELECT version FROM automation WHERE automation_id=?`, id).Scan(&version); err != nil {
				t.Fatal(err)
			}
			next := due.AddDate(0, 0, 1)
			q := automation.Request{Operation: "update", Ref: id, Version: version, Instruction: "HKG-CDG 2026-12-01 price", Type: "at", Spec: next.Format(time.RFC3339), Timezone: "UTC"}
			if result, err := r.ManageAutomation(ctx, "edit-old", q, due); err != nil || result.Error != nil {
				t.Fatal(result, err)
			}
			result, err := r.ManageAutomation(ctx, "new-intent", automation.Request{Operation: "create", Instruction: "Fresh scheduled intent", Type: "at", Spec: next.Format(time.RFC3339), Timezone: "UTC"}, due)
			if err != nil || result.Error != nil {
				t.Fatal(result, err)
			}
			r.SetTestAutomationClock(next)
			mustDrain(t, a)
			mustDrain(t, a)
			if scalar(t, s, `SELECT count(*) FROM automation_fire WHERE state='skipped'`) != 2 || scalar(t, s, `SELECT count(*) FROM run WHERE kind='worker' AND status='completed'`) != 1 || scalar(t, s, `SELECT count(*) FROM task`) != 1 || scalar(t, s, `SELECT count(*) FROM event_inbox WHERE consumer='automation_actor'`) != 3 {
				t.Fatal("edit renewed revoked intent or fresh Automation failed")
			}
			for _, req := range p.requests {
				raw, _ := encoded(req)
				if strings.Contains(raw, "HKG-CDG 2026-12-01 price") {
					t.Fatal("revoked intent in model request", raw)
				}
			}
		})
	}
}

func TestAutomationAdmissionStorageFailureRemainsPending(t *testing.T) {
	ctx := context.Background()
	s, r, a, p := automationFixture(t)
	p.respond = func(llm.GenerationRequest) (llm.GenerationResult, error) {
		return llm.GenerationResult{Text: "reminder result"}, nil
	}
	now := time.Now().UTC()
	createAutomation(t, r, now, nil)
	fire := now.AddDate(0, 0, 1)
	r.SetTestAutomationClock(fire)
	if err := r.ScheduleAutomations(ctx, fire); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec(`CREATE TRIGGER reject_automation_root BEFORE INSERT ON run WHEN NEW.kind='worker' BEGIN SELECT RAISE(ABORT,'storage fault'); END`); err != nil {
		t.Fatal(err)
	}
	if err := r.admitAutomations(ctx); !errors.Is(err, agent.ErrStorage) {
		t.Fatalf("storage failure swallowed: %v", err)
	}
	if scalar(t, s, `SELECT count(*) FROM automation_fire WHERE state='pending'`) != 1 || scalar(t, s, `SELECT count(*) FROM task`) != 0 || scalar(t, s, `SELECT count(*) FROM event_inbox WHERE consumer='automation_actor'`) != 0 {
		t.Fatal("storage failure partially committed admission")
	}
	if _, err := s.db.Exec(`DROP TRIGGER reject_automation_root`); err != nil {
		t.Fatal(err)
	}
	mustDrain(t, a)
	if scalar(t, s, `SELECT count(*) FROM run WHERE kind='worker' AND status='completed'`) != 1 {
		t.Fatal("recoverable storage failure lost occurrence")
	}
}

func TestAutomationMigrationPreservesPriorAndRollsBack(t *testing.T) {
	ctx := context.Background()
	db := schemaDB(t)
	if e := migrate(ctx, db, migrations[:22]); e != nil {
		t.Fatal(e)
	}
	if _, e := db.Exec(`INSERT INTO agent_config VALUES(1,'prior-automation',NULL,'setup_required',7,1,1)`); e != nil {
		t.Fatal(e)
	}
	failed := append(append([]string{}, migrations[:22]...), automationSQL+"; INVALID SQL")
	if e := migrate(ctx, db, failed); e == nil {
		t.Fatal("fault accepted")
	}
	var version, tables int
	db.QueryRow(`PRAGMA user_version`).Scan(&version)
	db.QueryRow(`SELECT count(*) FROM sqlite_schema WHERE name='automation'`).Scan(&tables)
	if version != 22 || tables != 0 {
		t.Fatal("partial schema migration")
	}
	if e := migrate(ctx, db, migrations); e != nil {
		t.Fatal(e)
	}
	var id string
	var config int
	if e := db.QueryRow(`SELECT agent_id,config_version FROM agent_config`).Scan(&id, &config); e != nil || id != "prior-automation" || config != 7 {
		t.Fatal("prior data lost", e)
	}
}

func TestAutomationPrivacyMigrationRetainsAuditAndFailsClosed(t *testing.T) {
	for _, barrier := range []bool{false, true} {
		t.Run(fmt.Sprintf("barrier=%t", barrier), func(t *testing.T) {
			ctx := context.Background()
			db := schemaDB(t)
			if err := migrate(ctx, db, migrations[:23]); err != nil {
				t.Fatal(err)
			}
			if _, err := db.Exec(`INSERT INTO agent_config VALUES(1,'legacy',NULL,'setup_required',7,1,1);
    INSERT INTO automation(automation_id,agent_id,instruction,trigger_type,trigger_spec,timezone,enabled,next_fire_at,created_at,updated_at,version) VALUES('legacy-auto','legacy','audit intent','at','2026-12-01T00:00:00Z','UTC',1,100,10,20,1);
    INSERT INTO automation_fire(event_id,automation_id,schedule_version,fire_at,instruction,state) VALUES('legacy-fire','legacy-auto',1,100,'audit intent','pending');
    INSERT INTO automation_request VALUES('legacy','receipt','hash','{"text":"accepted"}');`); err != nil {
				t.Fatal(err)
			}
			if barrier {
				if _, err := db.Exec(`INSERT INTO memory_privacy VALUES('legacy',2,0,15)`); err != nil {
					t.Fatal(err)
				}
			}
			broken := append(append([]string{}, migrations[:23]...), automationPrivacySQL+`; INVALID SQL`)
			if err := migrate(ctx, db, broken); err == nil {
				t.Fatal("partial migration accepted")
			}
			st := &Store{db: db}
			if scalar(t, st, `PRAGMA user_version`) != 23 || scalar(t, st, `SELECT count(*) FROM pragma_table_info('automation') WHERE name='intent_privacy_epoch'`) != 0 {
				t.Fatal("partial migration committed")
			}
			if err := migrate(ctx, db, migrations); err != nil {
				t.Fatal(err)
			}
			if scalar(t, st, `SELECT count(*) FROM automation WHERE instruction='audit intent' AND created_at=10 AND updated_at=20 AND version=1 AND intent_privacy_epoch=0`) != 1 || scalar(t, st, `SELECT count(*) FROM automation_fire WHERE event_id='legacy-fire' AND state='pending'`) != 1 || scalar(t, st, `SELECT count(*) FROM automation_request WHERE request_key='receipt' AND request_hash='hash'`) != 1 {
				t.Fatal("migration rewrote audit")
			}
			want := 0
			if barrier {
				want = 1
			}
			if scalar(t, st, `SELECT a.intent_privacy_epoch!=coalesce(p.epoch,0) FROM automation a LEFT JOIN memory_privacy p USING(agent_id)`) != want {
				t.Fatal("legacy privacy eligibility incorrect")
			}
			if _, err := db.Exec(`UPDATE automation SET intent_privacy_epoch=2,version=version+1`); err == nil {
				t.Fatal("intent provenance mutable")
			}
			if barrier {
				if err := (&Runs{host: st}).admitAutomations(ctx); err != nil {
					t.Fatal(err)
				}
				if scalar(t, st, `SELECT count(*) FROM automation_fire WHERE state='skipped'`) != 1 || scalar(t, st, `SELECT count(*) FROM event_inbox WHERE consumer='automation_actor'`) != 1 || scalar(t, st, `SELECT count(*) FROM task`) != 0 {
					t.Fatal("legacy intent not retired without execution")
				}
			}
		})
	}
}

func TestAutomationRevokedSkipStorageFailureRollsBack(t *testing.T) {
	ctx := context.Background()
	s, r, _, _ := automationFixture(t)
	now := time.Now().UTC()
	createAutomation(t, r, now, nil)
	if err := r.ScheduleAutomations(ctx, now.AddDate(0, 0, 1)); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec(`INSERT INTO memory_privacy SELECT agent_id,1,0,1 FROM agent_config;
  CREATE TRIGGER reject_skip_receipt BEFORE INSERT ON event_inbox WHEN NEW.consumer='automation_actor' BEGIN SELECT RAISE(ABORT,'storage fault'); END;`); err != nil {
		t.Fatal(err)
	}
	if err := r.admitAutomations(ctx); !errors.Is(err, agent.ErrStorage) {
		t.Fatalf("skip storage failure swallowed: %v", err)
	}
	if scalar(t, s, `SELECT count(*) FROM automation_fire WHERE state='pending'`) != 1 || scalar(t, s, `SELECT count(*) FROM task`) != 0 || scalar(t, s, `SELECT count(*) FROM event_inbox WHERE consumer='automation_actor'`) != 0 {
		t.Fatal("partial skip committed")
	}
	if _, err := s.db.Exec(`DROP TRIGGER reject_skip_receipt`); err != nil {
		t.Fatal(err)
	}
	if err := r.admitAutomations(ctx); err != nil {
		t.Fatal(err)
	}
	if err := r.admitAutomations(ctx); err != nil {
		t.Fatal(err)
	}
	if scalar(t, s, `SELECT count(*) FROM automation_fire WHERE state='skipped'`) != 1 || scalar(t, s, `SELECT count(*) FROM event_inbox WHERE consumer='automation_actor'`) != 1 || scalar(t, s, `SELECT count(*) FROM task`) != 0 {
		t.Fatal("skip did not recover once")
	}
}

func TestAutomationCapabilityReductionPreservesChildAdmission(t *testing.T) {
	s, _, a, p := childFixture(t, automation.Definitions()...)
	p.respond = childPolicy
	mustSubmit(t, a, "tree", "delegate normally with Automation registry")
	mustDrain(t, a)
	if scalar(t, s, `SELECT count(*) FROM run WHERE worker_position='child' AND status='completed'`) != 2 {
		t.Fatal("Automation broke recursive Child admission")
	}
	if scalar(t, s, `SELECT count(*) FROM run r JOIN capability_snapshot c ON c.snapshot_id=r.capability_snapshot_id,json_each(c.tools_json) j WHERE (r.kind='main' OR r.worker_position IN ('root','child')) AND json_extract(j.value,'$.name')=?`, automation.ObserveName) != 0 {
		t.Fatal("Root observation authority escaped")
	}
	if scalar(t, s, `SELECT count(*) FROM run r JOIN capability_snapshot c ON c.snapshot_id=r.capability_snapshot_id,json_each(c.tools_json) j WHERE r.kind='worker' AND json_extract(j.value,'$.name')=?`, automation.ManageName) != 0 {
		t.Fatal("Automation control escaped Main")
	}
}

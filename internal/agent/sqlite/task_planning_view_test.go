package sqlite

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/z-chenhao/assistant/internal/task"
	tasksqlite "github.com/z-chenhao/assistant/internal/task/sqlite"
)

func TestTaskPlanningReadsPersistedRulesInstructionsAndUndispatchedSchedules(t *testing.T) {
	ctx := context.Background()
	s, _, _, _ := childFixture(t)
	exec := func(query string, args ...any) {
		t.Helper()
		if _, err := s.db.Exec(query, args...); err != nil {
			t.Fatal(err)
		}
	}
	exec(`INSERT INTO task(task_id,task_ref,agent_id,conversation_id,title,anchor_task,anchor_artifact_refs,status,task_epoch,created_at,updated_at,version,completion_policy) SELECT 'planning','task:planning',agent_id,agent_id,'Review renewals','Review actual renewal dates','[]','active',1,100,100,1,'ongoing' FROM agent_config WHERE singleton=1`)
	for i := 1; i <= 22; i++ {
		exec(`INSERT INTO task_instruction(instruction_id,task_id,task_epoch,instruction,artifact_refs,created_at) VALUES(?,'planning',1,?,'[]',?)`, fmt.Sprintf("plan-%d", i), fmt.Sprintf("Accepted instruction %d", i), 100+i)
	}
	exec(`INSERT INTO automation(automation_id,agent_id,instruction,trigger_type,trigger_spec,timezone,enabled,next_fire_at,created_at,updated_at,version,task_id,below_value) SELECT 'automation:linked',agent_id,'Review renewals every morning','cron','0 8 * * *','Asia/Shanghai',1,2000000,100,100,1,'planning',1800 FROM agent_config WHERE singleton=1`)
	exec(`INSERT INTO automation(automation_id,agent_id,instruction,trigger_type,trigger_spec,timezone,enabled,next_fire_at,created_at,updated_at,version) SELECT 'automation:upcoming',agent_id,'Prepare tomorrow','at','2026-11-04T08:00:00+08:00','Asia/Shanghai',1,1000000,101,101,1 FROM agent_config WHERE singleton=1`)
	exec(`INSERT INTO automation(automation_id,agent_id,instruction,trigger_type,trigger_spec,timezone,enabled,created_at,updated_at,version) VALUES('automation:foreign','other-agent','Private foreign schedule','cron','0 9 * * *','UTC',1,101,101,1)`)
	reader, err := tasksqlite.Open(s.path)
	if err != nil {
		t.Fatal(err)
	}
	before := scalar(t, s, `SELECT total_changes()`)
	d, err := reader.Detail(ctx, "task:planning", "")
	if err != nil {
		t.Fatal(err)
	}
	if d.CompletionPolicy != "ongoing" || d.GoalSummary != "Review actual renewal dates" || d.Schedule == nil || d.Schedule.Spec != "0 8 * * *" || d.Schedule.Timezone != "Asia/Shanghai" || d.Schedule.NextAt != "1970-01-01T00:00:02Z" || d.Schedule.Below == nil || *d.Schedule.Below != 1800 {
		t.Fatalf("lost saved configuration: %+v", d)
	}
	if !d.MoreInstructions || len(d.Instructions) != 20 || d.Instructions[0].Text != "Accepted instruction 3" || d.Instructions[19].Text != "Accepted instruction 22" {
		t.Fatal("instructions are not a bounded acceptance-ordered read", d.Instructions)
	}
	listed, err := reader.List(ctx, task.ListQuery{View: "open"})
	if err != nil || len(listed.Items) != 1 || listed.Items[0].Schedule == nil || listed.Items[0].CompletionPolicy != "ongoing" {
		t.Fatal(listed, err)
	}
	schedules, _, err := reader.ScheduledPage(ctx, "", "")
	if err != nil || len(schedules) != 2 || schedules[0].Ref != "automation:upcoming" || schedules[0].TaskRef != "" || schedules[1].TaskRef != "task:planning" {
		t.Fatal("undispatched intent missing or foreign scope leaked", schedules, err)
	}
	if scalar(t, s, `SELECT count(*) FROM task`) != 1 || scalar(t, s, `SELECT count(*) FROM run`) != 0 || scalar(t, s, `SELECT total_changes()`) != before {
		t.Fatal("planning inspection dispatched or mutated work")
	}
	if _, err = reader.Detail(ctx, "task:foreign", ""); !errors.Is(err, task.ErrNotFound) {
		t.Fatal("foreign task scope escaped", err)
	}
	// A new read pool observes the same persisted configuration; no derived
	// planning state or in-memory schedule cache is needed.
	reader.Close()
	reader, err = tasksqlite.Open(s.path)
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()
	d, err = reader.Detail(ctx, "task:planning", "")
	if err != nil || d.Schedule == nil || d.Schedule.Instruction != "Review renewals every morning" {
		t.Fatal("configuration not durable", d, err)
	}
	exec(`INSERT INTO memory_privacy(agent_id,epoch,history_floor,updated_at) SELECT agent_id,1,0,500 FROM agent_config WHERE singleton=1`)
	d, err = reader.Detail(ctx, "task:planning", "")
	if err != nil || d.Schedule == nil || !d.Schedule.Restricted || d.Schedule.Instruction != "" || d.Schedule.Spec != "" || d.Schedule.NextAt != "" || d.Schedule.Below != nil {
		t.Fatal("revoked schedule intent leaked", d.Schedule, err)
	}
	schedules, _, err = reader.ScheduledPage(ctx, "", "")
	if err != nil || len(schedules) != 2 || !schedules[0].Restricted || schedules[0].Instruction != "" || schedules[0].Timezone != "" {
		t.Fatal("revoked upcoming intent leaked", schedules, err)
	}
	filtered, _, err := reader.ScheduledPage(ctx, "Prepare", "")
	if err != nil || len(filtered) != 0 {
		t.Fatal("search exposed revoked schedule intent", filtered, err)
	}
}

func TestScheduledWorkIsBoundedAndRetainsNoNextOccurrence(t *testing.T) {
	s, _, _, _ := childFixture(t)
	_, err := s.db.Exec(`WITH RECURSIVE n(value) AS (SELECT 1 UNION ALL SELECT value+1 FROM n WHERE value<52)
 INSERT INTO automation(automation_id,agent_id,instruction,trigger_type,trigger_spec,timezone,enabled,next_fire_at,created_at,updated_at,version)
 SELECT 'automation:'||value,agent_id,'Upcoming '||value,'cron','0 8 * * *','UTC',1,value,100,100,1 FROM n,agent_config WHERE singleton=1`)
	if err != nil {
		t.Fatal(err)
	}
	_, err = s.db.Exec(`INSERT INTO automation(automation_id,agent_id,instruction,trigger_type,trigger_spec,timezone,enabled,created_at,updated_at,version)
 SELECT 'automation:ended',agent_id,'Ended single occurrence','at','2026-11-03T08:00:00Z','UTC',1,100,100,1 FROM agent_config WHERE singleton=1`)
	if err != nil {
		t.Fatal(err)
	}
	reader := s.TaskReader()
	rows, next, err := reader.ScheduledPage(context.Background(), "", "")
	if err != nil || len(rows) != 50 {
		t.Fatal("unbounded schedule read", len(rows), err)
	}
	older, end, err := reader.ScheduledPage(context.Background(), "", next)
	if err != nil || next == "" || len(older) != 3 || end != "" {
		t.Fatal("schedule continuation lost bounded tail", next, len(older), end, err)
	}
	seen := map[string]bool{}
	for _, row := range append(rows, older...) {
		if seen[row.Ref] {
			t.Fatalf("duplicate schedule %s", row.Ref)
		}
		seen[row.Ref] = true
	}
	rows, _, err = reader.ScheduledPage(context.Background(), "Ended", "")
	if err != nil || len(rows) != 1 || rows[0].NextAt != "" || rows[0].Type != "at" || !rows[0].Enabled || rows[0].TaskRef != "" {
		t.Fatal("no-next occurrence became invented future work", rows, err)
	}
	if _, _, err = reader.ScheduledPage(context.Background(), strings.Repeat("x", 301), ""); !errors.Is(err, task.ErrInvalid) {
		t.Fatal("unbounded search accepted", err)
	}
}

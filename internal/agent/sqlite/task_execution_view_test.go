package sqlite

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/z-chenhao/assistant/internal/llm"
	"github.com/z-chenhao/assistant/internal/task"
)

func TestExecutionRecordsKeepHistoricalRoundsSeparateFromTaskConfiguration(t *testing.T) {
	ctx := context.Background()
	s, _, a, p := taskFixture(t)
	p.respond = func(req llm.GenerationRequest) (llm.GenerationResult, error) {
		if isRoot(req) {
			return llm.GenerationResult{Text: "PRIVATE_RAW_WORKER_RESULT"}, nil
		}
		last := req.Messages[len(req.Messages)-1]
		if last.Role == "tool" || isFollowup(req) {
			return llm.GenerationResult{Text: "Main finalized result"}, nil
		}
		if last.Text == "append" {
			var ref string
			if err := s.db.QueryRow(`SELECT task_ref FROM task LIMIT 1`).Scan(&ref); err != nil {
				return llm.GenerationResult{}, err
			}
			raw, _ := json.Marshal(task.ManageRequest{Operation: "append", TaskRef: ref, Instruction: "New instructions for round two"})
			return calls(task.ManageName, string(raw)), nil
		}
		return calls(task.ManageName, `{"operation":"create","title":"Compare agent harnesses","task":"Compare the exact upstream versions. Preserve source links and report uncertainty.","completionPolicy":"ongoing"}`), nil
	}
	mustSubmit(t, a, "create-labelled", "create")
	mustDrain(t, a)
	mustSubmit(t, a, "second-round", "append")
	mustDrain(t, a)
	if _, err := s.db.Exec(`UPDATE task SET progress_summary='Current round progress',version=version+1`); err != nil {
		t.Fatal(err)
	}
	reader := s.TaskReader()
	before := scalar(t, s, `SELECT total_changes()`)
	configs, err := reader.List(ctx, task.ListQuery{View: "open"})
	if err != nil || len(configs.Items) != 1 || configs.Items[0].Title != "Compare agent harnesses" || configs.Items[0].Status != "active" {
		t.Fatal("configuration lost its short label or lifecycle", configs, err)
	}
	open, err := reader.Executions(ctx, task.ListQuery{View: "open"})
	if err != nil || len(open.Items) != 0 {
		t.Fatal("finished round was advertised as running because its Task remains active", open, err)
	}
	first, err := reader.Executions(ctx, task.ListQuery{View: "closed", Limit: 1})
	if err != nil || len(first.Items) != 1 || first.Next == "" {
		t.Fatal("round paging missing", first, err)
	}
	second, err := reader.Executions(ctx, task.ListQuery{View: "closed", Limit: 1, Cursor: first.Next})
	if err != nil || len(second.Items) != 1 || second.Next != "" || second.Items[0].ID == first.Items[0].ID || second.Items[0].Epoch >= first.Items[0].Epoch {
		t.Fatal("old round was replaced or repeated", second, err)
	}
	old, err := reader.Execution(ctx, second.Items[0].ID)
	if err == nil && (old.ProgressSummary != "" || len(old.RecentRuns) != 2 || old.RecentRuns[0].Epoch != 2 || old.RecentRuns[1].Epoch != 1) {
		t.Fatal("historical run imported current progress or lost scoped run history", old)
	}
	if err != nil || old.Epoch != 1 || len(old.Instructions) != 0 || !strings.Contains(old.Description, "upstream versions") {
		t.Fatal("old detail used newer instructions", old, err)
	}
	raw, _ := json.Marshal(old)
	if strings.Contains(string(raw), "PRIVATE_RAW_WORKER_RESULT") {
		t.Fatal("raw worker result became a user delivery")
	}
	latest, err := reader.Execution(ctx, first.Items[0].ID)
	if err == nil && latest.ProgressSummary != "Current round progress" {
		t.Fatal("current run lost its accepted progress", latest)
	}
	if err != nil || len(latest.Instructions) != 1 || latest.Instructions[0].Text != "New instructions for round two" {
		t.Fatal("current round instructions lost", latest, err)
	}
	if _, err = reader.Executions(ctx, task.ListQuery{View: "open", Cursor: first.Next}); !errors.Is(err, task.ErrInvalid) {
		t.Fatal("cursor escaped its view", err)
	}
	found, err := reader.Executions(ctx, task.ListQuery{View: "all", Query: "source links"})
	if err != nil || len(found.Items) != 2 {
		t.Fatal("brief search failed", found, err)
	}
	if _, err = reader.Execution(ctx, "outside"); !errors.Is(err, task.ErrNotFound) {
		t.Fatal("foreign identity exposed", err)
	}

	all, err := reader.Executions(ctx, task.ListQuery{})
	if err != nil || len(all.Items) != 2 {
		t.Fatal("default executions hid finished rounds", all, err)
	}
	if _, err = reader.Executions(ctx, task.ListQuery{View: "closed", NeedsMe: true, Cursor: first.Next}); !errors.Is(err, task.ErrInvalid) {
		t.Fatal("cursor escaped its attention condition", err)
	}
	intersection, err := reader.Executions(ctx, task.ListQuery{View: "closed", NeedsMe: true})
	if err != nil || len(intersection.Items) != 0 {
		t.Fatal("attention filter ignored", intersection, err)
	}
	if scalar(t, s, `SELECT total_changes()`) != before {
		t.Fatal("browsing mutated task or execution state")
	}
}

func TestFinishedExecutionCanIndependentlyNeedAttention(t *testing.T) {
	s, _, a, p, ref := feedbackFixture(t)
	requestFeedback(t, s, a, p, ref)
	q := task.ListQuery{View: "closed", NeedsMe: true}
	list, err := s.TaskReader().Executions(context.Background(), q)
	if err != nil || len(list.Items) != 1 || list.Items[0].Status != "completed" || !list.Items[0].NeedsAttention {
		t.Fatal("finished round with feedback disappeared", list, err)
	}
	q.View = "open"
	list, err = s.TaskReader().Executions(context.Background(), q)
	if err != nil || len(list.Items) != 0 {
		t.Fatal("attention became an execution status", list, err)
	}
}

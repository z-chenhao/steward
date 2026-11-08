package sqlite

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/z-chenhao/assistant/internal/llm"
	"github.com/z-chenhao/assistant/internal/task"
)

func TestRootFailureNoticeSurvivesUnrelatedMainReplyAndRecovery(t *testing.T) {
	s, r, a, p := taskFixture(t)
	p.respond = func(req llm.GenerationRequest) (llm.GenerationResult, error) {
		if isRoot(req) {
			return llm.GenerationResult{}, llm.InvalidProviderResponse
		}
		if isFollowup(req) {
			return llm.GenerationResult{Text: "Unrelated policy research is still underway."}, nil
		}
		if req.Messages[len(req.Messages)-1].Role == "tool" {
			return llm.GenerationResult{Text: "Research accepted"}, nil
		}
		return calls(task.ManageName, `{"operation":"create","title":"AI ad evaluation","task":"Investigate AI advertising evaluation methods"}`), nil
	}
	mustSubmit(t, a, "failed-research", "Research AI advertising evaluation")
	mustDrain(t, a)
	if scalar(t, s, `SELECT count(*) FROM task WHERE status='active'`) != 1 || scalar(t, s, `SELECT count(*) FROM run WHERE worker_position='root' AND status='failed'`) != 1 {
		t.Fatal("failure changed task lifecycle")
	}
	if scalar(t, s, `SELECT count(*) FROM notification_intent WHERE intent='failure'`) != 1 {
		t.Fatal("ignored Main update hid the Root failure")
	}
	var raw string
	if err := s.db.QueryRow(`SELECT i.message_json FROM notification_intent n JOIN conversation_intent i USING(message_id) WHERE n.intent='failure'`).Scan(&raw); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(raw, "AI ad evaluation") || strings.Contains(raw, "INVALID_PROVIDER_RESPONSE") || strings.Contains(raw, "Unrelated policy") {
		t.Fatal("failure notice lost task identity or leaked raw provider detail", raw)
	}
	var inbox string
	if err := s.db.QueryRow(`SELECT message_json FROM main_inbox`).Scan(&inbox); err != nil {
		t.Fatal(err)
	}
	var projection struct{ Details task.UpdateDetails }
	if err := json.Unmarshal([]byte(inbox), &projection); err != nil {
		t.Fatal(err)
	}
	if projection.Details.Title != "AI ad evaluation" {
		t.Fatal("Main update lacks task title")
	}
	executions, err := s.TaskReader().Executions(context.Background(), task.ListQuery{View: "closed", NeedsMe: true})
	if err != nil || len(executions.Items) != 1 || executions.Items[0].Status != "failed" {
		t.Fatal("failed execution advertised as working", executions, err)
	}
	count := scalar(t, s, `SELECT count(*) FROM delivery_request`)
	for range 2 {
		if err := r.Recover(context.Background()); err != nil {
			t.Fatal(err)
		}
		mustDrain(t, a)
	}
	if scalar(t, s, `SELECT count(*) FROM notification_intent WHERE intent='failure'`) != 1 || scalar(t, s, `SELECT count(*) FROM delivery_request`) != count {
		t.Fatal("recovery duplicated failure delivery")
	}
}

package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"testing"

	"github.com/z-chenhao/assistant/internal/agent"
	"github.com/z-chenhao/assistant/internal/llm"
	"github.com/z-chenhao/assistant/internal/skill"
)

func TestExperienceAuthorizedCorrectionsBecomeOneRejectableCandidate(t *testing.T) {
	ctx := context.Background()
	s, r, a, p := evidenceFixture(t)
	h := installExperiencePlugin(t, s, r)
	p.respond = func(llm.GenerationRequest) (llm.GenerationResult, error) {
		return llm.GenerationResult{Text: "Acknowledged."}, nil
	}
	c := skill.Config{Root: t.TempDir(), Scope: "agent"}
	const note = "/learn-correction release-check\nVerify the release before publishing.\nReview the staged release.\nRun the focused validation."
	count := func() int { return scalar(t, s, "SELECT count(*) FROM skill_candidate") }
	mustSubmit(t, a, "ordinary", "Please correct the release.")
	mustDrain(t, a)
	first := mustSubmit(t, a, "first", note)
	retry := mustSubmit(t, a, "first", note)
	other := mustSubmit(t, a, "same-run", note)
	if first.MessageID != retry.MessageID || first.RunID != other.RunID || count() != 0 {
		t.Fatal("same event or same run counted twice")
	}
	mustDrain(t, a)
	second := mustSubmit(t, a, "second", note)
	if first.RunID == second.RunID || count() != 1 {
		t.Fatal("independent reports did not create exactly one candidate")
	}
	var id string
	var version int64
	if e := s.db.QueryRow("SELECT candidate_id,version FROM skill_candidate").Scan(&id, &version); e != nil {
		t.Fatal(e)
	}
	q := skill.ReviewRequest{CandidateRef: "experience:" + id, Version: version, Operation: "view"}
	view, e := r.ReviewExperience(ctx, c, q)
	if e != nil {
		t.Fatal(e)
	}
	refs := view["source_refs"].([]string)
	if len(refs) != 2 || refs[0] != "conversation:"+first.MessageID || refs[1] != "conversation:"+second.MessageID || view["status"] != "candidate" {
		t.Fatal("independent evidence lost", view)
	}
	q.Operation = "promote"
	if _, e = r.ReviewExperience(ctx, c, q); !errors.Is(e, agent.ErrConflict) {
		t.Fatal("unverified candidate promoted", e)
	}
	q.Operation = "reject"
	if _, e = r.ReviewExperience(ctx, c, q); e != nil {
		t.Fatal(e)
	}
	mustDrain(t, a)
	mustSubmit(t, a, "third", note)
	mustDrain(t, a)
	if count() != 1 || scalar(t, s, "SELECT count(*) FROM skill_candidate WHERE status='rejected'") != 1 {
		t.Fatal("rejection was replayed")
	}
	status, _ := h.Store.Status(ctx)
	if _, e = h.Store.Deactivate(ctx, "stop-corrections", status.Revision); e != nil {
		t.Fatal(e)
	}
	if _, e = a.Submit(ctx, "after-stop", note); e == nil {
		t.Fatal("new correction accepted after stop")
	}
	original, e := a.Submit(ctx, "second", note)
	if e != nil || original.MessageID != second.MessageID {
		t.Fatal("accepted replay lost", original, e)
	}
}

func TestExperienceForgetSeparatesIndependentCorrectionEvidence(t *testing.T) {
	ctx := context.Background()
	s, r, a, p := evidenceFixture(t)
	installExperiencePlugin(t, s, r)
	p.respond = func(llm.GenerationRequest) (llm.GenerationResult, error) {
		return llm.GenerationResult{Text: "Acknowledged."}, nil
	}
	note := "/learn-correction prior-check\nReview a corrected action.\nReview the original evidence.\nRun the focused check."
	mustSubmit(t, a, "before", note)
	mustDrain(t, a)
	forget := func() {
		t.Helper()
		owner := idAgent(t, s)
		if err := r.write(ctx, func(tx *sql.Tx) error { return r.revokeMemory(ctx, tx, owner) }); err != nil {
			t.Fatal(err)
		}
	}
	forget()
	mustSubmit(t, a, "after", note)
	mustDrain(t, a)
	if scalar(t, s, "SELECT count(*) FROM skill_candidate") != 0 {
		t.Fatal("forgotten source counted as independent evidence")
	}
	mustSubmit(t, a, "after-again", note)
	mustDrain(t, a)
	config := skill.Config{Root: t.TempDir(), Scope: "agent"}
	list, err := r.ReviewExperience(ctx, config, skill.ReviewRequest{Operation: "list"})
	if err != nil {
		t.Fatal(err)
	}
	items := list["candidates"].([]map[string]any)
	if len(items) != 1 {
		t.Fatal(items)
	}
	forget()
	list, err = r.ReviewExperience(ctx, config, skill.ReviewRequest{Operation: "list"})
	if err != nil || len(list["candidates"].([]map[string]any)) != 0 {
		t.Fatal("redacted candidate listed", list, err)
	}
	_, err = r.ReviewExperience(ctx, config, skill.ReviewRequest{Operation: "view", CandidateRef: items[0]["candidate_ref"].(string), Version: 1})
	if !errors.Is(err, agent.ErrConflict) {
		t.Fatal("forgotten prior source was readable", err)
	}
}

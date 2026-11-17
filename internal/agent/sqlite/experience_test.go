package sqlite

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/z-chenhao/assistant/internal/agent"
	"github.com/z-chenhao/assistant/internal/llm"
	"github.com/z-chenhao/assistant/internal/skill"
	"github.com/z-chenhao/assistant/internal/tool"
)

func TestExperienceRequiresIndependentVerifiedRunsAndCAS(t *testing.T) {
	testExperienceLifecycle(t, false)
}
func TestExperiencePluginKeepsEvidenceCASAndPublicationRecovery(t *testing.T) {
	testExperienceLifecycle(t, true)
}
func testExperienceLifecycle(t *testing.T, managed bool) {
	ctx := context.Background()
	spec := tool.Spec{Name: "builtin__bash", Version: 1, Permission: "external_effect", Risk: "high", ReplayPolicy: "never", TimeoutMS: 10000, OutputBytes: 16384, Provider: "fixture", ConflictPolicy: "none", InputSchema: `{"type":"object","properties":{"command":{"type":"string"}},"required":["command"],"additionalProperties":false}`}
	definition := tool.Definition{Spec: spec, Invoke: func(context.Context, tool.Request) tool.Outcome {
		return tool.Outcome{Result: tool.Result{Text: `{"exit_code":0,"timed_out":false,"command":"go test -race ./...","truncated":false}`}}
	}}
	s, r, a, p := childFixture(t, definition)
	if managed {
		installExperiencePlugin(t, s, r)
	}
	execute := func(req llm.GenerationRequest) (llm.GenerationResult, error) {
		if req.Messages[len(req.Messages)-1].Role == "tool" {
			return llm.GenerationResult{Text: "Verified correction"}, nil
		}
		return calls(spec.Name, `{"command":"go test -race ./..."}`), nil
	}
	p.respond = delegateToolWork(execute)
	one := mustSubmit(t, a, "experience-1", "Verify the corrected method")
	mustDrain(t, a)
	approvePolicy(t, s, r)
	mustDrain(t, a)
	if err := s.db.QueryRow(`SELECT run_id FROM run WHERE kind='worker' ORDER BY created_at LIMIT 1`).Scan(&one.RunID); err != nil {
		t.Fatal(err)
	}
	two := mustSubmit(t, a, "experience-2", "Independently repeat the corrected method")
	mustDrain(t, a)
	views, err := r.Approvals(ctx)
	if err != nil {
		t.Fatal(err)
	}
	d, err := s.Diagnostics(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, v := range views {
		if v.Status == "pending" {
			if err := r.Decide(ctx, d.AgentID, v.ID, v.Hash, "approved"); err != nil {
				t.Fatal(err)
			}
		}
	}
	mustDrain(t, a)
	if err := s.db.QueryRow(`SELECT run_id FROM run WHERE kind='worker' ORDER BY created_at DESC LIMIT 1`).Scan(&two.RunID); err != nil {
		t.Fatal(err)
	}
	c := skill.Config{Root: t.TempDir(), Scope: "agent"}
	q := skill.ReviewRequest{Operation: "propose", Name: "verified---tests", Description: "Review code --- safely.", Procedure: "1. Read AGENTS.md and reproduce the failure.\n2. Make the smallest correction.\n3. Run go test -race ./... and inspect the result.", Reason: "success", SourceRefs: []string{"run:" + one.RunID}}
	value, e := r.ReviewExperience(ctx, c, q)
	if e != nil {
		t.Fatal(e)
	}
	ref := value["candidate_ref"].(string)
	var inv1, inv2 string
	if e = s.db.QueryRow(`SELECT invocation_id FROM tool_invocation WHERE run_id=?`, one.RunID).Scan(&inv1); e != nil {
		t.Fatal(e)
	}
	if e = s.db.QueryRow(`SELECT invocation_id FROM tool_invocation WHERE run_id=?`, two.RunID).Scan(&inv2); e != nil {
		t.Fatal(e)
	}
	verify := func(version int64, inv string) map[string]any {
		v, e := r.ReviewExperience(ctx, c, skill.ReviewRequest{Operation: "verify", CandidateRef: ref, Version: version, VerificationRef: "invocation:" + inv})
		if e != nil {
			t.Fatal(e)
		}
		return v
	}
	value = verify(1, inv1)
	if value["status"] != "candidate" {
		t.Fatal("single ordinary success promoted", value)
	}
	if _, e = r.ReviewExperience(ctx, c, skill.ReviewRequest{Operation: "promote", CandidateRef: ref, Version: 2}); e == nil {
		t.Fatal("unverified candidate promoted")
	}
	value = verify(2, inv2)
	if value["status"] != "verified" {
		t.Fatal(value)
	}
	if _, e = r.ReviewExperience(ctx, c, skill.ReviewRequest{Operation: "promote", CandidateRef: ref, Version: 2}); e == nil {
		t.Fatal("stale CAS")
	}
	for i := 0; i < 128; i++ {
		name := fmt.Sprintf("fixture-%03d", i)
		if e := os.Mkdir(filepath.Join(c.Root, name), 0700); e != nil {
			t.Fatal(e)
		}
		if e := os.WriteFile(filepath.Join(c.Root, name, "SKILL.md"), []byte("---\nname: "+name+"\ndescription: Fixture.\n---\nMethod.\n"), 0600); e != nil {
			t.Fatal(e)
		}
	}
	if _, e = r.ReviewExperience(ctx, c, skill.ReviewRequest{Operation: "promote", CandidateRef: ref, Version: 3}); !errors.Is(e, agent.ErrResourceLimit) {
		t.Fatal("full catalog did not reject promotion with RESOURCE_LIMIT", e)
	}
	var status string
	var version int64
	if e := s.db.QueryRow(`SELECT status,version FROM skill_candidate WHERE name=?`, q.Name).Scan(&status, &version); e != nil || status != "verified" || version != 3 {
		t.Fatal("capacity rejection committed promotion", status, version, e)
	}
	if _, e := skill.Definitions(c); e != nil {
		t.Fatal("rejected promotion broke startup", e)
	}
	if e := os.Rename(filepath.Join(c.Root, "fixture-000"), filepath.Join(c.Root, ".steward-retired-fixture")); e != nil {
		t.Fatal(e)
	}
	if _, e := s.db.Exec(`CREATE TRIGGER fail_experience_commit BEFORE UPDATE OF status ON skill_candidate
WHEN NEW.status='promoted' BEGIN SELECT RAISE(ABORT,'injected status commit failure'); END`); e != nil {
		t.Fatal(e)
	}
	if _, e = r.ReviewExperience(ctx, c, skill.ReviewRequest{Operation: "promote", CandidateRef: ref, Version: 3}); e == nil {
		t.Fatal("status commit failure not injected")
	}
	if e := s.db.QueryRow(`SELECT status,version FROM skill_candidate WHERE name=?`, q.Name).Scan(&status, &version); e != nil || status != "verified" || version != 3 {
		t.Fatal("failed commit advanced candidate", status, version, e)
	}
	if _, e := os.Stat(filepath.Join(c.Root, q.Name, "SKILL.md")); e != nil {
		t.Fatal("fault did not reach post-publication boundary", e)
	}
	if _, e := s.db.Exec(`DROP TRIGGER fail_experience_commit`); e != nil {
		t.Fatal(e)
	}
	value, e = r.ReviewExperience(ctx, c, skill.ReviewRequest{Operation: "promote", CandidateRef: ref, Version: 3})
	if e != nil || value["status"] != "promoted" {
		t.Fatal(value, e)
	}
	defs, e := skill.Definitions(c)
	if e != nil {
		t.Fatal(e)
	}
	var summaries []skill.Summary
	if json.Unmarshal([]byte(defs[0].Execute(ctx, `{"limit":128}`).Text), &summaries) != nil || len(summaries) != 128 || summaries[127].Name != q.Name || summaries[127].Description != q.Description {
		t.Fatal(summaries)
	}
	for _, d := range defs {
		if d.Spec.Name == skill.ManageName {
			t.Fatal("ordinary Loop gained management")
		}
	}
	value, e = r.ReviewExperience(ctx, c, skill.ReviewRequest{Operation: "retire", CandidateRef: ref, Version: 4})
	if e != nil || value["status"] != "retired" {
		t.Fatal(value, e)
	}
	defs, e = skill.Definitions(c)
	if e != nil || json.Unmarshal([]byte(defs[0].Execute(ctx, `{"limit":128}`).Text), &summaries) != nil || len(summaries) != 127 {
		t.Fatal(defs, e)
	}
}

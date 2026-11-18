package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/z-chenhao/assistant/internal/agent"
	"github.com/z-chenhao/assistant/internal/llm"
	"github.com/z-chenhao/assistant/internal/plugin/experiencelearning/contract"
	"github.com/z-chenhao/assistant/internal/rsi"
	"github.com/z-chenhao/assistant/internal/skill"
	"github.com/z-chenhao/assistant/internal/tool"
	"github.com/z-chenhao/assistant/pkg/rsiplugin"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func installExperiencePlugin(t *testing.T, s *Store, r *Runs) rsi.Host {
	t.Helper()
	policy := rsi.Policy{ID: "experience-owner-fixture", AllowedContracts: []string{contract.Contract, contract.Demonstration}}
	for _, reason := range []string{"success", "correction", "recovered_failure"} {
		n := 1
		if reason == "success" {
			n = 2
		}
		input, _ := json.Marshal(contract.Request{Reason: reason})
		expected, _ := json.Marshal(contract.Policy{RequiredRuns: n, Introduction: ""})
		policy.Cases = append(policy.Cases, rsi.Case{InstanceID: "experience-learning", Contract: contract.Contract, Input: input, Expected: expected})
	}
	policy.Cases = append(policy.Cases, rsi.Case{InstanceID: "experience-learning", Contract: contract.Demonstration, Input: json.RawMessage(`{"name":"Preview","steps":[{"action":"activate","label":"Preview"}],"sample":[],"goal":0}`), Expected: json.RawMessage(`{"steps":["1. Activate \"Preview\"."],"matched":0,"verified":false}`)})
	return installRuntimeFeature(t, s, r, "experience-learning", policy)
}
func TestExperiencePluginCannotLowerTrustedEvidenceFloor(t *testing.T) {
	for _, reason := range []string{"success", "correction", "recovered_failure"} {
		for _, count := range []int{-1, 0, 1, 2, 3, 129} {
			valid := count >= minimumExperienceRuns(reason) && count <= 128
			if validExperiencePolicy(reason, contract.Policy{RequiredRuns: count}) != valid {
				t.Fatal("invalid evidence threshold", reason, count)
			}
		}
	}
	if validExperiencePolicy("success", contract.Policy{RequiredRuns: 2, Introduction: string([]byte{0xff})}) {
		t.Fatal("invalid private method bytes accepted")
	}
}
func TestExperiencePluginDisabledPreventsNewRecipe(t *testing.T) {
	ctx := context.Background()
	s, r, _, _ := evidenceFixture(t)
	h := installExperiencePlugin(t, s, r)
	status, err := h.Store.Status(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = h.Store.Deactivate(ctx, "experience-disable", status.Revision); err != nil {
		t.Fatal(err)
	}
	if _, err = r.prepareExperiencePolicy(ctx, "success"); err != errExperienceUnavailable {
		t.Fatal("disabled policy fell back to native", err)
	}
}

func experienceProofRuntime(t *testing.T, count int) (*Store, *Runs, []string, []string) {
	t.Helper()
	ctx := context.Background()
	spec := tool.Spec{Name: "builtin__bash", Version: 1, Permission: "external_effect", Risk: "high", ReplayPolicy: "never", TimeoutMS: 10000, OutputBytes: 16384, Provider: "fixture", ConflictPolicy: "none", InputSchema: `{"type":"object","properties":{"command":{"type":"string"}},"required":["command"],"additionalProperties":false}`}
	definition := tool.Definition{Spec: spec, Invoke: func(context.Context, tool.Request) tool.Outcome {
		return tool.Outcome{Result: tool.Result{Text: `{"exit_code":0,"timed_out":false,"command":"go test -race ./...","truncated":false}`}}
	}}
	s, r, a, p := childFixture(t, definition)
	p.respond = delegateToolWork(func(req llm.GenerationRequest) (llm.GenerationResult, error) {
		if req.Messages[len(req.Messages)-1].Role == "tool" {
			return llm.GenerationResult{Text: "verified method"}, nil
		}
		return calls(spec.Name, `{"command":"go test -race ./..."}`), nil
	})
	runs, proofs := []string{}, []string{}
	for i := 0; i < count; i++ {
		mustSubmit(t, a, fmt.Sprintf("proof-%d", i), "verify the reusable method independently")
		mustDrain(t, a)
		approvals, err := r.Approvals(ctx)
		if err != nil {
			t.Fatal(err)
		}
		owner := idAgent(t, s)
		for _, approval := range approvals {
			if approval.Status == "pending" {
				if err = r.Decide(ctx, owner, approval.ID, approval.Hash, "approved"); err != nil {
					t.Fatal(err)
				}
			}
		}
		mustDrain(t, a)
		var run, proof string
		if err = s.db.QueryRow(`SELECT i.run_id,i.invocation_id FROM tool_invocation i JOIN run r USING(run_id) WHERE i.tool_name='builtin__bash' AND r.status='completed' ORDER BY i.created_at DESC LIMIT 1`).Scan(&run, &proof); err != nil {
			t.Fatal(err)
		}
		runs = append(runs, run)
		proofs = append(proofs, proof)
	}
	return s, r, runs, proofs
}

func experienceVersionPackage(t *testing.T, version string) string {
	t.Helper()
	source := t.TempDir()
	path, err := filepath.Abs("../../plugin/experiencelearning/plugin.go")
	if err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	code := strings.Replace(string(raw), `Version: "1.0.2"`, `Version: "`+version+`"`, 1)
	if version == "1.1.0" {
		code = strings.Replace(code, `n = 2`, `n = 3`, 1)
		code = strings.Replace(code, `Introduction: ""`, `Introduction: "Independently reviewed reusable method."`, 1)
	} else {
		code = strings.Replace(code, `n = 2`, `n = 1`, 1)
	}
	override := filepath.Join(t.TempDir(), "plugin.go")
	if err = os.WriteFile(override, []byte(code), 0600); err != nil {
		t.Fatal(err)
	}
	overlay, _ := json.Marshal(struct{ Replace map[string]string }{map[string]string{path: override}})
	file := filepath.Join(t.TempDir(), "overlay.json")
	if err = os.WriteFile(file, overlay, 0600); err != nil {
		t.Fatal(err)
	}
	if out, err := exec.Command("go", "build", "-overlay", file, "-o", filepath.Join(source, "plugin"), "../../../cmd/plugins/experience-learning").CombinedOutput(); err != nil {
		t.Fatal(string(out), err)
	}
	raw, err = os.ReadFile("../../../plugins/experience-learning/steward.plugin.json")
	if err != nil {
		t.Fatal(err)
	}
	var m rsiplugin.Manifest
	if err = rsiplugin.Decode(raw, &m); err != nil {
		t.Fatal(err)
	}
	m.Version = version
	m, err = rsi.Seal(source, m)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ = json.Marshal(m)
	if err = os.WriteFile(filepath.Join(source, "steward.plugin.json"), raw, 0600); err != nil {
		t.Fatal(err)
	}
	return source
}
func TestExperiencePluginIndependentPolicyCandidateFreezeAndRollback(t *testing.T) {
	ctx := context.Background()
	s, r, runs, proofs := experienceProofRuntime(t, 3)
	config := skill.Config{Root: t.TempDir(), Scope: "agent"}
	proposal := skill.ReviewRequest{Operation: "propose", Name: "independent-method", Description: "Review a reusable procedure.", Procedure: "1. Reproduce the failure from original evidence.\n2. Apply a minimal fix and independently verify it.", Reason: "success", SourceRefs: []string{"run:" + runs[0]}}
	native, err := r.ReviewExperience(ctx, config, proposal)
	if err != nil {
		t.Fatal(err)
	}
	h := installExperiencePlugin(t, s, r)
	first, err := h.Store.Status(ctx)
	if err != nil {
		t.Fatal(err)
	}
	old, err := r.ReviewExperience(ctx, config, proposal)
	if err != nil {
		t.Fatal(err)
	}
	tasks := scalar(t, s, `SELECT count(*) FROM task`)
	pkg, err := h.Install(ctx, experienceVersionPackage(t, "1.1.0"))
	if err != nil {
		t.Fatal(err)
	}
	graph, err := h.Prepare(ctx, rsiplugin.Graph{Protocol: rsiplugin.Protocol, Bindings: []rsiplugin.Binding{{InstanceID: "experience-learning", PackageDigest: pkg.Digest, Config: json.RawMessage(`{}`), StateNamespace: "experience-learning", StateVersion: 1}}})
	if err != nil {
		t.Fatal(err)
	}
	policy, err := h.Store.Policy(ctx, "experience-owner-fixture")
	if err != nil {
		t.Fatal(err)
	}
	policy.ID = "experience-owner-v11"
	for i := range policy.Cases {
		if policy.Cases[i].Contract != contract.Contract {
			continue
		}
		var expected contract.Policy
		if err = json.Unmarshal(policy.Cases[i].Expected, &expected); err != nil {
			t.Fatal(err)
		}
		if expected.RequiredRuns == 2 {
			expected.RequiredRuns = 3
		}
		expected.Introduction = "Independently reviewed reusable method."
		policy.Cases[i].Expected, _ = json.Marshal(expected)
	}
	if err = h.Store.PutPolicy(ctx, policy); err != nil {
		t.Fatal(err)
	}
	choose := func(id, baseline, target, policyID string) {
		t.Helper()
		if err := h.Propose(ctx, rsi.Candidate{ID: id, Baseline: baseline, Target: target, PolicyID: policyID, Goal: "independent reusable-method recipe"}); err != nil {
			t.Fatal(err)
		}
		decision, err := h.Verify(ctx, id)
		if err != nil {
			t.Fatal(err)
		}
		if _, err = h.Activate(ctx, rsi.Activation{OperationID: id, Expected: baseline, Target: target, DecisionID: decision}); err != nil {
			t.Fatal(err)
		}
	}
	choose("experience-upgrade", first.Revision, graph.Revision, policy.ID)
	current, err := r.ReviewExperience(ctx, config, proposal)
	if err != nil {
		t.Fatal(err)
	}
	if current["candidate_ref"] == old["candidate_ref"] || native["candidate_ref"] == old["candidate_ref"] {
		t.Fatal("new policy rewrote a previous candidate", native, old, current)
	}
	id := strings.TrimPrefix(current["candidate_ref"].(string), "experience:")
	var original, body, revision string
	var needed int
	if err = s.db.QueryRow(`SELECT b.original_procedure,c.procedure,b.graph_revision,b.required_runs FROM experience_policy_binding b JOIN skill_candidate c USING(candidate_id) WHERE c.candidate_id=?`, id).Scan(&original, &body, &revision, &needed); err != nil || original != proposal.Procedure || body != "Independently reviewed reusable method.\n\n"+proposal.Procedure || revision != graph.Revision || needed != 3 {
		t.Fatal("policy/source provenance lost", original, body, revision, needed, err)
	}
	choose("experience-rollback", graph.Revision, first.Revision, "experience-owner-fixture")
	if err = h.Store.Retire(ctx, pkg.Digest); err != nil {
		t.Fatal(err)
	}
	verify := func(value map[string]any, n int) map[string]any {
		t.Helper()
		for i := 0; i < n; i++ {
			var err error
			value, err = r.ReviewExperience(ctx, config, skill.ReviewRequest{Operation: "verify", CandidateRef: value["candidate_ref"].(string), Version: value["version"].(int64), VerificationRef: "invocation:" + proofs[i]})
			if err != nil {
				t.Fatal(err)
			}
		}
		return value
	}
	native = verify(native, 2)
	old = verify(old, 2)
	current = verify(current, 2)
	if native["status"] != "verified" || old["status"] != "verified" || current["status"] != "candidate" {
		t.Fatal("accepted recipes changed across rollback", native, old, current)
	}
	current, err = r.ReviewExperience(ctx, config, skill.ReviewRequest{Operation: "verify", CandidateRef: current["candidate_ref"].(string), Version: current["version"].(int64), VerificationRef: "invocation:" + proofs[2]})
	if err != nil || current["status"] != "verified" {
		t.Fatal("retired recipe failed to drain", current, err)
	}
	if err = h.Store.Quarantine(ctx, graph.Revision); err != nil {
		t.Fatal(err)
	}
	if _, err = r.ReviewExperience(ctx, config, skill.ReviewRequest{Operation: "promote", CandidateRef: current["candidate_ref"].(string), Version: current["version"].(int64)}); err != errExperienceUnavailable {
		t.Fatal("quarantined recipe published", err)
	}
	if _, err = r.ReviewExperience(ctx, config, skill.ReviewRequest{Operation: "promote", CandidateRef: old["candidate_ref"].(string), Version: old["version"].(int64)}); err != nil {
		t.Fatal(err)
	}
	if _, err = r.ReviewExperience(ctx, config, skill.ReviewRequest{Operation: "promote", CandidateRef: native["candidate_ref"].(string), Version: native["version"].(int64)}); !errors.Is(err, agent.ErrConflict) {
		t.Fatal("two candidates owned one live publication", err)
	}
	if _, err = s.db.Exec(`UPDATE experience_policy_binding SET required_runs=1 WHERE candidate_id=?`, id); err == nil {
		t.Fatal("accepted policy became mutable")
	}
	if scalar(t, s, `SELECT count(*) FROM task`) != tasks {
		t.Fatal("recipe control fabricated user work")
	}
}

func TestExperiencePluginActualCandidateCannotWeakenHostEvidence(t *testing.T) {
	ctx := context.Background()
	s, r, runs, _ := experienceProofRuntime(t, 1)
	h := installExperiencePlugin(t, s, r)
	first, err := h.Store.Status(ctx)
	if err != nil {
		t.Fatal(err)
	}
	pkg, err := h.Install(ctx, experienceVersionPackage(t, "1.2.0"))
	if err != nil {
		t.Fatal(err)
	}
	graph, err := h.Prepare(ctx, rsiplugin.Graph{Protocol: rsiplugin.Protocol, Bindings: []rsiplugin.Binding{{InstanceID: "experience-learning", PackageDigest: pkg.Digest, Config: json.RawMessage(`{}`), StateNamespace: "experience-learning", StateVersion: 1}}})
	if err != nil {
		t.Fatal(err)
	}
	policy, err := h.Store.Policy(ctx, "experience-owner-fixture")
	if err != nil {
		t.Fatal(err)
	}
	policy.ID = "experience-owner-mechanical-only"
	policy.Cases[0].Expected = json.RawMessage(`{"required_runs":1,"introduction":""}`)
	if err = h.Store.PutPolicy(ctx, policy); err != nil {
		t.Fatal(err)
	}
	if err = h.Propose(ctx, rsi.Candidate{ID: policy.ID, Baseline: first.Revision, Target: graph.Revision, PolicyID: policy.ID, Goal: "test trusted downstream evidence floor"}); err != nil {
		t.Fatal(err)
	}
	decision, err := h.Verify(ctx, policy.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = h.Activate(ctx, rsi.Activation{OperationID: policy.ID, Expected: first.Revision, Target: graph.Revision, DecisionID: decision}); err != nil {
		t.Fatal(err)
	}
	proposal := skill.ReviewRequest{Operation: "propose", Name: "unqualified-method", Description: "Reusable method.", Procedure: "1. Inspect the original evidence.\n2. Verify the complete proposed correction.", Reason: "success", SourceRefs: []string{"run:" + runs[0]}}
	if _, err = r.ReviewExperience(ctx, skill.Config{Root: t.TempDir(), Scope: "agent"}, proposal); !errors.Is(err, rsi.ErrInvalid) {
		t.Fatal("candidate weakened trusted verification floor", err)
	}
	if scalar(t, s, `SELECT count(*) FROM skill_candidate`) != 0 {
		t.Fatal("unsafe recipe created a candidate")
	}
}

func TestV55ExperiencePolicyMigrationRetainsCandidateAndMethod(t *testing.T) {
	ctx := context.Background()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	db.SetMaxOpenConns(1)
	if err = migrate(ctx, db, migrations[:55]); err != nil {
		t.Fatal(err)
	}
	method := "1. Preserve the original procedure.\n2. Verify its own evidence."
	if _, err = db.Exec(`INSERT INTO skill_candidate VALUES('legacy','owner','method','original description',?,'success','["run:legacy"]',?,'verified',1,2,3)`, method, digest(method)); err != nil {
		t.Fatal(err)
	}
	if err = migrate(ctx, db, migrations); err != nil {
		t.Fatal(err)
	}
	var managed bool
	var revision sql.NullString
	var needed int
	var original, procedure, hash, status string
	var version int64
	if err = db.QueryRow(`SELECT b.managed,b.graph_revision,b.required_runs,b.original_procedure,c.procedure,c.method_hash,c.status,c.version FROM skill_candidate c JOIN experience_policy_binding b USING(candidate_id)`).Scan(&managed, &revision, &needed, &original, &procedure, &hash, &status, &version); err != nil || managed || revision.Valid || needed != 2 || original != method || procedure != method || hash != digest(method) || status != "verified" || version != 3 {
		t.Fatal("legacy method or lifecycle changed", managed, revision, needed, original, procedure, hash, status, version, err)
	}
	if _, err = db.Exec(`UPDATE experience_policy_binding SET required_runs=1`); err == nil {
		t.Fatal("migrated policy mutable")
	}
	if _, err = db.Exec(`DELETE FROM experience_policy_binding`); err == nil {
		t.Fatal("migrated policy removed")
	}
}

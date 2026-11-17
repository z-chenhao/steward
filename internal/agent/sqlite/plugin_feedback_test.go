package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/z-chenhao/assistant/internal/artifact"
	"github.com/z-chenhao/assistant/internal/conversation"
	"github.com/z-chenhao/assistant/internal/llm"
	"github.com/z-chenhao/assistant/internal/plugin/taskfeedback/contract"
	"github.com/z-chenhao/assistant/internal/rsi"
	"github.com/z-chenhao/assistant/internal/task"
	"github.com/z-chenhao/assistant/internal/tool"
	"github.com/z-chenhao/assistant/pkg/rsiplugin"
	"testing"
)

func installFeedbackPlugin(t *testing.T, s *Store, r *Runs) rsi.Host {
	t.Helper()
	return installRuntimeFeature(t, s, r, "task-feedback", rsi.Policy{ID: "feedback-owner-fixture", AllowedContracts: []string{contract.Contract}, Cases: []rsi.Case{
		{InstanceID: "task-feedback", Contract: contract.Contract, Input: json.RawMessage(`{"kind":"input"}`), Expected: json.RawMessage(`{"lifetime_seconds":604800,"response_guidance":"If continuing through task_manage append, copy feedback_ref to feedbackRef, pass artifact_refs as artifactRefs, and use the exact Task reference;"}`)},
		{InstanceID: "task-feedback", Contract: contract.Contract, Input: json.RawMessage(`{"kind":"review"}`), Expected: json.RawMessage(`{"lifetime_seconds":604800,"response_guidance":"If continuing through task_manage append, copy feedback_ref to feedbackRef, pass artifact_refs as artifactRefs, and use the exact Task reference;"}`)},
	}})
}

func feedbackVersionPackage(t *testing.T, version string) string {
	t.Helper()
	source := t.TempDir()
	path, err := filepath.Abs("../../plugin/taskfeedback/plugin.go")
	if err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	code := strings.Replace(string(raw), `Version: "1.0.1"`, `Version: "`+version+`"`, 1)
	if version == "1.1.0" {
		code = strings.Replace(code, `LifetimeSeconds: 604800`, `LifetimeSeconds: 86400`, 1)
		code = strings.Replace(code, `ResponseGuidance: "If continuing through task_manage append, copy feedback_ref to feedbackRef, pass artifact_refs as artifactRefs, and use the exact Task reference;"`, `ResponseGuidance: "VERSION_1_1: Verify the saved scope and retain the exact Task and artifact references."`, 1)
	} else {
		code = strings.Replace(code, `LifetimeSeconds: 604800`, `LifetimeSeconds: 604801`, 1)
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
	if out, err := exec.Command("go", "build", "-overlay", file, "-o", filepath.Join(source, "plugin"), "../../../cmd/plugins/task-feedback").CombinedOutput(); err != nil {
		t.Fatal(string(out), err)
	}
	raw, err = os.ReadFile("../../../plugins/task-feedback/steward.plugin.json")
	if err != nil {
		t.Fatal(err)
	}
	var m rsiplugin.Manifest
	if err = rsiplugin.Decode(raw, &m); err != nil {
		t.Fatal(err)
	}
	m.Version = version
	if version == "1.2.0" {
		m.Provides[0].OutputSchema = json.RawMessage(strings.Replace(string(m.Provides[0].OutputSchema), `604800`, `604801`, 1))
	}
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

func activateFeedback(t *testing.T, h rsi.Host, id, baseline, target, policy string) {
	t.Helper()
	ctx := context.Background()
	if err := h.Propose(ctx, rsi.Candidate{ID: id, Baseline: baseline, Target: target, PolicyID: policy, Goal: "independent feedback lifetime and response method"}); err != nil {
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

func selectFeedbackVersion(t *testing.T, h rsi.Host, baseline, version string) (string, string) {
	t.Helper()
	ctx := context.Background()
	pkg, err := h.Install(ctx, feedbackVersionPackage(t, version))
	if err != nil {
		t.Fatal(err)
	}
	graph, err := h.Prepare(ctx, rsiplugin.Graph{Protocol: rsiplugin.Protocol, Bindings: []rsiplugin.Binding{{InstanceID: "task-feedback", PackageDigest: pkg.Digest, Config: json.RawMessage(`{}`), StateNamespace: "task-feedback", StateVersion: 1}}})
	if err != nil {
		t.Fatal(err)
	}
	policy, err := h.Store.Policy(ctx, "feedback-owner-fixture")
	if err != nil {
		t.Fatal(err)
	}
	policy.ID = "feedback-owner-" + version
	expected := contract.Policy{LifetimeSeconds: 86400, ResponseGuidance: "VERSION_1_1: Verify the saved scope and retain the exact Task and artifact references."}
	if version == "1.2.0" {
		expected = contract.Policy{LifetimeSeconds: 604801, ResponseGuidance: legacyFeedbackGuidanceV1}
	}
	for i := range policy.Cases {
		policy.Cases[i].Expected, _ = json.Marshal(expected)
	}
	if err = h.Store.PutPolicy(ctx, policy); err != nil {
		t.Fatal(err)
	}
	activateFeedback(t, h, "feedback-upgrade-"+version, baseline, graph.Revision, policy.ID)
	return graph.Revision, pkg.Digest
}

func TestFeedbackPluginIndependentUpgradeRetainsAcceptedQuestionsAndResponseData(t *testing.T) {
	ctx := context.Background()
	s, r, a, p, ref := feedbackFixture(t)
	native := requestFeedback(t, s, a, p, ref)
	h := installFeedbackPlugin(t, s, r)
	first, err := h.Store.Status(ctx)
	if err != nil {
		t.Fatal(err)
	}
	question := func(key string, review bool) task.Feedback {
		t.Helper()
		feedbackAction(t, a, p, "create-"+key, task.ManageRequest{Operation: "create", Task: "Exact original request " + key, CompletionPolicy: "ongoing"})
		var target string
		if err := s.db.QueryRow(`SELECT task_ref FROM task ORDER BY created_at DESC LIMIT 1`).Scan(&target); err != nil {
			t.Fatal(err)
		}
		if review {
			scope := artifact.Scope{UserID: artifact.LocalUser, AgentID: idAgent(t, s), Owner: true}
			artifactRef, err := s.Artifacts().Put(ctx, scope, artifact.Input{Name: "report.txt", MediaType: "text/plain"}, strings.NewReader("Exact immutable revision A"))
			if err != nil {
				t.Fatal(err)
			}
			return requestFeedback(t, s, a, p, target, artifactRef)
		}
		return requestFeedback(t, s, a, p, target)
	}
	old := question("v1", false)
	second, pkg := selectFeedbackVersion(t, h, first.Revision, "1.1.0")
	current := question("v11", true)
	// Hold an accepted Main before execution, then roll back and retire the new
	// binary. Its original Run graph still owns the pending feedback request.
	feedbackAction(t, a, p, "create-retained", task.ManageRequest{Operation: "create", Task: "Retained work", CompletionPolicy: "ongoing"})
	var target string
	if err = s.db.QueryRow(`SELECT task_ref FROM task ORDER BY created_at DESC LIMIT 1`).Scan(&target); err != nil {
		t.Fatal(err)
	}
	q := task.ManageRequest{Operation: "request_input", TaskRef: target, Prompt: "Exact retained question"}
	raw, _ := json.Marshal(q)
	p.respond = func(req llm.GenerationRequest) (llm.GenerationResult, error) {
		if req.Messages[len(req.Messages)-1].Role == "tool" || isRoot(req) || isFollowup(req) {
			return llm.GenerationResult{Text: "observed"}, nil
		}
		return calls(task.ManageName, string(raw)), nil
	}
	pending := mustSubmit(t, a, "retained-question", "Ask the exact saved question")
	activateFeedback(t, h, "feedback-rollback", second, first.Revision, "feedback-owner-fixture")
	if err = h.Store.Retire(ctx, pkg); err != nil {
		t.Fatal(err)
	}
	s, r, a = reopenMethodRuntime(t, s, r, a, p)
	h = *r.plugins
	mustDrain(t, a)
	page, err := s.TaskFeedbacks(ctx, target, "")
	if err != nil || len(page.Items) != 1 {
		t.Fatal(page, err)
	}
	retained := page.Items[0]
	rolled := question("rolled", false)
	checkRecipe := func(f task.Feedback, revision string, ttl int64, variant bool) {
		t.Helper()
		var graph sql.NullString
		var guidance string
		var lifetime int64
		if err := s.db.QueryRow(`SELECT p.graph_revision,p.response_guidance,f.expires_at-f.created_at FROM task_feedback f JOIN task_feedback_policy_binding p USING(feedback_ref) WHERE f.feedback_ref=?`, f.Ref).Scan(&graph, &guidance, &lifetime); err != nil || graph.String != revision || lifetime != ttl*1000000 || strings.HasPrefix(guidance, "VERSION_1_1:") != variant {
			t.Fatal("feedback recipe drift", graph, guidance, lifetime, err)
		}
	}
	checkRecipe(native, "", 604800, false)
	checkRecipe(old, first.Revision, 604800, false)
	checkRecipe(current, second, 86400, true)
	checkRecipe(retained, second, 86400, true)
	checkRecipe(rolled, first.Revision, 604800, false)
	if scalar(t, s, `SELECT count(*) FROM rsi_call WHERE instance_id='task-feedback'`) != 0 {
		t.Fatal("pure recipe added a second write/unknown effect ledger")
	}
	if scalar(t, s, `SELECT count(*) FROM task_feedback_policy_binding WHERE graph_revision=?`, second) != 2 || scalar(t, s, `SELECT count(*) FROM rsi_run WHERE run_id=? AND graph_revision=?`, pending.RunID, second) != 1 {
		t.Fatal("retired pending Run lost its graph")
	}
	if _, err = h.Store.Deactivate(ctx, "feedback-disable", first.Revision); err != nil {
		t.Fatal(err)
	}
	s, r, a = reopenMethodRuntime(t, s, r, a, p)
	// All accepted questions, including a retired recipe, remain owner inputs;
	// answering performs no new plugin dispatch or external-action approval.
	for _, f := range []task.Feedback{native, old, current, retained, rolled} {
		receipt, err := r.RespondTaskFeedback(ctx, f.Ref, "ANSWER\nwith exact source")
		if err != nil {
			t.Fatal(err)
		}
		var data string
		if err = s.db.QueryRow(`SELECT message_json FROM main_input WHERE message_id=?`, receipt.MessageID).Scan(&data); err != nil {
			t.Fatal(err)
		}
		var m conversation.Message
		if err = json.Unmarshal([]byte(data), &m); err != nil {
			t.Fatal(err)
		}
		text := m.Content[0].Text
		if !strings.Contains(text, "this response grants no external-action approval.") || !strings.Contains(text, `"feedback_ref":"`+f.Ref+`"`) || !strings.Contains(text, `"task_ref":"`+f.TaskRef+`"`) || !strings.Contains(text, `"prompt":"`+f.Prompt+`"`) || !strings.Contains(text, `"answer":"ANSWER\nwith exact source"`) {
			t.Fatal("response method replaced original evidence or Host constraint", text)
		}
		if strings.Contains(text, "VERSION_1_1:") != (f.Ref == current.Ref || f.Ref == retained.Ref) {
			t.Fatal("response used current instead of accepted recipe", text)
		}
		if retry, err := r.RespondTaskFeedback(ctx, f.Ref, "ANSWER\nwith exact source"); err != nil || retry != receipt {
			t.Fatal("cross-version answer replay drift", retry, receipt, err)
		}
	}
	if scalar(t, s, `SELECT count(*) FROM approval`) != 0 {
		t.Fatal("feedback created action authorization")
	}
	count := scalar(t, s, `SELECT count(*) FROM task_feedback`)
	feedbackAction(t, a, p, "feedback-disabled-new", task.ManageRequest{Operation: "request_input", TaskRef: rolled.TaskRef, Prompt: "Do not silently restore a native recipe"})
	if scalar(t, s, `SELECT count(*) FROM task_feedback`) != count || scalar(t, s, `SELECT count(*) FROM tool_invocation WHERE json_extract(result_json,'$.error.code')='PROVIDER_UNAVAILABLE'`) == 0 {
		t.Fatal("disabled recipe revived native feedback creation")
	}
}

func TestFeedbackPluginCannotExtendTrustedExpiryOrMutateAcceptedRecipe(t *testing.T) {
	for _, p := range []contract.Policy{{LifetimeSeconds: 0, ResponseGuidance: "valid"}, {LifetimeSeconds: 604801, ResponseGuidance: "valid"}, {LifetimeSeconds: 1, ResponseGuidance: ""}, {LifetimeSeconds: 1, ResponseGuidance: "\x1b[2J"}, {LifetimeSeconds: 1, ResponseGuidance: strings.Repeat("界", 683)}} {
		if validFeedbackPolicy(p) {
			t.Fatal("unsafe feedback recipe accepted", p)
		}
	}
	ctx := context.Background()
	s, r, a, p, ref := feedbackFixture(t)
	h := installFeedbackPlugin(t, s, r)
	accepted := requestFeedback(t, s, a, p, ref)
	if _, err := s.db.Exec(`UPDATE task_feedback_policy_binding SET response_guidance='changed' WHERE feedback_ref=?`, accepted.Ref); err == nil {
		t.Fatal("accepted recipe mutable")
	}
	if _, err := s.db.Exec(`DELETE FROM task_feedback_policy_binding WHERE feedback_ref=?`, accepted.Ref); err == nil {
		t.Fatal("accepted recipe removable")
	}
	status, err := h.Store.Status(ctx)
	if err != nil {
		t.Fatal(err)
	}
	selectFeedbackVersion(t, h, status.Revision, "1.2.0")
	raw, _ := json.Marshal(task.ManageRequest{Operation: "request_input", TaskRef: ref, Prompt: "Do not supersede the valid question"})
	p.respond = func(req llm.GenerationRequest) (llm.GenerationResult, error) {
		return calls(task.ManageName, string(raw)), nil
	}
	mustSubmit(t, a, "unsafe-expiry", "Ask the saved question")
	if err = a.Drain(ctx); !errors.Is(err, rsi.ErrInvalid) {
		t.Fatal("candidate widened trusted expiry bound", err)
	}
	if scalar(t, s, `SELECT count(*) FROM task_feedback`) != 1 || scalar(t, s, `SELECT count(*) FROM task_feedback WHERE feedback_ref=? AND status='pending'`, accepted.Ref) != 1 {
		t.Fatal("invalid policy superseded accepted feedback")
	}
}

func TestV56FeedbackPolicyMigrationPreservesOriginalQuestions(t *testing.T) {
	ctx := context.Background()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	db.SetMaxOpenConns(1)
	if err = migrate(ctx, db, migrations[:56]); err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(`INSERT INTO task(task_id,task_ref,agent_id,conversation_id,title,anchor_task,anchor_artifact_refs,status,task_epoch,created_at,updated_at,version) VALUES('legacy','task:legacy','owner','owner','Original title','Original request','[]','active',1,1,2,1)`); err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(`INSERT INTO task_feedback(feedback_ref,task_id,task_epoch,privacy_epoch,kind,prompt,source_message_id,artifact_refs,status,created_at,expires_at) VALUES('feedback:legacy','legacy',1,0,'input','Exact original question','original-message','[]','pending',10,604800000010)`); err != nil {
		t.Fatal(err)
	}
	if err = migrate(ctx, db, migrations); err != nil {
		t.Fatal(err)
	}
	var graph sql.NullString
	var guidance, prompt, status, source string
	var created, expires int64
	if err = db.QueryRow(`SELECT b.graph_revision,b.response_guidance,f.prompt,f.status,f.source_message_id,f.created_at,f.expires_at FROM task_feedback f JOIN task_feedback_policy_binding b USING(feedback_ref)`).Scan(&graph, &guidance, &prompt, &status, &source, &created, &expires); err != nil || graph.Valid || guidance != legacyFeedbackGuidanceV1 || prompt != "Exact original question" || status != "pending" || source != "original-message" || created != 10 || expires != 604800000010 {
		t.Fatal("migration rewrote original feedback", graph, guidance, prompt, status, source, created, expires, err)
	}
	for _, statement := range []string{`UPDATE task_feedback_policy_binding SET response_guidance='new'`, `DELETE FROM task_feedback_policy_binding`, `INSERT OR REPLACE INTO task_feedback_policy_binding VALUES('feedback:legacy',NULL,'replacement')`} {
		if _, err = db.Exec(statement); err == nil {
			t.Fatal("migrated recipe mutable", statement)
		}
	}
}

func TestFeedbackPluginRecipeAndQuestionCommitTogetherAndAcceptedReceiptDrains(t *testing.T) {
	ctx := context.Background()
	s, r, a, p, ref := feedbackFixture(t)
	h := installFeedbackPlugin(t, s, r)
	q := task.ManageRequest{Operation: "request_input", TaskRef: ref, Prompt: "Keep this exact original question"}
	raw, _ := json.Marshal(q)
	p.respond = func(req llm.GenerationRequest) (llm.GenerationResult, error) {
		if req.Messages[len(req.Messages)-1].Role == "tool" {
			return llm.GenerationResult{Text: "observed"}, nil
		}
		return calls(task.ManageName, string(raw)), nil
	}
	if _, err := s.db.Exec(`CREATE TRIGGER fail_feedback BEFORE INSERT ON task_feedback_policy_binding BEGIN SELECT RAISE(ABORT,'injected recipe commit failure'); END`); err != nil {
		t.Fatal(err)
	}
	receipt := mustSubmit(t, a, "feedback-atomic", "Ask the exact question")
	if err := a.Drain(ctx); err == nil {
		t.Fatal("recipe transaction failure not exercised")
	}
	if scalar(t, s, `SELECT count(*) FROM task_feedback`) != 0 || scalar(t, s, `SELECT count(*) FROM task_feedback_policy_binding`) != 0 || scalar(t, s, `SELECT count(*) FROM rsi_call WHERE instance_id='task-feedback'`) != 0 {
		t.Fatal("pure recipe leaked a partial question, binding or unknown ledger")
	}
	if _, err := s.db.Exec(`DROP TRIGGER fail_feedback; CREATE TRIGGER fail_feedback_tool_finish BEFORE UPDATE OF status ON tool_invocation WHEN NEW.tool_name='builtin__task_manage' AND NEW.status='succeeded' BEGIN SELECT RAISE(ABORT,'injected feedback Tool receipt failure'); END`); err != nil {
		t.Fatal(err)
	}
	if err := a.Drain(ctx); err == nil {
		t.Fatal("Tool receipt failure not exercised")
	}
	if scalar(t, s, `SELECT count(*) FROM task_feedback`) != 1 || scalar(t, s, `SELECT count(*) FROM task_feedback_policy_binding WHERE graph_revision IS NOT NULL`) != 1 {
		t.Fatal("recovered question did not freeze one recipe")
	}
	if _, err := s.db.Exec(`DROP TRIGGER fail_feedback_tool_finish`); err != nil {
		t.Fatal(err)
	}
	c, err := r.Claim(ctx, receipt.RunID)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Release(ctx, c)
	var inv tool.Invocation
	var stored string
	if err = s.db.QueryRow(`SELECT i.invocation_id,t.receipt_json FROM tool_invocation i JOIN task_invocation t USING(invocation_id) WHERE i.run_id=?`, receipt.RunID).Scan(&inv.ID, &stored); err != nil {
		t.Fatal(err)
	}
	status, err := h.Store.Status(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err = h.Store.Quarantine(ctx, status.Revision); err != nil {
		t.Fatal(err)
	}
	result, err := r.ToolStore(c).(*tools).ExecuteBuiltin(ctx, inv)
	if err != nil || result.Error != nil || result.Text != stored || scalar(t, s, `SELECT count(*) FROM task_feedback`) != 1 {
		t.Fatal("accepted feedback depended on redispatching quarantined recipe", result, err)
	}
}

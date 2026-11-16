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
	"time"

	"github.com/z-chenhao/assistant/internal/agent"
	"github.com/z-chenhao/assistant/internal/llm"
	"github.com/z-chenhao/assistant/internal/task"
	"github.com/z-chenhao/assistant/pkg/rsiplugin"
	"testing"

	"github.com/z-chenhao/assistant/internal/plugin/taskprogress/contract"
	"github.com/z-chenhao/assistant/internal/rsi"
)

func installProgressPlugin(t *testing.T, s *Store, r *Runs) rsi.Host {
	t.Helper()
	return installRuntimeFeature(t, s, r, "task-progress", rsi.Policy{ID: "progress-owner-fixture", AllowedContracts: []string{contract.GuidanceContract, contract.SelectionContract}, Cases: []rsi.Case{
		{InstanceID: "task-progress", Contract: contract.GuidanceContract, Input: json.RawMessage(`{"role":"root"}`), Expected: json.RawMessage(`{"instructions":"- Before tools, when the execution stage changes, write one brief progress sentence in assistant text, at most 512 UTF-8 bytes. State confirmed progress and the next stage; omit private reasoning and raw tool calls. Final text remains the Task result."}`)},
		{InstanceID: "task-progress", Contract: contract.SelectionContract, Input: json.RawMessage(`{"summary":"Sources checked; comparing options."}`), Expected: json.RawMessage(`{"publish":true}`)},
	}})
}

// Independently compiled implementation, not a candidate-controlled expected
// answer or an in-process replacement of the Host's projector.
func progressVersionPackage(t *testing.T, version string) string {
	t.Helper()
	source := t.TempDir()
	path, err := filepath.Abs("../../plugin/taskprogress/plugin.go")
	if err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	code := strings.Replace(string(raw), `Version: "1.0.1"`, `Version: "`+version+`"`, 1)
	if version == "1.1.0" {
		code = strings.Replace(code, `Instructions: strings.TrimSuffix(guidance, "\n")`, `Instructions: strings.TrimSuffix(guidance, "\n") + "\n- VERSION_1_1_STAGE_METHOD."`, 1)
		code = strings.Replace(code, `Publish: true`, `Publish: !strings.HasPrefix(f.Summary,"NOISE:")`, 1)
	} else {
		code = strings.Replace(code, `"context"`, `"context";"os";"time"`, 1)
		code = strings.Replace(code, `return rsiplugin.JSONResult(ctx, contract.Selection{Publish: true})`, `if strings.HasPrefix(f.Summary,"PRIVATE_STAGE") {if err:=os.WriteFile("progress-ready",[]byte("ready"),0600);err!=nil{return rsiplugin.Result{},err};for{if _,err:=os.Stat("resume");err==nil{break};select{case<-ctx.Done():return rsiplugin.Result{},ctx.Err();case<-time.After(time.Millisecond):}}};return rsiplugin.JSONResult(ctx, contract.Selection{Publish: true})`, 1)
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
	if out, err := exec.Command("go", "build", "-overlay", file, "-o", filepath.Join(source, "plugin"), "../../../cmd/plugins/task-progress").CombinedOutput(); err != nil {
		t.Fatal(string(out), err)
	}
	raw, err = os.ReadFile("../../../plugins/task-progress/steward.plugin.json")
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

func selectProgressVersion(t *testing.T, h rsi.Host, source, baseline string) (string, string) {
	t.Helper()
	ctx := context.Background()
	pkg, err := h.Install(ctx, source)
	if err != nil {
		t.Fatal(err)
	}
	graph, err := h.Prepare(ctx, rsiplugin.Graph{Protocol: rsiplugin.Protocol, Bindings: []rsiplugin.Binding{{InstanceID: "task-progress", PackageDigest: pkg.Digest, Config: json.RawMessage(`{}`), StateNamespace: "task-progress", StateVersion: 1}}})
	if err != nil {
		t.Fatal(err)
	}
	policy, err := h.Store.Policy(ctx, "progress-owner-fixture")
	if err != nil {
		t.Fatal(err)
	}
	policy.ID = "progress-upgrade-owner"
	var expected contract.Guidance
	if err = json.Unmarshal(policy.Cases[0].Expected, &expected); err != nil {
		t.Fatal(err)
	}
	expected.Instructions += "\n- VERSION_1_1_STAGE_METHOD."
	policy.Cases[0].Expected, _ = json.Marshal(expected)
	policy.Cases = append(policy.Cases, rsi.Case{InstanceID: "task-progress", Contract: contract.SelectionContract, Input: json.RawMessage(`{"summary":"NOISE: redundant stage"}`), Expected: json.RawMessage(`{"publish":false}`)})
	if err = h.Store.PutPolicy(ctx, policy); err != nil {
		t.Fatal(err)
	}
	activateProgress(t, h, "progress-upgrade", baseline, graph.Revision, policy.ID)
	return graph.Revision, pkg.Digest
}

func activateProgress(t *testing.T, h rsi.Host, id, baseline, target, policy string) {
	t.Helper()
	ctx := context.Background()
	if err := h.Propose(ctx, rsi.Candidate{ID: id, Baseline: baseline, Target: target, PolicyID: policy, Goal: "independent stage guidance and selection"}); err != nil {
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

func TestProgressPluginIndependentUpgradeRetainedRootsChildIsolationAndDisable(t *testing.T) {
	ctx := context.Background()
	s, r, a, p := childFixture(t)
	r.ConfigurePlugins(rsi.NewCatalog(filepath.Dir(s.path)))
	p.respond = func(req llm.GenerationRequest) (llm.GenerationResult, error) {
		last := req.Messages[len(req.Messages)-1]
		if last.Role == "tool" || isFollowup(req) {
			return llm.GenerationResult{Text: "Exact business result"}, nil
		}
		if !isRoot(req) {
			return calls(task.ManageName, `{"operation":"create","task":"Compare the exact original sources"}`), nil
		}
		if last.Text == "EXPLICIT_CHILD_INPUT" {
			return llm.GenerationResult{Text: "Child business result"}, nil
		}
		out := calls(agent.RunToolName, `{"task":"EXPLICIT_CHILD_INPUT"}`)
		out.Text = "NOISE: redundant stage"
		return out, nil
	}
	queueRoot := func(key string) string {
		t.Helper()
		receipt := mustSubmit(t, a, key, "Delegate this comparison")
		if err := executeTaskRunID(t, a, r, receipt.RunID); err != nil {
			t.Fatal(err)
		}
		var id string
		if err := s.db.QueryRow(`SELECT run_id FROM run WHERE worker_position='root' AND status='queued' ORDER BY created_at DESC LIMIT 1`).Scan(&id); err != nil {
			t.Fatal(err)
		}
		return id
	}
	checkRoot := func(id, summary string, variant, enabled bool) {
		t.Helper()
		var got, raw string
		if err := s.db.QueryRow(`SELECT t.progress_summary,r.checkpoint_json FROM run r JOIN task t USING(task_id) WHERE r.run_id=? AND r.status='completed'`, id).Scan(&got, &raw); err != nil || got != summary {
			t.Fatal("projection or business execution changed", got, summary, err)
		}
		var cp agent.Checkpoint
		if err := json.Unmarshal([]byte(raw), &cp); err != nil {
			t.Fatal(err)
		}
		sections, ok := agent.FrozenSystemSections(cp.Messages)
		if !ok || sections[3].Progress != enabled || strings.Contains(sections[3].Text, "VERSION_1_1_STAGE_METHOD") != variant {
			t.Fatal("Root guidance version drift", sections)
		}
		if strings.Contains(sections[3].ChildText, "VERSION_1_1_STAGE_METHOD") {
			t.Fatal("Root contribution escaped into shared rules")
		}
		var children int
		if err := s.db.QueryRow(`SELECT count(*) FROM run WHERE parent_run_id=? AND worker_position='child' AND status='completed' AND instr(checkpoint_json,'VERSION_1_1_STAGE_METHOD')=0 AND instr(checkpoint_json,'"progress":true')=0`, id).Scan(&children); err != nil || children != 1 {
			t.Fatal("Child inherited Root stage methods", children, err)
		}
	}
	legacy := queueRoot("progress-native")
	h := installProgressPlugin(t, s, r)
	first, err := h.Store.Status(ctx)
	if err != nil {
		t.Fatal(err)
	}
	mustDrain(t, a)
	checkRoot(legacy, "NOISE: redundant stage", false, true)
	if scalar(t, s, `SELECT count(*) FROM rsi_call WHERE run_id=?`, legacy) != 0 {
		t.Fatal("historical Root adopted new plugin")
	}
	old := queueRoot("progress-before-upgrade")
	second, pkg := selectProgressVersion(t, h, progressVersionPackage(t, "1.1.0"), first.Revision)
	mustDrain(t, a)
	checkRoot(old, "NOISE: redundant stage", false, true)
	current := queueRoot("progress-after-upgrade")
	mustDrain(t, a)
	checkRoot(current, "", true, true)
	retained := queueRoot("progress-retained")
	activateProgress(t, h, "progress-rollback", second, first.Revision, "progress-owner-fixture")
	if err = h.Store.Retire(ctx, pkg); err != nil {
		t.Fatal(err)
	}
	s, r, a = reopenMethodRuntime(t, s, r, a, p)
	h = *r.plugins
	mustDrain(t, a)
	checkRoot(retained, "", true, true)
	rolled := queueRoot("progress-rolled")
	mustDrain(t, a)
	checkRoot(rolled, "NOISE: redundant stage", false, true)
	if _, err = h.Store.Deactivate(ctx, "progress-disable", first.Revision); err != nil {
		t.Fatal(err)
	}
	s, r, a = reopenMethodRuntime(t, s, r, a, p)
	disabled := queueRoot("progress-disabled")
	mustDrain(t, a)
	checkRoot(disabled, "", false, false)
	if scalar(t, s, `SELECT count(*) FROM rsi_call WHERE run_id=?`, disabled) != 0 {
		t.Fatal("disabled progress dispatched native or candidate method")
	}
}

func TestProgressPluginLateForgetCannotPublishPrivateSummary(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
	defer cancel()
	s, r, a, p := taskFixture(t)
	h := installProgressPlugin(t, s, r)
	first, err := h.Store.Status(ctx)
	if err != nil {
		t.Fatal(err)
	}
	pkg, err := h.Install(ctx, progressVersionPackage(t, "1.2.0"))
	if err != nil {
		t.Fatal(err)
	}
	graph, err := h.Prepare(ctx, rsiplugin.Graph{Protocol: rsiplugin.Protocol, Bindings: []rsiplugin.Binding{{InstanceID: "task-progress", PackageDigest: pkg.Digest, Config: json.RawMessage(`{}`), StateNamespace: "task-progress", StateVersion: 1}}})
	if err != nil {
		t.Fatal(err)
	}
	activateProgress(t, h, "progress-private", first.Revision, graph.Revision, "progress-owner-fixture")
	p.respond = func(req llm.GenerationRequest) (llm.GenerationResult, error) {
		if isRoot(req) {
			out := calls("mcp__fixture__echo", `{"text":"evidence"}`)
			out.Text = "PRIVATE_STAGE to revoke"
			return out, nil
		}
		if req.Messages[len(req.Messages)-1].Role == "tool" {
			return llm.GenerationResult{Text: "accepted"}, nil
		}
		return calls(task.ManageName, `{"operation":"create","task":"Read private sources"}`), nil
	}
	main := mustSubmit(t, a, "private-stage", "Delegate")
	if err = executeTaskRunID(t, a, r, main.RunID); err != nil {
		t.Fatal(err)
	}
	var id string
	if err = s.db.QueryRow(`SELECT run_id FROM run WHERE worker_position='root'`).Scan(&id); err != nil {
		t.Fatal(err)
	}
	c, err := r.Claim(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	owner := idAgent(t, s)
	jobs := t.TempDir()
	t.Setenv("TMPDIR", jobs)
	done := make(chan struct{})
	var runErr error
	go func() { defer close(done); runErr = executeTaskRun(t, a, r, ctx, c) }()
	defer func() { cancel(); <-done }()
	tick := time.NewTicker(5 * time.Millisecond)
	defer tick.Stop()
	marker := ""
wait:
	for {
		select {
		case <-done:
			t.Fatal("stage exited before late privacy boundary", runErr)
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		case <-tick.C:
			paths, _ := filepath.Glob(filepath.Join(jobs, "steward-rsi-*", "progress-ready"))
			if len(paths) == 1 {
				marker = paths[0]
				break wait
			}
		}
	}
	if err = s.Transaction(ctx, func(tx *sql.Tx) error { return r.revokeMemory(ctx, tx, owner) }); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(filepath.Dir(marker), "resume"), []byte("continue"), 0600); err != nil {
		t.Fatal(err)
	}
	select {
	case <-done:
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	if runErr != nil && !errors.Is(runErr, agent.ErrClaimLost) {
		t.Fatal("late authority loss misclassified", runErr)
	}
	assertProgress(t, s, "")
	if scalar(t, s, `SELECT count(*) FROM tool_invocation WHERE run_id=?`, id) != 0 || scalar(t, s, `SELECT count(*) FROM rsi_call WHERE run_id=? AND operation_id<>'task-progress:guidance' AND result_json IS NOT NULL`, id) != 0 {
		t.Fatal("late policy or tools became accepted after Forget")
	}
}

func TestProgressPluginIncompleteContributionDoesNotBlockBusinessOrReplayUnknown(t *testing.T) {
	for _, operation := range []string{"guidance", "selection"} {
		t.Run(operation, func(t *testing.T) {
			ctx := context.Background()
			s, r, a, p := taskFixture(t)
			installProgressPlugin(t, s, r)
			p.respond = func(req llm.GenerationRequest) (llm.GenerationResult, error) {
				last := req.Messages[len(req.Messages)-1]
				if last.Role == "tool" || isFollowup(req) {
					return llm.GenerationResult{Text: "Business result"}, nil
				}
				if isRoot(req) {
					out := calls("mcp__fixture__echo", `{"text":"retained evidence"}`)
					out.Text = "Sources checked; comparing choices."
					return out, nil
				}
				return calls(task.ManageName, `{"operation":"create","task":"Compare sources"}`), nil
			}
			main := mustSubmit(t, a, "incomplete-"+operation, "Delegate")
			if err := executeTaskRunID(t, a, r, main.RunID); err != nil {
				t.Fatal(err)
			}
			var id string
			if err := s.db.QueryRow(`SELECT run_id FROM run WHERE worker_position='root'`).Scan(&id); err != nil {
				t.Fatal(err)
			}
			predicate := "NEW.operation_id='task-progress:guidance'"
			if operation == "selection" {
				predicate = "NEW.operation_id LIKE 'task-progress:%' AND NEW.operation_id<>'task-progress:guidance'"
			}
			if _, err := s.db.Exec(`CREATE TRIGGER fail_progress_contribution BEFORE UPDATE OF result_json ON rsi_call WHEN ` + predicate + ` BEGIN SELECT RAISE(ABORT,'injected pure contribution interrupted'); END`); err != nil {
				t.Fatal(err)
			}
			if err := a.Drain(ctx); !errors.Is(err, rsi.ErrUnknown) {
				t.Fatal("incomplete contribution did not retain unknown", err)
			}
			assertProgress(t, s, "")
			if scalar(t, s, `SELECT count(*) FROM tool_invocation WHERE run_id=?`, id) != 0 {
				t.Fatal("work preceded uncommitted checkpoint")
			}
			if _, err := s.db.Exec(`DROP TRIGGER fail_progress_contribution`); err != nil {
				t.Fatal(err)
			}
			if err := a.Drain(ctx); err != nil {
				t.Fatal("optional progress blocked recovery", err)
			}
			assertProgress(t, s, "")
			if scalar(t, s, `SELECT count(*) FROM run WHERE run_id=? AND status='completed'`, id) != 1 || scalar(t, s, `SELECT count(*) FROM tool_invocation WHERE run_id=?`, id) != 1 || scalar(t, s, `SELECT count(*) FROM rsi_call WHERE run_id=? AND result_json IS NULL`, id) != 1 {
				t.Fatal("unknown contribution replayed or business did not complete")
			}
			if err := r.Flush(ctx); err != nil {
				t.Fatal(err)
			}
		})
	}
}

package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/z-chenhao/assistant/internal/agent"
	"github.com/z-chenhao/assistant/internal/conversation"
	"github.com/z-chenhao/assistant/internal/llm"
	"github.com/z-chenhao/assistant/internal/tool"
	toolfake "github.com/z-chenhao/assistant/internal/tool/fake"
)

// admitPolicy drives the persisted provider exchange and real ToolStore; it
// deliberately leaves the loop at its Tool boundary for transition tests.
func admitPolicy(t *testing.T, defs []tool.Definition, calls []llm.ToolCall) (*Store, *Runs, agent.RunClaim, tool.Executor, string) {
	return admitPolicyRun(t, defs, calls, false)
}
func admitLegacyMainPolicy(t *testing.T, defs []tool.Definition, calls []llm.ToolCall) (*Store, *Runs, agent.RunClaim, tool.Executor, string) {
	return admitPolicyRun(t, defs, calls, true)
}
func admitPolicyRun(t *testing.T, defs []tool.Definition, calls []llm.ToolCall, legacy bool, configure ...func(*Store, *Runs)) (*Store, *Runs, agent.RunClaim, tool.Executor, string) {
	t.Helper()
	ctx := context.Background()
	s, r, _, a, p := loopFixture(t)
	for _, setup := range configure {
		setup(s, r)
	}
	registry, err := tool.NewRegistry(defs)
	if err != nil {
		t.Fatal(err)
	}
	r.registry = registry
	a.Registry = registry
	p.respond = func(llm.GenerationRequest) (llm.GenerationResult, error) {
		return llm.GenerationResult{ToolCalls: calls}, nil
	}
	var runID string
	if legacy {
		freezeLegacyMain(t, r)
		runID = mustSubmit(t, a, "policy", "do the requested work").RunID
	} else {
		runID = admitToolRun(t, s, r, a, registry.Describe(), nil)
	}
	claim, err := r.Claim(ctx, runID)
	if err != nil {
		t.Fatal(err)
	}
	if err = r.Initialize(ctx, claim); err != nil {
		t.Fatal(err)
	}
	run, err := r.Load(ctx, claim)
	if err != nil {
		t.Fatal(err)
	}
	generation, err := r.Generation(ctx, claim, run)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = p.Generate(ctx, generation.Request, nil); err != nil {
		t.Fatal(err)
	}
	return s, r, claim, tool.Executor{Registry: registry, Store: r.ToolStore(claim), Concurrency: 16}, generation.Request.GenerationID
}

// Persist a pre-role-reduction Main before ingress. Recovery must honor its
// original frozen tools; production creation always uses agent.MainTools.
func freezeLegacyMain(t *testing.T, r *Runs) {
	t.Helper()
	ctx := context.Background()
	err := r.write(ctx, func(tx *sql.Tx) error {
		var agentID, profile string
		var config, version int64
		if err := tx.QueryRowContext(ctx, `SELECT a.agent_id,a.active_model_profile_id,a.config_version,p.version FROM agent_config a JOIN model_profile p ON p.profile_id=a.active_model_profile_id WHERE a.singleton=1`).Scan(&agentID, &profile, &config, &version); err != nil {
			return err
		}
		model, err := r.host.Models().FreezeProfile(ctx, tx, profile, version)
		if err != nil {
			return err
		}
		if err := r.freezeSpend(ctx, tx, model); err != nil {
			return err
		}
		id, capID := newID(), newID()
		specs := r.registry.Describe()
		raw, _ := encoded(specs)
		now := time.Now().UnixMicro()
		if _, err := tx.ExecContext(ctx, `INSERT INTO capability_snapshot(snapshot_id,agent_config_version,role,tool_specs_hash,skill_set_hash,data_scope,can_spawn_agents,tools_json,created_at,format_version) VALUES(?,?,'main',?,'legacy',?,0,?,?,2)`, capID, config, digest(raw), agentID, raw, now); err != nil {
			return err
		}
		cp, _ := encoded(agent.Checkpoint{ContextVersion: 2, Phase: "model", Messages: agent.InitialContext(agent.ContextOptions{Position: "main", Tools: specs})})
		if _, err := tx.ExecContext(ctx, `INSERT INTO run(run_id,agent_id,conversation_id,kind,status,capability_snapshot_id,model_snapshot_id,checkpoint_json,history_boundary,created_at,updated_at,version) VALUES(?,?,?,'main','queued',?,?,?,(SELECT coalesce(max(sequence),0) FROM conversation_intent),?,?,1)`, id, agentID, agentID, capID, model.SnapshotID, cp, now, now); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO main_tool_protocol(run_id) VALUES(?); INSERT INTO run_claim VALUES(?,0,NULL,0,1)`, id, id); err != nil {
			return err
		}
		if err := r.freezeContext(ctx, tx, id); err != nil {
			return err
		}
		return runEvent(ctx, tx, id, "run_ready", 1)
	})
	if err != nil {
		t.Fatal(err)
	}
}

// Execution contracts run in a real task-scoped Root. Main-only contracts use
// ordinary ingress. Neither path widens Main or edits a frozen capability.
func admitToolRun(t *testing.T, s *Store, r *Runs, a agent.Actor, specs []tool.Spec, refs []string) string {
	t.Helper()
	ctx := context.Background()
	if len(agent.MainTools(specs)) == len(specs) {
		parts := conversation.Text("do the requested work")
		for _, ref := range refs {
			parts = append(parts, conversation.ContentPart{Type: "file", ArtifactRef: ref})
		}
		receipt, err := r.Submit(ctx, "policy", parts)
		if err != nil {
			t.Fatal(err)
		}
		return receipt.RunID
	}
	taskID := newID()
	owner := artifactOwner(t, s)
	raw, _ := encoded(refs)
	if refs == nil {
		raw = "[]"
	}
	err := r.write(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `INSERT INTO task(task_id,task_ref,agent_id,conversation_id,title,anchor_task,anchor_artifact_refs,status,task_epoch,created_at,updated_at,version) SELECT ?,?,agent_id,agent_id,'Tool execution','do the requested work',?,'active',1,1,1,1 FROM agent_config WHERE singleton=1`, taskID, "task:"+taskID, raw)
		if err != nil {
			return err
		}
		for _, ref := range refs {
			if err := s.Artifacts().PinTx(ctx, tx, owner, ref, "task", taskID); err != nil {
				return err
			}
		}
		return r.createRoot(ctx, tx, taskID)
	})
	if err != nil {
		t.Fatal(err)
	}
	var runID string
	if err := s.db.QueryRow(`SELECT run_id FROM run WHERE task_id=?`, taskID).Scan(&runID); err != nil {
		t.Fatal(err)
	}
	return runID
}
func effectDef(t *testing.T, policy string) tool.Definition {
	return toolfake.Definition(t.TempDir(), policy, nil)
}
func effectCall(args string) []llm.ToolCall {
	return []llm.ToolCall{{ID: "effect", Name: toolfake.Name, Arguments: args}}
}
func approvePolicy(t *testing.T, s *Store, r *Runs) {
	t.Helper()
	ctx := context.Background()
	views, err := r.Approvals(ctx)
	if err != nil || len(views) != 1 {
		t.Fatal(views, err)
	}
	d, err := s.Diagnostics(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err = r.Decide(ctx, d.AgentID, views[0].ID, views[0].Hash, "approved"); err != nil {
		t.Fatal(err)
	}
}
func TestToolApprovalWrongPrincipalHashDuplicateAndFrozenAdmission(t *testing.T) {
	ctx := context.Background()
	calls := effectCall(`{"target":"Alex","text":"Tuesday"}`)
	s, r, c, e, exchange := admitPolicy(t, []tool.Definition{effectDef(t, "query_before_retry")}, calls)
	if _, err := e.Execute(ctx, exchange, calls); !errors.Is(err, tool.ErrSuspended) {
		t.Fatal(err)
	}
	views, err := r.Approvals(ctx)
	if err != nil || len(views) != 1 {
		t.Fatal(views, err)
	}
	v := views[0]
	for _, pair := range [][2]string{{"wrong-principal", v.Hash}, {"wrong-principal", "wrong-hash"}} {
		if err = r.Decide(ctx, pair[0], v.ID, pair[1], "approved"); !errors.Is(err, agent.ErrConflict) {
			t.Fatal("unauthorized approval", err)
		}
	}
	d, _ := s.Diagnostics(ctx)
	if err = r.Decide(ctx, d.AgentID, v.ID, "changed", "approved"); !errors.Is(err, agent.ErrConflict) {
		t.Fatal(err)
	}
	var key, policy string
	if err = s.db.QueryRow(`SELECT idempotency_key,policy_json FROM tool_invocation`).Scan(&key, &policy); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(policy, `"risk":"high"`) || key == "" || scalar(t, s, `SELECT count(*) FROM event_outbox WHERE type='ai.steward.tool_invocation.accepted.v1'`) != 1 {
		t.Fatal("admission not frozen/atomic")
	}
	for _, query := range []string{`UPDATE tool_invocation SET arguments_json='{}',version=version+1`, `UPDATE tool_invocation SET policy_json='{}',version=version+1`, `UPDATE tool_invocation SET conflict_keys='[]',version=version+1`, `UPDATE approval SET frozen_arguments_hash='drift',version=version+1`} {
		if _, err = s.db.Exec(query); err == nil {
			t.Fatal("frozen value changed", query)
		}
	}
	approvePolicy(t, s, r)
	approvePolicy(t, s, r)
	first, err := e.Execute(ctx, exchange, calls)
	if err != nil || len(first) != 1 || first[0].Error != nil {
		t.Fatal(first, err)
	}
	again, err := e.Execute(ctx, exchange, calls)
	if err != nil || !reflect.DeepEqual(again[0], first[0]) {
		t.Fatal(again, err)
	}
	if scalar(t, s, `SELECT attempt_count FROM tool_invocation`) != 1 {
		t.Fatal("duplicate effect")
	}
	if err = r.Decide(ctx, d.AgentID, v.ID, v.Hash, "denied"); !errors.Is(err, agent.ErrConflict) {
		t.Fatal("decision changed", err)
	}
	if err = r.ToolStore(c).Finish(ctx, "wrong-invocation", tool.Result{}); !errors.Is(err, agent.ErrConflict) {
		t.Fatal(err)
	}
}
func TestToolApprovalExpiryDenialAndArgumentValidationNoEffect(t *testing.T) {
	for _, scenario := range []string{"denied", "expired", "invalid", "duplicates"} {
		t.Run(scenario, func(t *testing.T) {
			ctx := context.Background()
			var invoked atomic.Int32
			d := effectDef(t, "never")
			d.Invoke = func(context.Context, tool.Request) tool.Outcome { invoked.Add(1); return tool.Outcome{} }
			args := `{"target":"Alex","text":"draft"}`
			if scenario == "invalid" {
				args = `{"target":"Alex","text":"draft","run_id":"forged"}`
			}
			if scenario == "duplicates" {
				args = `{"target":"Alex","target":"other","text":"draft"}`
			}
			calls := effectCall(args)
			s, r, _, e, x := admitPolicy(t, []tool.Definition{d}, calls)
			if scenario == "expired" {
				r.ConfigureApprovalTTL(time.Nanosecond)
			}
			result, err := e.Execute(ctx, x, calls)
			want := "INVALID_ARGUMENT"
			if scenario == "denied" || scenario == "expired" {
				want = "APPROVAL_DENIED"
				if scenario == "denied" {
					if !errors.Is(err, tool.ErrSuspended) {
						t.Fatal(err)
					}
					views, err := r.Approvals(ctx)
					if err != nil {
						t.Fatal(err)
					}
					diag, _ := s.Diagnostics(ctx)
					if err = r.Decide(ctx, diag.AgentID, views[0].ID, views[0].Hash, "denied"); err != nil {
						t.Fatal(err)
					}
				}
				result, err = e.Execute(ctx, x, calls)
			}
			if err != nil || len(result) != 1 || result[0].Error == nil || result[0].Error.Code != want || invoked.Load() != 0 {
				t.Fatal(result, err, invoked.Load())
			}
		})
	}
}
func TestToolReplayPoliciesAndEvidence(t *testing.T) {
	for _, policy := range []string{"safe", "query_before_retry", "never", ""} {
		for _, evidence := range []string{"absent", "succeeded", "unknown"} {
			t.Run(policy+"/"+evidence, func(t *testing.T) {
				ctx := context.Background()
				var count atomic.Int32
				d := effectDef(t, policy)
				if policy == "safe" {
					d.Spec.Permission = "read_only"
					d.Spec.Risk = "low"
					d.Invoke = nil
					d.Execute = func(context.Context, string) tool.Result { count.Add(1); return tool.Result{Text: "read"} }
				} else {
					d.Invoke = func(context.Context, tool.Request) tool.Outcome {
						count.Add(1)
						return tool.Outcome{Result: tool.Result{Text: "executed"}}
					}
				}
				d.Query = func(context.Context, tool.Request) tool.Evidence {
					return tool.Evidence{State: evidence, Result: tool.Result{Text: "authoritative receipt"}}
				}
				calls := effectCall(`{"target":"R1","text":"reservation"}`)
				s, r, c, e, x := admitPolicy(t, []tool.Definition{d}, calls)
				store := r.ToolStore(c)
				invs, err := store.Accept(ctx, x, calls, e.Registry.Describe())
				if err != nil {
					t.Fatal(err)
				}
				if policy != "safe" {
					if _, err = e.Execute(ctx, x, calls); !errors.Is(err, tool.ErrSuspended) {
						t.Fatal(err)
					}
					approvePolicy(t, s, r)
				}
				if err = store.Start(ctx, invs[0].ID); err != nil {
					t.Fatal(err)
				} // crash before any result commit
				if err = r.Release(ctx, c); err != nil {
					t.Fatal(err)
				}
				c, err = r.Claim(ctx, c.RunID())
				if err != nil {
					t.Fatal(err)
				}
				e.Store = r.ToolStore(c)
				result, err := e.Execute(ctx, x, calls)
				if err != nil {
					t.Fatal(err)
				}
				wantCalls := int32(0)
				wantStatus := "unknown"
				if policy == "safe" || policy == "query_before_retry" && evidence == "absent" {
					wantCalls = 1
					wantStatus = "succeeded"
				}
				if policy == "query_before_retry" && evidence == "succeeded" {
					wantStatus = "succeeded"
				}
				if count.Load() != wantCalls || scalar(t, s, `SELECT count(*) FROM tool_invocation WHERE status=?`, wantStatus) != 1 {
					t.Fatal(policy, evidence, result, count.Load())
				}
				before := count.Load()
				if _, err = e.Execute(ctx, x, calls); err != nil {
					t.Fatal(err)
				}
				if count.Load() != before {
					t.Fatal("terminal/unknown replayed")
				}
				if wantStatus == "unknown" {
					if _, err = s.db.Exec(`UPDATE tool_invocation SET status='failed',version=version+1`); err == nil {
						t.Fatal("unknown resolved without evidence")
					}
				}
			})
		}
	}
}
func TestToolResultsCommitIndependentlyAndDeliverInDeclarationOrder(t *testing.T) {
	ctx := context.Background()
	release := make(chan struct{})
	started := make(chan struct{})
	var once sync.Once
	defer once.Do(func() { close(release) })
	d, _ := tool.FixtureRegistry().Resolve("mcp__fixture__echo")
	original := d.Execute
	d.Execute = func(ctx context.Context, args string) tool.Result {
		if strings.Contains(args, "slow") {
			close(started)
			select {
			case <-release:
			case <-ctx.Done():
			}
		} else {
			select {
			case <-started:
			case <-ctx.Done():
			}
		}
		return original(ctx, args)
	}
	calls := []llm.ToolCall{{ID: "slow", Name: d.Spec.Name, Arguments: `{"text":"slow"}`}, {ID: "fast", Name: d.Spec.Name, Arguments: `{"text":"fast"}`}}
	s, _, _, e, x := admitPolicy(t, []tool.Definition{d}, calls)
	type completion struct {
		result []tool.Result
		err    error
	}
	done := make(chan completion, 1)
	go func() { result, err := e.Execute(ctx, x, calls); done <- completion{result, err} }()
	deadline := time.Now().Add(3 * time.Second)
	for scalar(t, s, `SELECT count(*) FROM tool_invocation WHERE provider_call_id='fast' AND status='succeeded'`) == 0 {
		if time.Now().After(deadline) {
			t.Fatal("fast result blocked behind slow")
		}
		time.Sleep(time.Millisecond)
	}
	if scalar(t, s, `SELECT count(*) FROM tool_invocation WHERE provider_call_id='slow' AND status='running'`) != 1 {
		t.Fatal("slow lost")
	}
	once.Do(func() { close(release) })
	result := <-done
	if result.err != nil || len(result.result) != 2 || result.result[0].Text != "slow" || result.result[1].Text != "fast" {
		t.Fatal(result)
	}
}
func TestToolQuotasAndDynamicConflictKeys(t *testing.T) {
	for _, scope := range []string{"global", "agent", "run", "provider", "tool", "conflict"} {
		t.Run(scope, func(t *testing.T) {
			ctx := context.Background()
			var active, maxActive atomic.Int32
			d, _ := tool.FixtureRegistry().Resolve("mcp__fixture__echo")
			original := d.Execute
			d.Execute = func(ctx context.Context, args string) tool.Result {
				n := active.Add(1)
				for old := maxActive.Load(); n > old && !maxActive.CompareAndSwap(old, n); old = maxActive.Load() {
				}
				time.Sleep(5 * time.Millisecond)
				active.Add(-1)
				return original(ctx, args)
			}
			limits := ToolLimits{Global: 32, Agent: 32, Run: 32, Provider: 32, Tool: 32}
			switch scope {
			case "global":
				limits.Global = 1
			case "agent":
				limits.Agent = 1
			case "run":
				limits.Run = 1
			case "provider":
				limits.Provider = 1
			case "tool":
				limits.Tool = 1
			case "conflict":
				d.Spec.ConflictPolicy = "same-target-v1"
				d.ConflictKeys = func(string) []string { return []string{"same-resource"} }
			}
			calls := []llm.ToolCall{}
			for i := 0; i < 12; i++ {
				calls = append(calls, llm.ToolCall{ID: fmt.Sprint(i), Name: d.Spec.Name, Arguments: fmt.Sprintf(`{"text":"%d"}`, i)})
			}
			s, r, _, e, x := admitPolicy(t, []tool.Definition{d}, calls)
			r.ConfigureToolLimits(limits)
			for attempts := 0; attempts < 20; attempts++ {
				_, err := e.Execute(ctx, x, calls)
				if err == nil {
					break
				}
				if !errors.Is(err, tool.ErrSuspended) {
					t.Fatal(err)
				}
			}
			if maxActive.Load() != 1 || scalar(t, s, `SELECT count(*) FROM tool_invocation WHERE status='succeeded'`) != 12 {
				t.Fatal("quota/conflict violated", maxActive.Load())
			}
		})
	}
}
func TestToolPolicyV9MigrationRollbackAndFrozenSnapshot(t *testing.T) {
	ctx := context.Background()
	db, err := sql.Open("sqlite", filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err = migrate(ctx, db, migrations[:9]); err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(`INSERT INTO capability_snapshot VALUES('old',1,'main','hash','hash','agent',0,'[]',1)`); err != nil {
		t.Fatal(err)
	}
	broken := append(append([]string{}, migrations[:9]...), toolPolicySQL+`; INVALID DDL`)
	if err = migrate(ctx, db, broken); err == nil {
		t.Fatal("partial migration committed")
	}
	var version int
	db.QueryRow(`PRAGMA user_version`).Scan(&version)
	if version != 9 {
		t.Fatal(version)
	}
	if err = migrate(ctx, db, migrations); err != nil {
		t.Fatal(err)
	}
	var tools string
	var format int
	if err = db.QueryRow(`SELECT tools_json,format_version FROM capability_snapshot WHERE snapshot_id='old'`).Scan(&tools, &format); err != nil || tools != "[]" || format != 1 {
		t.Fatal("snapshot drift", tools, format, err)
	}
}

func TestToolLiveAttemptCannotBeDispatchedAgainBySameOwner(t *testing.T) {
	ctx := context.Background()
	calls := effectCall(`{"target":"Alex","text":"draft"}`)
	s, r, c, e, x := admitPolicy(t, []tool.Definition{effectDef(t, "never")}, calls)
	if _, err := e.Execute(ctx, x, calls); !errors.Is(err, tool.ErrSuspended) {
		t.Fatal(err)
	}
	approvePolicy(t, s, r)
	invs, err := e.Store.Accept(ctx, x, calls, e.Registry.Describe())
	if err != nil {
		t.Fatal(err)
	}
	if err = r.ToolStore(c).Start(ctx, invs[0].ID); err != nil {
		t.Fatal(err)
	}
	if _, err = e.Execute(ctx, x, calls); !errors.Is(err, agent.ErrBusy) {
		t.Fatal("live attempt treated as replay", err)
	}
	if scalar(t, s, `SELECT count(*) FROM tool_invocation WHERE status='running' AND attempt_count=1`) != 1 {
		t.Fatal("live attempt changed")
	}
}

func TestToolUnknownReconcilesOnlyFromAuthoritativeEvidence(t *testing.T) {
	ctx := context.Background()
	var state atomic.Value
	state.Store("unknown")
	var invoked atomic.Int32
	d := effectDef(t, "query_before_retry")
	d.Invoke = func(context.Context, tool.Request) tool.Outcome { invoked.Add(1); return tool.Outcome{Ambiguous: true} }
	d.Query = func(context.Context, tool.Request) tool.Evidence {
		return tool.Evidence{State: state.Load().(string), Result: tool.Result{Text: "receipt"}}
	}
	calls := effectCall(`{"target":"R1","text":"reserve"}`)
	s, r, _, e, x := admitPolicy(t, []tool.Definition{d}, calls)
	if _, err := e.Execute(ctx, x, calls); !errors.Is(err, tool.ErrSuspended) {
		t.Fatal(err)
	}
	approvePolicy(t, s, r)
	if result, err := e.Execute(ctx, x, calls); err != nil || result[0].Error.Code != "UNKNOWN_SIDE_EFFECT" {
		t.Fatal(result, err)
	}
	state.Store("absent")
	if err := r.ReconcileUnknown(ctx); err != nil {
		t.Fatal(err)
	}
	if scalar(t, s, `SELECT count(*) FROM tool_invocation WHERE status='unknown'`) != 1 {
		t.Fatal("absence treated as failure/retry")
	}
	state.Store("succeeded")
	if err := r.ReconcileUnknown(ctx); err != nil {
		t.Fatal(err)
	}
	if scalar(t, s, `SELECT count(*) FROM tool_invocation WHERE status='succeeded' AND reconciliation_json IS NOT NULL`) != 1 || invoked.Load() != 1 {
		t.Fatal("authoritative reconciliation failed")
	}
}

func TestToolArgumentDriftFailsWithExactReplayError(t *testing.T) {
	ctx := context.Background()
	d, _ := tool.FixtureRegistry().Resolve("mcp__fixture__echo")
	calls := []llm.ToolCall{{ID: "fixed-call", Name: d.Spec.Name, Arguments: `{"text":"original"}`}}
	s, _, _, e, x := admitPolicy(t, []tool.Definition{d}, calls)
	if _, err := e.Execute(ctx, x, calls); err != nil {
		t.Fatal(err)
	}
	drift := append([]llm.ToolCall{}, calls...)
	drift[0].Arguments = `{"text":"changed"}`
	if _, err := e.Execute(ctx, x, drift); !errors.Is(err, tool.ErrReplay) || err.Error() != "NON_DETERMINISTIC_REPLAY" {
		t.Fatal("drift did not fail closed", err)
	}
	if scalar(t, s, `SELECT count(*) FROM tool_invocation`) != 1 || scalar(t, s, `SELECT attempt_count FROM tool_invocation`) != 1 {
		t.Fatal("drift created new call or repeated effect")
	}
}

func TestToolExpiredApprovalCannotAuthorizeTransientRetry(t *testing.T) {
	ctx := context.Background()
	var invoked atomic.Int32
	d := effectDef(t, "query_before_retry")
	d.Invoke = func(context.Context, tool.Request) tool.Outcome {
		invoked.Add(1)
		return tool.Outcome{Transient: true, Result: tool.Failure("PROVIDER_UNAVAILABLE")}
	}
	calls := effectCall(`{"target":"Alex","text":"draft"}`)
	s, r, _, e, x := admitPolicy(t, []tool.Definition{d}, calls)
	r.ConfigureApprovalTTL(100 * time.Millisecond)
	if _, err := e.Execute(ctx, x, calls); !errors.Is(err, tool.ErrSuspended) {
		t.Fatal(err)
	}
	approvePolicy(t, s, r)
	if _, err := e.Execute(ctx, x, calls); !errors.Is(err, tool.ErrSuspended) {
		t.Fatal(err)
	}
	time.Sleep(120 * time.Millisecond)
	result, err := e.Execute(ctx, x, calls)
	if err != nil || result[0].Error == nil || result[0].Error.Code != "APPROVAL_DENIED" || invoked.Load() != 1 {
		t.Fatal("expired approval reused", result, err, invoked.Load())
	}
}

func TestToolTimeoutAndBusinessErrorsDoNotRetry(t *testing.T) {
	for _, mode := range []string{"read_timeout", "external_timeout", "business", "false_transient"} {
		t.Run(mode, func(t *testing.T) {
			ctx := context.Background()
			var invoked atomic.Int32
			d := effectDef(t, "query_before_retry")
			d.Spec.TimeoutMS = 10
			calls := effectCall(`{"target":"Alex","text":"draft"}`)
			want := "UNKNOWN_SIDE_EFFECT"
			if mode == "read_timeout" {
				want = "TIMEOUT"
				d.Spec.Permission = "read_only"
				d.Spec.Risk = "low"
				d.Spec.ReplayPolicy = "safe"
				d.Invoke = nil
				d.Execute = func(ctx context.Context, _ string) tool.Result {
					invoked.Add(1)
					<-ctx.Done()
					return tool.Result{Text: "late"}
				}
			} else {
				d.Invoke = func(ctx context.Context, _ tool.Request) tool.Outcome {
					invoked.Add(1)
					if mode == "external_timeout" {
						<-ctx.Done()
						return tool.Outcome{}
					}
					if mode == "false_transient" {
						return tool.Outcome{Transient: true, Result: tool.Failure("TIMEOUT")}
					}
					return tool.Outcome{Transient: true, Result: tool.Failure("INVALID_ARGUMENT")}
				}
				if mode == "business" {
					want = "INVALID_ARGUMENT"
				}
				if mode == "false_transient" {
					want = "TIMEOUT"
				}
			}
			s, r, _, e, x := admitPolicy(t, []tool.Definition{d}, calls)
			if mode != "read_timeout" {
				if _, err := e.Execute(ctx, x, calls); !errors.Is(err, tool.ErrSuspended) {
					t.Fatal(err)
				}
				approvePolicy(t, s, r)
			}
			result, err := e.Execute(ctx, x, calls)
			if err != nil || result[0].Error == nil || result[0].Error.Code != want || invoked.Load() != 1 {
				t.Fatal(mode, result, err, invoked.Load())
			}
		})
	}
}

func TestToolAcceptanceRollbackIncludesPolicyKeyAndOutbox(t *testing.T) {
	ctx := context.Background()
	d := effectDef(t, "query_before_retry")
	calls := effectCall(`{"target":"Alex","text":"draft"}`)
	s, _, _, e, x := admitPolicy(t, []tool.Definition{d}, calls)
	s.MainBoundary = func(name string) {
		if name == "before_tool_accept_commit" {
			panic("injected acceptance crash")
		}
	}
	crashed := false
	func() {
		defer func() {
			if recover() != nil {
				crashed = true
			}
		}()
		_, _ = e.Store.Accept(ctx, x, calls, e.Registry.Describe())
	}()
	if !crashed || scalar(t, s, `SELECT count(*) FROM tool_invocation`) != 0 || scalar(t, s, `SELECT count(*) FROM event_outbox WHERE type='ai.steward.tool_invocation.accepted.v1'`) != 0 {
		t.Fatal("partially accepted invocation")
	}
	s.MainBoundary = nil
	if _, err := e.Store.Accept(ctx, x, calls, e.Registry.Describe()); err != nil {
		t.Fatal(err)
	}
	if scalar(t, s, `SELECT count(*) FROM tool_invocation WHERE policy_json!='{}' AND idempotency_key!=''`) != 1 || scalar(t, s, `SELECT count(*) FROM event_outbox WHERE type='ai.steward.tool_invocation.accepted.v1'`) != 1 {
		t.Fatal("missing atomic admission fields")
	}
}

func TestToolWorkerUnknownReturnsUnknownUpdateAndKeepsTaskActive(t *testing.T) {
	ctx := context.Background()
	s, r, a, p := taskFixture(t)
	definitions := []tool.Definition{}
	for _, spec := range a.Registry.Describe() {
		d, _ := a.Registry.Resolve(spec.Name)
		definitions = append(definitions, d)
	}
	d := effectDef(t, "query_before_retry")
	d.Invoke = func(context.Context, tool.Request) tool.Outcome { return tool.Outcome{Ambiguous: true} }
	d.Query = func(context.Context, tool.Request) tool.Evidence { return tool.Evidence{State: "unknown"} }
	definitions = append(definitions, d)
	registry, err := tool.NewRegistry(definitions)
	if err != nil {
		t.Fatal(err)
	}
	a.Registry = registry
	r.registry = registry
	p.respond = func(req llm.GenerationRequest) (llm.GenerationResult, error) {
		if isFollowup(req) {
			return llm.GenerationResult{Text: "Main observed the uncertain result"}, nil
		}
		if isRoot(req) {
			if req.Messages[len(req.Messages)-1].Role == "tool" {
				return llm.GenerationResult{Text: "Worker finished reasoning"}, nil
			}
			return calls(toolfake.Name, `{"target":"R1","text":"reservation"}`), nil
		}
		if req.Messages[len(req.Messages)-1].Role == "tool" {
			return llm.GenerationResult{Text: "Delegated"}, nil
		}
		return calls("builtin__task_manage", `{"operation":"create","task":"Reserve R1 after confirmation"}`), nil
	}
	mustSubmit(t, a, "delegate", "delegate reservation")
	mustDrain(t, a)
	if scalar(t, s, `SELECT count(*) FROM run WHERE kind='worker' AND status='waiting' AND wait_reason='approval'`) != 1 || scalar(t, s, `SELECT count(*) FROM task_update`) != 0 {
		t.Fatal("approval was reported as business result")
	}
	approvePolicy(t, s, r)
	mustDrain(t, a)
	if scalar(t, s, `SELECT count(*) FROM task WHERE status='active'`) != 1 || scalar(t, s, `SELECT count(*) FROM task_update WHERE kind='unknown' AND content LIKE 'External effect outcome is unconfirmed.%'`) != 1 || scalar(t, s, `SELECT count(*) FROM main_inbox`) != 1 {
		t.Fatal("unconfirmed effect falsely completed Task")
	}
	if err = r.Report(ctx); err != nil {
		t.Fatal(err)
	}
	mustDrain(t, a)
	if scalar(t, s, `SELECT count(*) FROM task_update`) != 1 || scalar(t, s, `SELECT count(*) FROM main_inbox`) != 1 {
		t.Fatal("unknown report duplicated")
	}
}

func TestToolWorkerRecoveryUnusableEvidencePersistsUnknown(t *testing.T) {
	for _, name := range []string{"oversized", "success_with_error", "failure_without_error", "nonterminal_result", "nonterminal_state"} {
		t.Run(name, func(t *testing.T) {
			ctx := context.Background()
			s, r, a, p := taskFixture(t)
			d := effectDef(t, "query_before_retry")
			invoke, query := d.Invoke, d.Query
			var dispatches atomic.Int32
			d.Invoke = func(ctx context.Context, request tool.Request) tool.Outcome {
				dispatches.Add(1)
				return invoke(ctx, request)
			}
			var authoritative atomic.Bool
			d.Query = func(ctx context.Context, request tool.Request) tool.Evidence {
				if authoritative.Load() {
					return query(ctx, request)
				}
				switch name {
				case "oversized":
					return tool.Evidence{State: "succeeded", Result: tool.Result{Text: strings.Repeat("x", d.Spec.OutputBytes+1)}}
				case "success_with_error":
					return tool.Evidence{State: "succeeded", Result: tool.Failure("INVALID_ARGUMENT")}
				case "failure_without_error":
					return tool.Evidence{State: "failed", Result: tool.Result{Text: "receipt"}}
				case "nonterminal_result":
					return tool.Evidence{State: "failed", Result: tool.Failure("UNKNOWN_SIDE_EFFECT")}
				default:
					return tool.Evidence{State: "pending"}
				}
			}
			definitions := []tool.Definition{d}
			for _, spec := range a.Registry.Describe() {
				definition, _ := a.Registry.Resolve(spec.Name)
				definitions = append(definitions, definition)
			}
			registry, err := tool.NewRegistry(definitions)
			if err != nil {
				t.Fatal(err)
			}
			a.Registry, r.registry = registry, registry
			sawWorkerError, sawMainUpdate := false, false
			p.respond = func(req llm.GenerationRequest) (llm.GenerationResult, error) {
				if isFollowup(req) {
					for _, m := range req.Messages {
						if m.Origin == "runtime" && strings.Contains(m.Text, `"kind":"unknown"`) {
							sawMainUpdate = true
						}
					}
					return llm.GenerationResult{Text: "Main observed uncertainty"}, nil
				}
				if isRoot(req) {
					if last := req.Messages[len(req.Messages)-1]; last.Role == "tool" {
						// The durable transition must precede delivery to the Worker.
						sawWorkerError = strings.Contains(last.Text, "UNKNOWN_SIDE_EFFECT")
						if scalar(t, s, `SELECT count(*) FROM tool_invocation WHERE tool_name=? AND status='unknown'`, toolfake.Name) != 1 {
							t.Error("Worker received uncertainty before it was persisted")
						}
						return llm.GenerationResult{Text: "Worker finished reasoning"}, nil
					}
					return calls(toolfake.Name, `{"target":"R1","text":"reservation"}`), nil
				}
				if req.Messages[len(req.Messages)-1].Role == "tool" {
					return llm.GenerationResult{Text: "Delegated"}, nil
				}
				return calls("builtin__task_manage", `{"operation":"create","task":"Reserve R1 after confirmation"}`), nil
			}
			mustSubmit(t, a, "delegate", "delegate reservation")
			mustDrain(t, a)
			approvePolicy(t, s, r)
			var id, runID, args, key string
			if err = s.db.QueryRow(`SELECT invocation_id,run_id,arguments_json,idempotency_key FROM tool_invocation WHERE tool_name=?`, toolfake.Name).Scan(&id, &runID, &args, &key); err != nil {
				t.Fatal(err)
			}
			claim, err := r.Claim(ctx, runID)
			if err != nil {
				t.Fatal(err)
			}
			if err = r.ToolStore(claim).Start(ctx, id); err != nil {
				t.Fatal(err)
			}
			request := tool.Request{Arguments: args, IdempotencyKey: key}
			if outcome := d.Invoke(ctx, request); outcome.Result.Error != nil {
				t.Fatal(outcome)
			}
			// Replace the owner after the independent effect committed, without
			// committing its Tool result. The CLI harness proves actual SIGKILL.
			if err = r.Release(ctx, claim); err != nil {
				t.Fatal(err)
			}
			mustDrain(t, a)
			if !sawWorkerError || !sawMainUpdate || scalar(t, s, `SELECT count(*) FROM task WHERE status='active'`) != 1 || scalar(t, s, `SELECT count(*) FROM task_update WHERE kind='unknown'`) != 1 {
				t.Fatal("unusable evidence falsely completed the Task or lost the Main update")
			}
			outcomes, err := r.ToolOutcomes(ctx)
			if err != nil || len(outcomes) != 1 || outcomes[0].Status != "unknown" {
				t.Fatal("uncertainty not available for reconciliation", outcomes, err)
			}
			mustDrain(t, a)
			if scalar(t, s, `SELECT count(*) FROM task_update`) != 1 {
				t.Fatal("unknown report duplicated")
			}
			authoritative.Store(true)
			if err = r.ReconcileUnknown(ctx); err != nil {
				t.Fatal(err)
			}
			if dispatches.Load() != 1 || scalar(t, s, `SELECT count(*) FROM tool_invocation WHERE invocation_id=? AND status='succeeded' AND idempotency_key=? AND arguments_json=? AND attempt_count=1 AND reconciliation_json IS NOT NULL`, id, key, args) != 1 {
				t.Fatal("authoritative evidence did not settle the original call without dispatch")
			}
			if evidence := query(ctx, request); evidence.State != "succeeded" || !strings.Contains(evidence.Result.Text, "reservation") {
				t.Fatal("original external receipt lost", evidence)
			}
		})
	}
}

func TestToolHiddenFromWorkerIsNotResolvedOrValidated(t *testing.T) {
	s, _, a, p := taskFixture(t)
	sawHiddenResult := false
	p.respond = func(req llm.GenerationRequest) (llm.GenerationResult, error) {
		last := req.Messages[len(req.Messages)-1]
		if isFollowup(req) {
			return llm.GenerationResult{Text: "Main received Worker result"}, nil
		}
		if isRoot(req) {
			if last.Role == "tool" {
				var failure tool.Error
				if err := json.Unmarshal([]byte(last.Text), &failure); err != nil || failure.Code != "NOT_FOUND" {
					t.Errorf("hidden Tool was resolved/validated: %s (%v)", last.Text, err)
				}
				sawHiddenResult = true
				return llm.GenerationResult{Text: "Tool unavailable"}, nil
			}
			return calls("builtin__task_manage", `{"forged":"invalid arguments for a hidden Tool"}`), nil
		}
		if last.Role == "tool" {
			return llm.GenerationResult{Text: "Delegated"}, nil
		}
		return calls("builtin__task_manage", `{"operation":"create","task":"Attempt an unavailable Tool without gaining Task control"}`), nil
	}
	mustSubmit(t, a, "hidden-tool", "delegate work")
	mustDrain(t, a)
	if !sawHiddenResult || scalar(t, s, `SELECT count(*) FROM task`) != 1 || scalar(t, s, `SELECT count(*) FROM approval`) != 0 {
		t.Fatal("hidden Tool obtained authority")
	}
}

package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/z-chenhao/assistant/internal/agent"
	"github.com/z-chenhao/assistant/internal/llm"
	"github.com/z-chenhao/assistant/internal/task"
	"github.com/z-chenhao/assistant/internal/tool"
)

func controlFixture(t *testing.T, root func(llm.GenerationRequest) (llm.GenerationResult, error)) (*Store, *Runs, agent.Actor) {
	t.Helper()
	return controlPolicyFixture(t, root, "")
}

func controlPolicyFixture(t *testing.T, root func(llm.GenerationRequest) (llm.GenerationResult, error), policy string, managed ...bool) (*Store, *Runs, agent.Actor) {
	t.Helper()
	s, r, a, p := taskFixture(t)
	defs := []tool.Definition{agent.AgentToolDefinition()}
	for _, spec := range a.Registry.Describe() {
		d, _ := a.Registry.Resolve(spec.Name)
		defs = append(defs, d)
	}
	registry, err := tool.NewRegistry(defs)
	if err != nil {
		t.Fatal(err)
	}
	r.registry, a.Registry = registry, registry
	if len(managed) > 0 && managed[0] {
		installTaskPlugin(t, s, r)
	}
	p.respond = func(req llm.GenerationRequest) (llm.GenerationResult, error) {
		if isRoot(req) {
			if root != nil {
				return root(req)
			}
			return llm.GenerationResult{Text: "committed business result"}, nil
		}
		last := req.Messages[len(req.Messages)-1]
		if last.Role == "tool" || isFollowup(req) {
			return llm.GenerationResult{Text: "Main acknowledgement"}, nil
		}
		op := strings.Fields(last.Text)[0]
		request := task.ManageRequest{Operation: op}
		if op == "create" {
			request.Task = "Wedding plan with stable progress"
			request.CompletionPolicy = policy
		} else {
			if err := s.db.QueryRow(`SELECT task_ref FROM task LIMIT 1`).Scan(&request.TaskRef); err != nil {
				return llm.GenerationResult{}, err
			}
			if op == "append" {
				request.Instruction = "Continue with the new date"
			}
		}
		if op == "race" || op == "complete-race" {
			args := []string{}
			actions := []string{"pause", "append", "cancel"}
			if op == "complete-race" {
				actions = []string{"complete", "append"}
			}
			for _, action := range actions {
				q := task.ManageRequest{Operation: action, TaskRef: request.TaskRef}
				if action == "append" {
					q.Instruction = "Concurrent new date"
				}
				raw, _ := json.Marshal(q)
				args = append(args, string(raw))
			}
			return calls(task.ManageName, args...), nil
		}
		raw, _ := json.Marshal(request)
		args := []string{string(raw)}
		if strings.Contains(last.Text, "duplicate") {
			args = append(args, string(raw))
		}
		return calls(task.ManageName, args...), nil
	}
	return s, r, a
}

// controlMain is only for a fixture without a live Drain. Once Drain starts,
// submit inputs to it instead of competing for its Main Run claim.
func controlMain(t *testing.T, r *Runs, a agent.Actor, key, text string) {
	t.Helper()
	receipt := mustSubmit(t, a, key, text)
	c, err := r.Claim(context.Background(), receipt.RunID)
	if err != nil {
		t.Fatal(err)
	}
	if err = executeTaskRun(t, a, r, context.Background(), c); err != nil {
		t.Fatal(err)
	}
}

func rootClaim(t *testing.T, s *Store, r *Runs) agent.RunClaim {
	t.Helper()
	var id string
	if err := s.db.QueryRow(`SELECT run_id FROM run WHERE kind='worker' AND worker_position='root' AND status IN ('queued','running','waiting')`).Scan(&id); err != nil {
		t.Fatal(err)
	}
	c, err := r.Claim(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

// Replay the model's shortest fencing counterexample at the actual persisted
// generation/Tool admission boundary: claim -> stop commit -> old owner write.
func TestTaskControlModelCounterexampleRejectsOldOwner(t *testing.T) {
	for _, stop := range []string{"pause", "cancel"} {
		t.Run(stop, func(t *testing.T) {
			s, r, a := controlFixture(t, nil)
			ctx := context.Background()
			controlMain(t, r, a, "create", "create")
			c := rootClaim(t, s, r)
			run, err := r.Load(ctx, c)
			if err != nil {
				t.Fatal(err)
			}
			gen, err := r.Generation(ctx, c, run)
			if err != nil {
				t.Fatal(err)
			}
			response := calls(agent.RunToolName, `{"task":"accepted before stop"}`)
			if err = s.Models().RecordUsageAnd(ctx, gen.Request, response, 0, nil, s.AcceptGeneration); err != nil {
				t.Fatal(err)
			}
			if err = r.Progress(ctx, c, "before stop"); err != nil {
				t.Fatal(err)
			}
			controlMain(t, r, a, stop, stop)
			// A live lease intentionally remains: ownership alone is insufficient.
			if err = r.Renew(ctx, c); err != nil {
				t.Fatal(err)
			}
			if err = r.Progress(ctx, c, "stale model result"); !errors.Is(err, agent.ErrClaimLost) {
				t.Fatalf("model counterexample admitted stale write: %v", err)
			}
			if _, err = r.ToolStore(c).Accept(ctx, gen.Request.GenerationID, response.ToolCalls, run.Tools); !errors.Is(err, agent.ErrClaimLost) {
				t.Fatalf("old owner admitted Child: %v", err)
			}
			if scalar(t, s, `SELECT count(*) FROM task WHERE progress_summary='before stop' AND task_epoch=2`) != 1 ||
				scalar(t, s, `SELECT count(*) FROM run WHERE worker_position='child'`) != 0 ||
				scalar(t, s, `SELECT count(*) FROM tool_invocation WHERE run_id=?`, c.RunID()) != 0 {
				t.Fatal("rejected write/admission left partial state")
			}
			if stopped, err := r.SettleStop(ctx, c); err != nil || !stopped {
				t.Fatal(stopped, err)
			}
		})
	}
}

func TestTaskControlPauseFencesLateWritesAndContinueNewRoot(t *testing.T) {
	for _, policy := range []string{"once", "ongoing"} {
		t.Run(policy, func(t *testing.T) { testTaskControlPauseFencing(t, policy) })
	}
}

func testTaskControlPauseFencing(t *testing.T, policy string, managed ...bool) {
	s, r, a := controlPolicyFixture(t, nil, policy, managed...)
	ctx := context.Background()
	controlMain(t, r, a, "create", "create")
	c := rootClaim(t, s, r)
	if err := r.Progress(ctx, c, "Venue comparison saved"); err != nil {
		t.Fatal(err)
	}
	run, err := r.Load(ctx, c)
	if err != nil {
		t.Fatal(err)
	}
	gen, err := r.Generation(ctx, c, run)
	if err != nil {
		t.Fatal(err)
	}
	controlMain(t, r, a, "pause", "pause duplicate")
	if scalar(t, s, `SELECT count(*) FROM task WHERE status='paused' AND task_epoch=2`) != 1 || scalar(t, s, `SELECT count(*) FROM run WHERE run_id=? AND status='running' AND desired_state='task_paused'`, c.RunID()) != 1 {
		t.Fatal("pause did not persist intent before settling the live owner")
	}
	for name, check := range map[string]func() error{
		"checkpoint": func() error { return r.Checkpoint(ctx, c, run.Version, run.Checkpoint) },
		"progress":   func() error { return r.Progress(ctx, c, "late") },
		"generation": func() error { _, e := r.Generation(ctx, c, run); return e },
		"acceptance": func() error {
			return s.Models().RecordUsageAnd(ctx, gen.Request, llm.GenerationResult{Text: "late"}, 0, nil, s.AcceptGeneration)
		},
		"tool admission": func() error { _, e := r.ToolStore(c).Accept(ctx, gen.Request.GenerationID, nil, run.Tools); return e },
		"tool start":     func() error { return r.ToolStore(c).Start(ctx, "late") },
		"tool finish":    func() error { return r.ToolStore(c).Finish(ctx, "late", tool.Result{Text: "late"}) },
		"reply":          func() error { _, e := r.PrepareReply(ctx, c, run); return e },
		"complete":       func() error { _, e := r.Complete(ctx, c, run.Version); return e },
		"fail":           func() error { return r.Fail(ctx, c, run.Version, "PROVIDER_UNAVAILABLE") },
		"trace":          func() error { _, e := r.Trace(ctx, c, agent.Event{Kind: "agent_end"}); return e },
	} {
		if err := check(); !errors.Is(err, agent.ErrClaimLost) {
			t.Fatalf("%s: %v", name, err)
		}
	}
	if scalar(t, s, `SELECT count(*) FROM provider_usage u JOIN model_generation g USING(generation_id) WHERE g.run_id=?`, c.RunID()) != 0 {
		t.Fatal("late usage accepted")
	}
	if _, err = r.Claim(ctx, c.RunID()); !errors.Is(err, agent.ErrBusy) {
		t.Fatal("stopped run claimed", err)
	}
	if ok, err := r.SettleStop(ctx, c); err != nil || !ok {
		t.Fatal(ok, err)
	}
	if scalar(t, s, `SELECT count(*) FROM run WHERE run_id=? AND status='cancelled' AND error_code='task_paused'`, c.RunID()) != 1 {
		t.Fatal("old root not cancelled with reason")
	}
	before := scalar(t, s, `SELECT count(*) FROM event_outbox`)
	for range 3 {
		if err := r.recoverStops(ctx); err != nil {
			t.Fatal(err)
		}
	}
	if scalar(t, s, `SELECT count(*) FROM event_outbox`) != before {
		t.Fatal("duplicate recovery changed state")
	}
	controlMain(t, r, a, "continue", "append")
	if scalar(t, s, `SELECT count(*) FROM task WHERE status='active' AND task_epoch=3 AND progress_summary='Venue comparison saved'`) != 1 || scalar(t, s, `SELECT count(*) FROM run WHERE kind='worker'`) != 2 {
		t.Fatal("continue lost context/epoch/new root")
	}
	if err := s.Models().RecordUsageAnd(ctx, gen.Request, llm.GenerationResult{Text: "out of order"}, 0, nil, s.AcceptGeneration); !errors.Is(err, agent.ErrClaimLost) {
		t.Fatal(err)
	}
	mustDrain(t, a)
	want := "completed"
	if policy == "ongoing" {
		want = "active"
	}
	if scalar(t, s, `SELECT count(*) FROM task_update`) != 1 || scalar(t, s, `SELECT count(*) FROM task WHERE status=? AND completion_policy=?`, want, policy) != 1 {
		t.Fatal("stale result reported")
	}
}

func TestTaskControlTransitionsAndIdempotentReceipts(t *testing.T) {
	for _, end := range []string{"completed", "cancelled", "paused"} {
		t.Run(end, func(t *testing.T) {
			s, r, a := controlFixture(t, nil)
			controlMain(t, r, a, "create", "create")
			if end == "completed" {
				mustDrain(t, a)
			} else if end == "paused" {
				controlMain(t, r, a, "pause", "pause duplicate")
			} else {
				controlMain(t, r, a, "cancel", "cancel duplicate")
			}
			epoch := scalar(t, s, `SELECT task_epoch FROM task`)
			if end != "completed" {
				controlMain(t, r, a, "same", ""+map[string]string{"paused": "pause", "cancelled": "cancel"}[end])
				if scalar(t, s, `SELECT task_epoch FROM task`) != epoch {
					t.Fatal("duplicate stop incremented epoch")
				}
			}
			if end == "paused" {
				controlMain(t, r, a, "cancel-after-pause", "cancel")
				epoch++
			}
			controlMain(t, r, a, "illegal", "pause")
			if scalar(t, s, `SELECT task_epoch FROM task`) != epoch {
				t.Fatal("illegal stop mutated task")
			}
			controlMain(t, r, a, "append", "append duplicate")
			if scalar(t, s, `SELECT task_epoch FROM task`) != epoch+1 || scalar(t, s, `SELECT count(*) FROM task_instruction`) != 1 {
				t.Fatal("reactivation/dedup")
			}
			mustDrain(t, a)
		})
	}
}

func TestTaskControlAppendWaitsOutsideTransactionForPauseBoundary(t *testing.T) {
	entered, release := make(chan struct{}), make(chan struct{})
	var once sync.Once
	s, r, a := controlFixture(t, func(llm.GenerationRequest) (llm.GenerationResult, error) {
		once.Do(func() { close(entered); <-release })
		return llm.GenerationResult{Text: "late held response"}, nil
	})
	waiting := make(chan struct{})
	var waitingOnce sync.Once
	s.MainBoundary = func(name string) {
		if name == "before_append_stop_wait" {
			waitingOnce.Do(func() { close(waiting) })
		}
	}
	controlMain(t, r, a, "create", "create")
	ctx, cancel := context.WithCancel(context.Background())
	finished := make(chan error, 1)
	joined := make(chan struct{})
	var unblock sync.Once
	go func() {
		defer close(joined)
		finished <- a.Drain(ctx)
	}()
	t.Cleanup(func() {
		unblock.Do(func() { close(release) })
		cancel()
		<-joined
	})
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("root never started")
	}
	// Drain owns execution after it starts. Manually claiming these Main Runs
	// races its scheduler and correctly loses the claim on a slower runner.
	pause := mustSubmit(t, a, "pause", "pause")
	deadline := time.Now().Add(5 * time.Second)
	for scalar(t, s, `SELECT count(*) FROM run WHERE run_id=? AND status='completed'`, pause.RunID) != 1 {
		if time.Now().After(deadline) {
			t.Fatal("pause Main did not complete")
		}
		time.Sleep(10 * time.Millisecond)
	}
	receipt := mustSubmit(t, a, "append", "append")
	select {
	case <-waiting:
	case <-time.After(5 * time.Second):
		t.Fatal("append did not wait")
	}
	if _, err := r.Claim(context.Background(), receipt.RunID); !errors.Is(err, agent.ErrBusy) {
		t.Fatal("append Main must remain owned by Drain", err)
	}
	if scalar(t, s, `SELECT count(*) FROM task_instruction`) != 0 || scalar(t, s, `SELECT count(*) FROM run WHERE kind='worker'`) != 1 {
		t.Fatal("append created another live Root")
	}
	unblock.Do(func() { close(release) })
	select {
	case err := <-finished:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("stop/append did not converge")
	}
	if scalar(t, s, `SELECT count(*) FROM run WHERE kind='worker' AND status='cancelled' AND error_code='task_paused'`) != 1 || scalar(t, s, `SELECT count(*) FROM run WHERE kind='worker' AND status='completed'`) != 1 || scalar(t, s, `SELECT count(*) FROM task WHERE status='completed' AND task_epoch=3`) != 1 {
		t.Fatal("race did not converge to new completed Root")
	}
}

func addControlChild(t *testing.T, s *Store, parent, id string) {
	t.Helper()
	// This storage-only control fixture predates delegation. Give its synthetic
	// Parent a legal active state; delegation itself has end-to-end tests.
	if _, err := s.db.Exec(`UPDATE run SET status='running',version=version+1 WHERE run_id=? AND status='queued'`, parent); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec(`INSERT INTO run(run_id,agent_id,conversation_id,task_id,parent_run_id,root_run_id,kind,worker_position,status,task_epoch,depth,capability_snapshot_id,model_snapshot_id,checkpoint_json,history_boundary,created_at,updated_at,version) SELECT ?,agent_id,conversation_id,task_id,run_id,root_run_id,'worker','child','queued',task_epoch,depth+1,capability_snapshot_id,model_snapshot_id,checkpoint_json,history_boundary,created_at+1,updated_at+1,1 FROM run WHERE run_id=?`, id, parent); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec(`INSERT INTO run_claim VALUES(?,0,NULL,0,1)`, id); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec(`INSERT INTO run_context_policy SELECT ?,policy_json FROM run_context_policy WHERE run_id=?`, id, parent); err != nil {
		t.Fatal(err)
	}
}

func TestTaskControlCancelTreeAndInvocationOutcomes(t *testing.T) {
	for _, policy := range []string{"once", "ongoing"} {
		t.Run(policy, func(t *testing.T) { testTaskControlCancelTreeAndInvocationOutcomes(t, policy) })
	}
}

func testTaskControlCancelTreeAndInvocationOutcomes(t *testing.T, policy string) {
	s, r, a := controlPolicyFixture(t, nil, policy)
	ctx := context.Background()
	controlMain(t, r, a, "create", "create")
	c := rootClaim(t, s, r)
	run, err := r.Load(ctx, c)
	if err != nil {
		t.Fatal(err)
	}
	gen, err := r.Generation(ctx, c, run)
	if err != nil {
		t.Fatal(err)
	}
	response := calls("mcp__fixture__echo", `{"text":"saved"}`, `{"text":"in flight"}`, `{"text":"queued"}`)
	response.ToolCalls = append(response.ToolCalls, llm.ToolCall{ID: "unsafe-fixture", Name: "unavailable_external", Arguments: `{}`})
	if err = s.Models().RecordUsageAnd(ctx, gen.Request, response, 0, nil, s.AcceptGeneration); err != nil {
		t.Fatal(err)
	}
	ts := r.ToolStore(c)
	invs, err := ts.Accept(ctx, gen.Request.GenerationID, response.ToolCalls, run.Tools)
	if err != nil {
		t.Fatal(err)
	}
	for _, i := range []int{0, 1, 3} {
		if err = ts.Start(ctx, invs[i].ID); err != nil {
			t.Fatal(err)
		}
	}
	if err = ts.Finish(ctx, invs[0].ID, tool.Result{Text: "saved result"}); err != nil {
		t.Fatal(err)
	}
	addControlChild(t, s, c.RunID(), "child")
	addControlChild(t, s, "child", "grandchild")
	grand, err := r.Claim(ctx, "grandchild")
	if err != nil {
		t.Fatal(err)
	}
	gr, err := r.Load(ctx, grand)
	if err != nil {
		t.Fatal(err)
	}
	if err = r.Wait(ctx, grand, gr.Version, "external_event"); err != nil {
		t.Fatal(err)
	}
	controlMain(t, r, a, "cancel", "cancel duplicate")
	if scalar(t, s, `SELECT count(*) FROM run WHERE parent_run_id IS NOT NULL AND status='cancelled' AND error_code='task_cancelled'`) != 2 {
		t.Fatal("descendant/wait cancellation missing")
	}
	if stopped, err := r.SettleStop(ctx, c); err != nil || !stopped {
		t.Fatal(stopped, err)
	}
	for i, want := range []string{"succeeded", "cancelled", "cancelled", "unknown"} {
		if scalar(t, s, `SELECT count(*) FROM tool_invocation WHERE invocation_id=? AND status=?`, invs[i].ID, want) != 1 {
			t.Fatal("invocation outcome", i, want)
		}
	}
	if scalar(t, s, `SELECT count(*) FROM run WHERE kind='worker' AND status='cancelled'`) != 3 || scalar(t, s, `SELECT count(*) FROM task_update`) != 0 {
		t.Fatal("tree cancel produced followup or left work")
	}
	controlMain(t, r, a, "continue", "append")
	var contextJSON string
	if err = s.db.QueryRow(`SELECT checkpoint_json FROM run WHERE kind='worker' AND status='queued'`).Scan(&contextJSON); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(contextJSON, "saved result") {
		t.Fatal("committed tool context lost")
	}
	mustDrain(t, a)
	if scalar(t, s, `SELECT count(*) FROM tool_invocation WHERE invocation_id=? AND status='unknown' AND attempt_count=1`, invs[3].ID) != 1 {
		t.Fatal("unknown replayed")
	}
}

func TestTaskControlCompletesOnlyFinishedOneShotTree(t *testing.T) {
	for _, policy := range []string{"once", "ongoing"} {
		t.Run(policy, func(t *testing.T) {
			s, r, a := controlPolicyFixture(t, nil, policy)
			ctx := context.Background()
			controlMain(t, r, a, "create", "create")
			c := rootClaim(t, s, r)
			addControlChild(t, s, c.RunID(), "child")
			if err := executeTaskRun(t, a, r, ctx, c); err != nil {
				t.Fatal(err)
			}
			if scalar(t, s, `SELECT count(*) FROM task WHERE status='active' AND completion_policy=?`, policy) != 1 || scalar(t, s, `SELECT count(*) FROM task_update`) != 0 {
				t.Fatal("Task completed before descendant")
			}
			child, err := r.Claim(ctx, "child")
			if err != nil {
				t.Fatal(err)
			}
			if err = executeTaskRun(t, a, r, ctx, child); err != nil {
				t.Fatal(err)
			}
			want := "completed"
			if policy == "ongoing" {
				want = "active"
			}
			if scalar(t, s, `SELECT count(*) FROM task WHERE status=? AND completion_policy=?`, want, policy) != 1 || scalar(t, s, `SELECT count(*) FROM task_update WHERE root_run_id=?`, c.RunID()) != 1 {
				t.Fatal("final descendant policy/result mismatch")
			}
			mustDrain(t, a)
			controlMain(t, r, a, "append", "append duplicate")
			mustDrain(t, a)
			if scalar(t, s, `SELECT count(*) FROM task WHERE status=? AND completion_policy=? AND task_epoch=2`, want, policy) != 1 || scalar(t, s, `SELECT count(*) FROM task_instruction`) != 1 || scalar(t, s, `SELECT count(*) FROM task_update`) != 2 {
				t.Fatal("append lost policy or duplicated work")
			}
		})
	}
}

func TestTaskControlModelCompletionUsesRootOutcome(t *testing.T) {
	// Same bounded traces as the Python model regression. Child admission uses
	// the existing storage fixture; completion uses the real loop/terminal path.
	for _, rootStatus := range []string{"completed", "failed"} {
		for _, childStatus := range []string{"completed", "failed"} {
			for _, childFirst := range []bool{false, true} {
				name := rootStatus + "-root/" + childStatus + "-child/"
				if childFirst {
					name += "child-first"
				} else {
					name += "root-first"
				}
				t.Run(name, func(t *testing.T) {
					s, r, a := controlFixture(t, nil)
					ctx := context.Background()
					controlMain(t, r, a, "create", "create")
					root := rootClaim(t, s, r)
					addControlChild(t, s, root.RunID(), "child")
					child, err := r.Claim(ctx, "child")
					if err != nil {
						t.Fatal(err)
					}
					finish := func(c agent.RunClaim, status string) {
						t.Helper()
						if status == "completed" {
							err = executeTaskRun(t, a, r, ctx, c)
						} else {
							var run agent.Run
							run, err = r.Load(ctx, c)
							if err == nil {
								err = r.Fail(ctx, c, run.Version, "fixture_failure")
							}
						}
						if err != nil {
							t.Fatal(err)
						}
					}
					first, last := root, child
					firstStatus, lastStatus := rootStatus, childStatus
					if childFirst {
						first, last = child, root
						firstStatus, lastStatus = childStatus, rootStatus
					}
					finish(first, firstStatus)
					if scalar(t, s, `SELECT count(*) FROM task WHERE status='active'`) != 1 || scalar(t, s, `SELECT count(*) FROM task_update`) != 0 {
						t.Fatal("Task reported before its tree drained")
					}
					finish(last, lastStatus)
					wantTask := "active"
					if rootStatus == "completed" {
						wantTask = "completed"
					}
					if scalar(t, s, `SELECT count(*) FROM task WHERE status=?`, wantTask) != 1 ||
						scalar(t, s, `SELECT count(*) FROM run WHERE run_id=? AND status=?`, root.RunID(), rootStatus) != 1 ||
						scalar(t, s, `SELECT count(*) FROM run WHERE run_id=? AND status=?`, child.RunID(), childStatus) != 1 ||
						scalar(t, s, `SELECT count(*) FROM task_update WHERE root_run_id=? AND kind=?`, root.RunID(), rootStatus) != 1 {
						t.Fatal("Task completion/update did not follow Root outcome")
					}
				})
			}
		}
	}
}

func TestTaskControlV8MigrationPreservesStateAndRollsBack(t *testing.T) {
	dir := t.TempDir()
	ctx := context.Background()
	db, err := sql.Open("sqlite", filepath.Join(dir, "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err = migrate(ctx, db, migrations[:8]); err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(`INSERT INTO task(task_id,task_ref,agent_id,conversation_id,title,anchor_task,anchor_artifact_refs,status,task_epoch,created_at,updated_at,version) VALUES('prior','task:prior','agent','conversation','title','immutable anchor','[]','active',1,100,100,1)`); err != nil {
		t.Fatal(err)
	}
	if err = migrate(ctx, db, append(append([]string{}, migrations[:8]...), taskControlSQL+"; INVALID DDL")); err == nil {
		t.Fatal("invalid migration accepted")
	}
	var version, columns int
	if err = db.QueryRow(`PRAGMA user_version`).Scan(&version); err != nil {
		t.Fatal(err)
	}
	if err = db.QueryRow(`SELECT count(*) FROM pragma_table_info('task') WHERE name='completion_policy'`).Scan(&columns); err != nil {
		t.Fatal(err)
	}
	if version != 8 || columns != 0 {
		t.Fatal("partial migration persisted", version, columns)
	}
	if err = migrate(ctx, db, migrations); err != nil {
		t.Fatal(err)
	}
	var anchor, policy string
	if err = db.QueryRow(`SELECT anchor_task,completion_policy FROM task WHERE task_id='prior'`).Scan(&anchor, &policy); err != nil || anchor != "immutable anchor" || policy != "once" {
		t.Fatal(anchor, policy, err)
	}
	for _, query := range []string{
		`UPDATE task SET status='paused',version=version+1 WHERE task_id='prior'`,
		`UPDATE task SET task_epoch=task_epoch+2,version=version+1 WHERE task_id='prior'`,
		`UPDATE task SET status='failed',version=version+1 WHERE task_id='prior'`,
		`UPDATE task SET completion_policy='ongoing',version=version+1 WHERE task_id='prior'`,
	} {
		if _, err = db.Exec(query); err == nil {
			t.Fatal("illegal transition accepted", query)
		}
	}
	if _, err = db.Exec(`UPDATE task SET status='paused',task_epoch=2,version=version+1 WHERE task_id='prior'`); err != nil {
		t.Fatal(err)
	}
}

func TestTaskControlAppendPauseCancelRace(t *testing.T) {
	for _, policy := range []string{"once", "ongoing"} {
		t.Run(policy, func(t *testing.T) { testTaskControlAppendPauseCancelRace(t, policy) })
	}
}

func testTaskControlAppendPauseCancelRace(t *testing.T, policy string) {
	for range 10 {
		s, r, a := controlPolicyFixture(t, nil, policy)
		controlMain(t, r, a, "create", "create")
		controlMain(t, r, a, "race", "race")
		if scalar(t, s, `SELECT count(*) FROM task_instruction`) != 1 {
			t.Fatal("concurrent append lost/duplicated")
		}
		if scalar(t, s, `SELECT count(*) FROM run r JOIN task t USING(task_id) WHERE r.status IN ('queued','running','waiting') AND (r.task_epoch!=t.task_epoch OR t.status!='active')`) != 0 {
			t.Fatal("stale runnable root after racing controls")
		}
		if scalar(t, s, `SELECT count(*) FROM run WHERE kind='worker' AND status IN ('queued','running','waiting')`) > 1 {
			t.Fatal("two live Roots")
		}
		mustDrain(t, a)
		if scalar(t, s, `SELECT count(*) FROM run WHERE status IN ('queued','running','waiting')`) != 0 {
			t.Fatal("race stranded work")
		}
	}
}

func TestTaskControlShutdownPreservesRootAndAcceptedStep(t *testing.T) {
	entered, release := make(chan struct{}), make(chan struct{})
	var once sync.Once
	s, r, a := controlFixture(t, func(llm.GenerationRequest) (llm.GenerationResult, error) {
		once.Do(func() { close(entered); <-release })
		return llm.GenerationResult{Text: "saved on shutdown"}, nil
	})
	controlMain(t, r, a, "create", "create")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- a.Drain(ctx) }()
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("root did not start")
	}
	cancel()
	close(release)
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("shutdown did not join")
	}
	if scalar(t, s, `SELECT count(*) FROM task WHERE status='active' AND task_epoch=1`) != 1 || scalar(t, s, `SELECT count(*) FROM run WHERE kind='worker' AND status='running' AND desired_state='running'`) != 1 || scalar(t, s, `SELECT count(*) FROM run_claim c JOIN run r USING(run_id) WHERE r.kind='worker' AND c.token IS NULL`) != 1 {
		t.Fatal("shutdown became business cancellation")
	}
	if scalar(t, s, `SELECT count(*) FROM model_generation g JOIN run r USING(run_id) WHERE r.kind='worker' AND g.status='completed'`) != 1 {
		t.Fatal("current step lost during shutdown")
	}
	mustDrain(t, a)
	if scalar(t, s, `SELECT count(*) FROM run WHERE kind='worker'`) != 1 || scalar(t, s, `SELECT count(*) FROM model_generation g JOIN run r USING(run_id) WHERE r.kind='worker' AND g.attempt_count=1`) != 1 {
		t.Fatal("shutdown recovery created/replayed Root")
	}
}

func TestTaskControlPauseCancelsSafeToolBeforeNextStep(t *testing.T) {
	entered, cancelled := make(chan struct{}), make(chan struct{})
	s, r, a := controlFixture(t, func(req llm.GenerationRequest) (llm.GenerationResult, error) {
		if req.Messages[len(req.Messages)-1].Role == "tool" {
			return llm.GenerationResult{Text: "must not reach next model step"}, nil
		}
		return calls("mcp__fixture__echo", `{"text":"safe read"}`), nil
	})
	defs := []tool.Definition{}
	for _, spec := range a.Registry.Describe() {
		d, _ := a.Registry.Resolve(spec.Name)
		if spec.Name == "mcp__fixture__echo" {
			d.Execute = func(ctx context.Context, _ string) tool.Result {
				close(entered)
				<-ctx.Done()
				close(cancelled)
				return tool.Failure("CANCELLED")
			}
		}
		defs = append(defs, d)
	}
	registry, err := tool.NewRegistry(defs)
	if err != nil {
		t.Fatal(err)
	}
	a.Registry = registry
	r.registry = registry
	controlMain(t, r, a, "create", "create")
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	var drainErr error
	go func() {
		drainErr = a.Drain(ctx)
		close(done)
	}()
	t.Cleanup(func() {
		cancel()
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			t.Error("Actor cleanup did not join")
		}
	})
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("Tool did not start")
	}
	// The live Actor owns the queued Main. A manual controlMain claim races
	// the scheduler and legitimately loses with ErrBusy on a loaded runner.
	pause := mustSubmit(t, a, "pause", "pause")
	select {
	case <-cancelled:
	case <-time.After(5 * time.Second):
		t.Fatal("Tool cancellation missing")
	}
	select {
	case <-done:
		if drainErr != nil {
			t.Fatal(drainErr)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Actor did not join Tool")
	}
	if scalar(t, s, `SELECT count(*) FROM tool_invocation i JOIN run r USING(run_id) WHERE r.kind='worker' AND i.status='cancelled'`) != 1 || scalar(t, s, `SELECT count(*) FROM model_generation g JOIN run r USING(run_id) WHERE r.kind='worker'`) != 1 || scalar(t, s, `SELECT count(*) FROM task_update`) != 0 {
		t.Fatal("safe Tool stop admitted new work or failed settlement")
	}
	if scalar(t, s, `SELECT count(*) FROM run r JOIN run_claim c USING(run_id) WHERE r.run_id=? AND r.kind='main' AND r.status='completed' AND c.epoch=1 AND c.token IS NULL`, pause.RunID) != 1 || scalar(t, s, `SELECT count(*) FROM task WHERE status='paused' AND task_epoch=2`) != 1 || scalar(t, s, `SELECT count(*) FROM run WHERE kind='worker' AND status='cancelled' AND error_code='task_paused'`) != 1 || scalar(t, s, `SELECT count(*) FROM run_claim WHERE token IS NOT NULL`) != 0 {
		t.Fatal("Actor did not own and settle the pause Main/Root exactly once")
	}
}

func TestTaskPluginPauseFencesLateWritesAndContinuesOriginalTask(t *testing.T) {
	for _, policy := range []string{"once", "ongoing"} {
		t.Run(policy, func(t *testing.T) { testTaskControlPauseFencing(t, policy, true) })
	}
}

package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"testing"

	"github.com/z-chenhao/assistant/internal/agent"
	"github.com/z-chenhao/assistant/internal/llm"
	"github.com/z-chenhao/assistant/internal/rsi"
	"github.com/z-chenhao/assistant/internal/task"
)

func taskLabelWork(t *testing.T, duplicate bool) (*Store, *Runs, agent.Actor) {
	t.Helper()
	s, r, a, p := taskFixture(t)
	installTaskPlugin(t, s, r)
	p.respond = func(req llm.GenerationRequest) (llm.GenerationResult, error) {
		if isRoot(req) || isFollowup(req) || req.Messages[len(req.Messages)-1].Role == "tool" {
			return llm.GenerationResult{Text: "Original business result"}, nil
		}
		args := `{"operation":"create","task":"Preserve the original Task and its evidence."}`
		if duplicate {
			return calls(task.ManageName, args, args), nil
		}
		return calls(task.ManageName, args), nil
	}
	mustSubmit(t, a, "pure-task-label", "perform the requested Task")
	return s, r, a
}

func TestTaskPluginLabelInterruptedBeforeAcceptanceRecoversDuplicateTask(t *testing.T) {
	s, _, a := taskLabelWork(t, true)
	if _, err := s.db.Exec(`CREATE TRIGGER fail_label_acceptance BEFORE INSERT ON task_label_plan BEGIN SELECT RAISE(ABORT,'injected title acceptance interruption'); END`); err != nil {
		t.Fatal(err)
	}
	if err := a.Drain(context.Background()); err == nil {
		t.Fatal("interrupted plan acceptance was not exercised")
	}
	if scalar(t, s, `SELECT count(*) FROM task`) != 0 || scalar(t, s, `SELECT count(*) FROM task_label_plan`) != 0 || scalar(t, s, `SELECT count(*) FROM rsi_call`) != 0 {
		t.Fatal("incomplete pure computation reserved a Task or opaque effect")
	}
	var generation, hash string
	var attempts int
	if err := s.db.QueryRow(`SELECT generation_id,request_hash,attempt_count FROM model_generation WHERE status='completed'`).Scan(&generation, &hash, &attempts); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec(`DROP TRIGGER fail_label_acceptance`); err != nil {
		t.Fatal(err)
	}
	// The actual recovery action must immediately complete the original work.
	mustDrain(t, a)
	if scalar(t, s, `SELECT count(*) FROM task`) != 1 || scalar(t, s, `SELECT count(*) FROM run WHERE worker_position='root'`) != 1 || scalar(t, s, `SELECT count(*) FROM task_request`) != 1 || scalar(t, s, `SELECT count(*) FROM task_invocation`) != 2 {
		t.Fatal("duplicate source/invocation recovery changed Task effects")
	}
	if scalar(t, s, `SELECT count(*) FROM task_label_plan WHERE title IS NULL`) != 0 || scalar(t, s, `SELECT count(*) FROM task_label_plan`) < 1 || scalar(t, s, `SELECT count(*) FROM rsi_call`) != 0 {
		t.Fatal("pure recovery left a pending policy or opaque receipt")
	}
	var afterHash string
	var afterAttempts int
	if err := s.db.QueryRow(`SELECT request_hash,attempt_count FROM model_generation WHERE generation_id=?`, generation).Scan(&afterHash, &afterAttempts); err != nil || afterHash != hash || afterAttempts != attempts {
		t.Fatal("recovery regenerated the accepted model response", err)
	}
	before := scalar(t, s, `SELECT count(*) FROM run`)
	mustDrain(t, a)
	if scalar(t, s, `SELECT count(*) FROM run`) != before {
		t.Fatal("terminal drain repeated Task work")
	}
}

func TestTaskPluginLabelLateForgetAndGuardianFence(t *testing.T) {
	for _, point := range []string{"before_task_label_commit", "after_task_label_commit"} {
		for _, cause := range []string{"forget", "stop", "quarantine"} {
			t.Run(point+"/"+cause, func(t *testing.T) {
				s, r, a := taskLabelWork(t, false)
				ctx := context.Background()
				host := r.plugins
				selected, err := host.Store.Status(ctx)
				if err != nil {
					t.Fatal(err)
				}
				owner := idAgent(t, s)
				crossed := false
				s.MainBoundary = func(name string) {
					if name != point || crossed {
						return
					}
					crossed = true
					switch cause {
					case "forget":
						err = s.Transaction(ctx, func(tx *sql.Tx) error { return r.revokeMemory(ctx, tx, owner) })
					case "stop":
						err = host.Store.Stop(ctx, true)
					case "quarantine":
						err = host.Store.Quarantine(ctx, selected.Revision)
					}
					if err != nil {
						t.Fatal(err)
					}
				}
				if runErr := a.Drain(ctx); runErr != nil && !errors.Is(runErr, agent.ErrClaimLost) && !errors.Is(runErr, rsi.ErrRevoked) {
					t.Fatal("unexpected authority failure", runErr)
				}
				if !crossed || scalar(t, s, `SELECT count(*) FROM task`) != 0 || scalar(t, s, `SELECT count(*) FROM rsi_call`) != 0 {
					t.Fatal("late authority loss accepted Task work or opaque calls")
				}
				want := 0
				if point == "after_task_label_commit" {
					want = 1
				}
				if scalar(t, s, `SELECT count(*) FROM task_label_plan`) != want {
					t.Fatal("title acceptance crossed its commit boundary")
				}
				if cause == "forget" && scalar(t, s, `SELECT count(*) FROM task_label_plan WHERE title IS NOT NULL`) != 0 {
					t.Fatal("Forget retained private title text")
				}
				if want == 1 {
					for _, query := range []string{`UPDATE task_label_plan SET title='replacement'`, `UPDATE task_label_plan SET request_hash='changed'`, `DELETE FROM task_label_plan`, `INSERT OR REPLACE INTO task_label_plan SELECT * FROM task_label_plan`} {
						if _, err := s.db.Exec(query); err == nil {
							t.Fatal("accepted or erased plan could be rewritten", query)
						}
					}
				}
			})
		}
	}
}

func TestV63TaskLabelPlanMigrationPreservesOpaqueCallsAndRequests(t *testing.T) {
	ctx := context.Background()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	db.SetMaxOpenConns(1)
	if err = migrate(ctx, db, migrations[:63]); err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(`INSERT INTO run(run_id,agent_id,conversation_id,kind,status,capability_snapshot_id,model_snapshot_id,checkpoint_json,history_boundary,created_at,updated_at,version) VALUES('historical','agent','agent','main','queued','cap','model','{"messages":[{"text":"frozen original"}]}',0,1,1,1);INSERT INTO rsi_run VALUES('historical',NULL,'[]');INSERT INTO rsi_call VALUES('historical','task-control','completed','accepted-hash',0,'{"title":"Original choice"}'),('historical','task-control','pending','pending-hash',0,NULL)`); err != nil {
		t.Fatal(err)
	}
	if err = migrate(ctx, db, migrations); err != nil {
		t.Fatal(err)
	}
	var checkpoint, title string
	var version, plans, unknown int
	if err = db.QueryRow(`SELECT checkpoint_json,version FROM run WHERE run_id='historical'`).Scan(&checkpoint, &version); err != nil || checkpoint != `{"messages":[{"text":"frozen original"}]}` || version != 1 {
		t.Fatal("migration rewrote historical Run", err)
	}
	if err = db.QueryRow(`SELECT result_json FROM rsi_call WHERE operation_id='completed' AND request_hash='accepted-hash' AND state_version=0`).Scan(&title); err != nil || title != `{"title":"Original choice"}` {
		t.Fatal("migration changed completed opaque output", err)
	}
	if err = db.QueryRow(`SELECT count(*) FROM rsi_call WHERE operation_id='pending' AND request_hash='pending-hash' AND result_json IS NULL`).Scan(&unknown); err != nil || unknown != 1 {
		t.Fatal("migration retried or discarded unknown opaque call", err)
	}
	if err = db.QueryRow(`SELECT count(*) FROM task_label_plan`).Scan(&plans); err != nil || plans != 0 {
		t.Fatal("migration invented accepted title choices", err)
	}
	if err = migrate(ctx, db, migrations); err != nil {
		t.Fatal("reopen migration failed", err)
	}
}

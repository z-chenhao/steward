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
	"testing"
	"time"

	"github.com/z-chenhao/assistant/internal/agent"
	"github.com/z-chenhao/assistant/internal/artifact"
	"github.com/z-chenhao/assistant/internal/conversation"
	"github.com/z-chenhao/assistant/internal/llm"
	"github.com/z-chenhao/assistant/internal/task"
)

func feedbackFixture(t *testing.T) (*Store, *Runs, agent.Actor, *loopProvider, string) {
	t.Helper()
	s, r, a, p := taskFixture(t)
	feedbackAction(t, a, p, "create", task.ManageRequest{Operation: "create", Task: "Prepare the report", CompletionPolicy: "ongoing"})
	var ref string
	if err := s.db.QueryRow(`SELECT task_ref FROM task`).Scan(&ref); err != nil {
		t.Fatal(err)
	}
	return s, r, a, p, ref
}

func feedbackAction(t *testing.T, a agent.Actor, p *loopProvider, key string, request task.ManageRequest) {
	t.Helper()
	raw, _ := json.Marshal(request)
	p.respond = func(req llm.GenerationRequest) (llm.GenerationResult, error) {
		if isRoot(req) || isFollowup(req) || req.Messages[len(req.Messages)-1].Role == "tool" {
			return llm.GenerationResult{Text: "Main delivers the current result."}, nil
		}
		return calls(task.ManageName, string(raw)), nil
	}
	parts := conversation.Text(key)
	for _, ref := range request.ArtifactRefs {
		parts = append(parts, conversation.ContentPart{Type: "file", ArtifactRef: ref})
	}
	if _, err := a.Store.Submit(context.Background(), key, parts); err != nil {
		t.Fatal(err)
	}
	mustDrain(t, a)
}

func requestFeedback(t *testing.T, s *Store, a agent.Actor, p *loopProvider, ref string, refs ...string) task.Feedback {
	t.Helper()
	op := "request_input"
	if len(refs) > 0 {
		op = "request_review"
	}
	feedbackAction(t, a, p, newID(), task.ManageRequest{Operation: op, TaskRef: ref, Prompt: "Please confirm the report's scope.", ArtifactRefs: refs})
	items, err := s.TaskFeedbacks(context.Background(), ref, "")
	if err != nil || len(items.Items) == 0 {
		t.Fatal(items, err)
	}
	return items.Items[0]
}

func TestTaskFeedbackMainContinuationAndConcurrentRetries(t *testing.T) {
	testFeedbackMainContinuationAndConcurrentRetries(t, false)
}
func TestFeedbackPluginMainContinuationAndConcurrentRetries(t *testing.T) {
	testFeedbackMainContinuationAndConcurrentRetries(t, true)
}
func testFeedbackMainContinuationAndConcurrentRetries(t *testing.T, managed bool) {
	s, r, a, p, ref := feedbackFixture(t)
	if managed {
		installFeedbackPlugin(t, s, r)
	}
	f := requestFeedback(t, s, a, p, ref)
	if f.Status != "pending" || f.Kind != "input" || f.SourceMessageID == "" {
		t.Fatal(f)
	}
	before := scalar(t, s, `SELECT count(*) FROM run WHERE kind='worker'`)
	var wg sync.WaitGroup
	receipts := make(chan agent.Receipt, 8)
	errs := make(chan error, 8)
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			receipt, err := r.RespondTaskFeedback(context.Background(), f.Ref, "Use the confirmed scope.")
			receipts <- receipt
			errs <- err
		}()
	}
	wg.Wait()
	close(receipts)
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	var first agent.Receipt
	for receipt := range receipts {
		if first.MessageID == "" {
			first = receipt
		}
		if receipt != first {
			t.Fatal("retry did not retain receipt", receipt, first)
		}
	}
	if scalar(t, s, `SELECT count(*) FROM run WHERE kind='worker'`) != before || scalar(t, s, `SELECT count(*) FROM main_input WHERE request_key LIKE 'task-feedback:%'`) != 1 || scalar(t, s, `SELECT count(*) FROM task_feedback WHERE status='answered' AND response_message_id=?`, first.MessageID) != 1 {
		t.Fatal("response changed work or duplicated input")
	}
	if _, err := r.RespondTaskFeedback(context.Background(), f.Ref, "A different answer"); !errors.Is(err, conversation.ErrConflict) {
		t.Fatal("different second answer accepted", err)
	}
	// The actual Actor consumes the user response and invokes the existing Task
	// tool; only that Main admission may start the next Root.
	p.respond = func(req llm.GenerationRequest) (llm.GenerationResult, error) {
		if isRoot(req) || isFollowup(req) || req.Messages[len(req.Messages)-1].Role == "tool" {
			return llm.GenerationResult{Text: "Main delivers the update."}, nil
		}
		return calls(task.ManageName, fmt.Sprintf(`{"operation":"append","taskRef":%q,"feedbackRef":%q,"instruction":"Use the confirmed scope."}`, ref, f.Ref)), nil
	}
	mustDrain(t, a)
	if scalar(t, s, `SELECT count(*) FROM run WHERE kind='worker'`) != before+1 || scalar(t, s, `SELECT count(*) FROM task_instruction`) != 1 {
		t.Fatal("Main did not continue original Task")
	}
	if retry, err := r.RespondTaskFeedback(context.Background(), f.Ref, "Use the confirmed scope."); err != nil || retry != first {
		t.Fatal("accepted retry lost after epoch advanced", retry, err)
	}
}

func TestTaskFeedbackRejectsStaleAndKeepsApprovalsSeparate(t *testing.T) {
	testFeedbackRejectsStaleAndKeepsApprovalsSeparate(t, false)
}
func TestFeedbackPluginRejectsStaleAndKeepsApprovalsSeparate(t *testing.T) {
	testFeedbackRejectsStaleAndKeepsApprovalsSeparate(t, true)
}
func testFeedbackRejectsStaleAndKeepsApprovalsSeparate(t *testing.T, managed bool) {
	for _, mode := range []string{"superseded", "pause", "cancel", "epoch", "forget", "expired", "wrong_task"} {
		t.Run(mode, func(t *testing.T) {
			s, r, a, p, ref := feedbackFixture(t)
			if managed {
				installFeedbackPlugin(t, s, r)
			}
			f := requestFeedback(t, s, a, p, ref)
			switch mode {
			case "superseded":
				requestFeedback(t, s, a, p, ref)
			case "pause", "cancel":
				feedbackAction(t, a, p, mode, task.ManageRequest{Operation: mode, TaskRef: ref})
			case "epoch":
				feedbackAction(t, a, p, mode, task.ManageRequest{Operation: "append", TaskRef: ref, Instruction: "A newer revision"})
			case "forget":
				if _, err := s.db.Exec(`INSERT INTO memory_privacy(agent_id,epoch,history_floor,updated_at) VALUES(?,1,0,?) ON CONFLICT(agent_id) DO UPDATE SET epoch=epoch+1,updated_at=excluded.updated_at`, idAgent(t, s), time.Now().UnixMicro()); err != nil {
					t.Fatal(err)
				}
			case "expired":
				// Clock expiry is an independent read predicate. Construct a request
				// already in the past without weakening the immutable update trigger.
				if _, err := s.db.Exec(`UPDATE task_feedback SET status='superseded' WHERE feedback_ref=?`, f.Ref); err != nil {
					t.Fatal(err)
				}
				if _, err := s.db.Exec(`INSERT INTO task_feedback(feedback_ref,task_id,task_epoch,privacy_epoch,kind,prompt,source_message_id,artifact_refs,status,created_at,expires_at) SELECT 'feedback:expired',task_id,task_epoch,privacy_epoch,kind,prompt,source_message_id,artifact_refs,'pending',1,2 FROM task_feedback WHERE feedback_ref=?`, f.Ref); err != nil {
					t.Fatal(err)
				}
				f.Ref = "feedback:expired"

			case "wrong_task":
				if err := r.write(context.Background(), func(tx *sql.Tx) error {
					return s.requireCurrentFeedback(context.Background(), tx, f.Ref, "task:another", false)
				}); !errors.Is(err, task.ErrFeedbackStale) {
					t.Fatal("wrong task accepted", err)
				}
				return
			}
			before := scalar(t, s, `SELECT count(*) FROM main_input`)
			if _, err := r.RespondTaskFeedback(context.Background(), f.Ref, "Looks good"); !errors.Is(err, task.ErrFeedbackStale) {
				t.Fatal("stale response accepted", err)
			}
			if scalar(t, s, `SELECT count(*) FROM main_input`) != before || scalar(t, s, `SELECT count(*) FROM approval WHERE status='approved'`) != 0 {
				t.Fatal("stale response changed ingress or approvals")
			}
		})
	}
}

func TestTaskFeedbackResponseRollbackAndReopen(t *testing.T) {
	testFeedbackResponseRollbackAndReopen(t, false)
}
func TestFeedbackPluginResponseRollbackAndReopen(t *testing.T) {
	testFeedbackResponseRollbackAndReopen(t, true)
}
func testFeedbackResponseRollbackAndReopen(t *testing.T, managed bool) {
	s, r, a, p, ref := feedbackFixture(t)
	if managed {
		installFeedbackPlugin(t, s, r)
	}
	f := requestFeedback(t, s, a, p, ref)
	if _, err := s.db.Exec(`CREATE TRIGGER reject_feedback_response BEFORE UPDATE ON task_feedback WHEN NEW.status='answered' BEGIN SELECT RAISE(ABORT,'injected'); END`); err != nil {
		t.Fatal(err)
	}
	before := scalar(t, s, `SELECT count(*) FROM main_input`)
	if _, err := r.RespondTaskFeedback(context.Background(), f.Ref, "The answer"); err == nil {
		t.Fatal("injected response failure lost")
	}
	if scalar(t, s, `SELECT count(*) FROM main_input`) != before || scalar(t, s, `SELECT count(*) FROM task_feedback WHERE status='pending'`) != 1 {
		t.Fatal("response rollback leaked an input")
	}
	if _, err := s.db.Exec(`DROP TRIGGER reject_feedback_response`); err != nil {
		t.Fatal(err)
	}
	other, err := Open(context.Background(), filepath.Dir(s.path))
	if err != nil {
		t.Fatal(err)
	}
	defer other.Close()
	items, err := other.TaskFeedbacks(context.Background(), ref, "")
	if err != nil || len(items.Items) != 1 || !reflect.DeepEqual(items.Items[0], f) {
		t.Fatal("reopened question was lost", items, err)
	}
	if _, err := other.Runs(r.history, r.registry).RespondTaskFeedback(context.Background(), f.Ref, "The answer"); err != nil {
		t.Fatal("reopened response failed", err)
	}
}

func TestTaskFeedbackLateMainCannotUndoStop(t *testing.T) {
	testFeedbackLateMainCannotUndoStop(t, false)
}
func TestFeedbackPluginLateMainCannotUndoStop(t *testing.T) {
	testFeedbackLateMainCannotUndoStop(t, true)
}
func testFeedbackLateMainCannotUndoStop(t *testing.T, managed bool) {
	for _, mode := range []string{"pause", "cancel", "forget"} {
		t.Run(mode, func(t *testing.T) {
			s, r, a, p, ref := feedbackFixture(t)
			if managed {
				installFeedbackPlugin(t, s, r)
			}
			f := requestFeedback(t, s, a, p, ref)
			if _, err := r.RespondTaskFeedback(context.Background(), f.Ref, "Continue with the report"); err != nil {
				t.Fatal(err)
			}
			before := scalar(t, s, `SELECT count(*) FROM task_instruction`)
			agentID := idAgent(t, s)
			if err := r.write(context.Background(), func(tx *sql.Tx) error {
				if mode == "forget" {
					return r.revokeMemory(context.Background(), tx, agentID)
				}
				var id string
				var epoch, version int64
				if err := tx.QueryRow(`SELECT task_id,task_epoch,version FROM task WHERE task_ref=?`, ref).Scan(&id, &epoch, &version); err != nil {
					return err
				}
				status := "paused"
				if mode == "cancel" {
					status = "cancelled"
				}
				return r.stopTask(context.Background(), tx, id, status, epoch, version)
			}); err != nil {
				t.Fatal(err)
			}
			// A new unrelated user input must not make an explicit old feedback
			// cause current. New text does not rewrite its Task/epoch/privacy.
			feedbackAction(t, a, p, "unrelated-new-source", task.ManageRequest{Operation: "append", TaskRef: ref, FeedbackRef: f.Ref, Instruction: "Old feedback"})
			if scalar(t, s, `SELECT count(*) FROM task_instruction`) != before {
				t.Fatal("late feedback undid stop/privacy")
			}
			page, err := s.TaskFeedbacks(context.Background(), "", "")
			if err != nil || len(page.Items) != 0 {
				t.Fatal("stopped/private question still actionable", page, err)
			}
			if mode != "forget" {
				// A fresh explicit ordinary user request remains the existing
				// Main-controlled continuation, independent of the old response.
				feedbackAction(t, a, p, "explicit-new-continuation", task.ManageRequest{Operation: "append", TaskRef: ref, Instruction: "A new explicit instruction"})
				if scalar(t, s, `SELECT count(*) FROM task_instruction`) != before+1 {
					t.Fatal("old feedback blocked a new explicit request")
				}
			}
		})
	}
}

func TestTaskFeedbackCurrentSourceCannotSwitchTargetOrCause(t *testing.T) {
	testFeedbackCurrentSourceCannotSwitchTargetOrCause(t, false)
}
func TestFeedbackPluginCurrentSourceCannotSwitchTargetOrCause(t *testing.T) {
	testFeedbackCurrentSourceCannotSwitchTargetOrCause(t, true)
}
func testFeedbackCurrentSourceCannotSwitchTargetOrCause(t *testing.T, managed bool) {
	for _, mode := range []string{"task", "cause", "stopped"} {
		t.Run(mode, func(t *testing.T) {
			s, r, a, p, ref := feedbackFixture(t)
			if managed {
				installFeedbackPlugin(t, s, r)
			}
			f := requestFeedback(t, s, a, p, ref)
			if _, err := r.RespondTaskFeedback(context.Background(), f.Ref, "The answer"); err != nil {
				t.Fatal(err)
			}
			request := task.ManageRequest{Operation: "append", TaskRef: ref, Instruction: "Continue"}
			if mode == "task" {
				request.TaskRef = "task:another"
			} else if mode == "cause" {
				request.FeedbackRef = "feedback:another"
			} else {
				if err := r.write(context.Background(), func(tx *sql.Tx) error {
					var id string
					var epoch, version int64
					if err := tx.QueryRow(`SELECT task_id,task_epoch,version FROM task WHERE task_ref=?`, ref).Scan(&id, &epoch, &version); err != nil {
						return err
					}
					return r.stopTask(context.Background(), tx, id, "paused", epoch, version)
				}); err != nil {
					t.Fatal(err)
				}
			}
			raw, _ := json.Marshal(request)
			p.respond = func(req llm.GenerationRequest) (llm.GenerationResult, error) {
				if req.Messages[len(req.Messages)-1].Role == "tool" {
					return llm.GenerationResult{Text: "Cannot continue stale feedback."}, nil
				}
				return calls(task.ManageName, string(raw)), nil
			}
			mustDrain(t, a)
			if scalar(t, s, `SELECT count(*) FROM task_instruction`) != 0 || scalar(t, s, `SELECT count(*) FROM tool_invocation WHERE json_extract(result_json,'$.error.code')='INVALID_ARGUMENT'`) != 1 {
				t.Fatal("feedback source bypassed cause/target/epoch fencing")
			}
		})
	}
}

func TestTaskFeedbackReviewRetainsExactArtifactRevision(t *testing.T) {
	testFeedbackReviewRetainsExactArtifactRevision(t, false)
}
func TestFeedbackPluginReviewRetainsExactArtifactRevision(t *testing.T) {
	testFeedbackReviewRetainsExactArtifactRevision(t, true)
}
func testFeedbackReviewRetainsExactArtifactRevision(t *testing.T, managed bool) {
	s, r, a, p, ref := feedbackFixture(t)
	if managed {
		installFeedbackPlugin(t, s, r)
	}
	scope := artifact.Scope{UserID: artifact.LocalUser, AgentID: idAgent(t, s), Owner: true}
	artifactRef, err := s.Artifacts().Put(context.Background(), scope, artifact.Input{Name: "report.txt", MediaType: "text/plain"}, strings.NewReader("Revision A"))
	if err != nil {
		t.Fatal(err)
	}
	f := requestFeedback(t, s, a, p, ref, artifactRef)
	if f.Kind != "review" || len(f.ArtifactRefs) != 1 || f.ArtifactRefs[0] != artifactRef {
		t.Fatal("review lost exact revision", f)
	}
	d, err := s.Artifacts().Describe(context.Background(), scope, artifactRef)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Artifacts().Delete(context.Background(), scope, artifactRef, d.Version); !errors.Is(err, artifact.ErrReferenced) {
		t.Fatal("pending review did not retain artifact", err)
	}
	answer, err := r.RespondTaskFeedback(context.Background(), f.Ref, "Revise the introduction.")
	if err != nil {
		t.Fatal(err)
	}
	if scalar(t, s, `SELECT count(*) FROM main_input WHERE message_id=? AND json_extract(message_json,'$.content[1].artifact_ref')=?`, answer.MessageID, artifactRef) != 1 || scalar(t, s, `SELECT count(*) FROM approval`) != 0 {
		t.Fatal("review response lost artifact input or became approval")
	}
	// A new review, including one in the same Task epoch, supersedes the old
	// answer as a continuation cause; it cannot be reinterpreted as the new work.
	newArtifact, err := s.Artifacts().Put(context.Background(), scope, artifact.Input{Name: "report.txt", MediaType: "text/plain"}, strings.NewReader("Revision B"))
	if err != nil {
		t.Fatal(err)
	}
	next := requestFeedback(t, s, a, p, ref, newArtifact)
	if next.Ref == f.Ref || next.ArtifactRefs[0] == artifactRef {
		t.Fatal("new revision reused old review", next)
	}
	if err := r.write(context.Background(), func(tx *sql.Tx) error { return s.requireCurrentFeedback(context.Background(), tx, f.Ref, ref, true) }); !errors.Is(err, task.ErrFeedbackStale) {
		t.Fatal("old revision feedback became current", err)
	}
	feedbackAction(t, a, p, "continue-old-review", task.ManageRequest{Operation: "append", TaskRef: ref, FeedbackRef: f.Ref, Instruction: "Use the old review"})
	if scalar(t, s, `SELECT count(*) FROM task_instruction`) != 0 || scalar(t, s, `SELECT count(*) FROM tool_invocation WHERE json_extract(result_json,'$.error.code')='INVALID_ARGUMENT'`) != 1 {
		t.Fatal("old answered review continued after a newer review")
	}
	if retry, err := r.RespondTaskFeedback(context.Background(), f.Ref, "Revise the introduction."); err != nil || retry != answer {
		t.Fatal("original accepted review receipt was lost", retry, err)
	}
}

func TestTaskFeedbackPendingPaginationDoesNotHideOlderQuestions(t *testing.T) {
	s, _, a, p, ref := feedbackFixture(t)
	f := requestFeedback(t, s, a, p, ref)
	// Populate a read fixture with equal timestamps and independent Tasks. The
	// real Main/CLI writes are exercised above and in the Task system gate.
	if err := s.Transaction(context.Background(), func(tx *sql.Tx) error {
		for i := 0; i < 51; i++ {
			id := newID()
			if _, err := tx.Exec(`INSERT INTO task(task_id,task_ref,agent_id,conversation_id,title,anchor_task,anchor_artifact_refs,status,task_epoch,created_at,updated_at,version,completion_policy) SELECT ?,?,agent_id,conversation_id,title,anchor_task,anchor_artifact_refs,'active',1,created_at,updated_at,1,completion_policy FROM task WHERE task_ref=?`, id, "task:"+id, ref); err != nil {
				return err
			}
			if _, err := tx.Exec(`INSERT INTO task_feedback(feedback_ref,task_id,task_epoch,privacy_epoch,kind,prompt,source_message_id,artifact_refs,status,created_at,expires_at) SELECT ?,?,task_epoch,privacy_epoch,kind,prompt,source_message_id,artifact_refs,'pending',created_at,expires_at FROM task_feedback WHERE feedback_ref=?`, "feedback:"+id, id, f.Ref); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	before := scalar(t, s, `SELECT count(*) FROM event_outbox`)
	page, err := s.TaskFeedbacks(context.Background(), "", "")
	if err != nil || len(page.Items) != 50 || page.Next == "" {
		t.Fatal(page, err)
	}
	last, err := s.TaskFeedbacks(context.Background(), "", page.Next)
	if err != nil || len(last.Items) != 2 || last.Next != "" || last.Items[1].Ref != f.Ref {
		t.Fatal("older current question was hidden", last, err)
	}
	filtered, err := s.TaskFeedbacks(context.Background(), ref, "")
	if err != nil || len(filtered.Items) != 1 || filtered.Items[0].Ref != f.Ref || scalar(t, s, `SELECT count(*) FROM event_outbox`) != before {
		t.Fatal("filter/read mutated or lost current question", filtered, err)
	}
}

func TestTaskFeedbackEscapedAnswerRetainsValidContentBounds(t *testing.T) {
	testFeedbackEscapedAnswerRetainsValidContentBounds(t, false)
}
func TestFeedbackPluginEscapedAnswerRetainsValidContentBounds(t *testing.T) {
	testFeedbackEscapedAnswerRetainsValidContentBounds(t, true)
}
func testFeedbackEscapedAnswerRetainsValidContentBounds(t *testing.T, managed bool) {
	s, r, a, p, ref := feedbackFixture(t)
	if managed {
		installFeedbackPlugin(t, s, r)
	}
	f := requestFeedback(t, s, a, p, ref)
	answer := strings.Repeat("\x01", 16<<10)
	receipt, err := r.RespondTaskFeedback(context.Background(), f.Ref, answer)
	if err != nil {
		t.Fatal("valid answer rejected after JSON expansion", err)
	}
	var raw string
	if err := s.db.QueryRow(`SELECT message_json FROM main_input WHERE message_id=?`, receipt.MessageID).Scan(&raw); err != nil {
		t.Fatal(err)
	}
	var message conversation.Message
	if err := json.Unmarshal([]byte(raw), &message); err != nil {
		t.Fatal(err)
	}
	if len(message.Content) < 2 {
		t.Fatal("expanded response exceeded a single part")
	}
	if _, err := conversation.Normalize(message.Content); err != nil {
		t.Fatal("stored response does not satisfy provider content contract", err)
	}
}

func TestTaskFeedbackListReusesArtifactAccessAndProtectedExpiry(t *testing.T) {
	for _, mode := range []string{"deleted", "missing", "other_owner", "expired", "protected"} {
		t.Run(mode, func(t *testing.T) {
			s, r, a, p, ref := feedbackFixture(t)
			scope := artifact.Scope{UserID: artifact.LocalUser, AgentID: idAgent(t, s), Owner: true}
			artifactRef, err := s.Artifacts().Put(context.Background(), scope, artifact.Input{Name: "revision.txt", MediaType: "text/plain"}, strings.NewReader("A"))
			if err != nil {
				t.Fatal(err)
			}
			f := requestFeedback(t, s, a, p, ref, artifactRef)
			if mode == "deleted" {
				if _, err := s.db.Exec(`UPDATE artifact SET deleted_at=?,version=version+1 WHERE artifact_ref=?`, time.Now().UnixMicro(), artifactRef); err != nil {
					t.Fatal(err)
				}
			} else {
				// A metadata read fixture checks unusable historical references and
				// protected TTLs without opening a blob or bypassing a model guard.
				fixtureRef := "artifact:" + digest(newID())
				owner := artifact.LocalUser
				expires := int64(2)
				if mode == "other_owner" {
					owner = "another-user"
					expires = time.Now().Add(24 * time.Hour).UnixMicro()
				}
				if mode != "missing" {
					if _, err := s.db.Exec(`INSERT INTO artifact(artifact_id,artifact_ref,owner_user_id,owner_agent_id,media_type,name,byte_size,content_hash,storage_key,visibility,retention_class,created_at,expires_at) SELECT ?,?,?,owner_agent_id,media_type,name,byte_size,content_hash,?,'agent','standard',1,? FROM artifact WHERE artifact_ref=?`, newID(), fixtureRef, owner, "unopened-"+newID(), expires, artifactRef); err != nil {
						t.Fatal(err)
					}
				}
				if mode == "protected" {
					if _, err := s.db.Exec(`INSERT INTO artifact_reference SELECT ?,'task',task_id FROM task WHERE task_ref=?`, fixtureRef, ref); err != nil {
						t.Fatal(err)
					}
				}
				if _, err := s.db.Exec(`UPDATE task_feedback SET status='superseded' WHERE feedback_ref=?`, f.Ref); err != nil {
					t.Fatal(err)
				}
				refs, _ := encoded([]string{fixtureRef})
				newRef := "feedback:" + newID()
				if _, err := s.db.Exec(`INSERT INTO task_feedback(feedback_ref,task_id,task_epoch,privacy_epoch,kind,prompt,source_message_id,artifact_refs,status,created_at,expires_at) SELECT ?,task_id,task_epoch,privacy_epoch,kind,prompt,source_message_id,?,'pending',created_at,expires_at FROM task_feedback WHERE feedback_ref=?`, newRef, refs, f.Ref); err != nil {
					t.Fatal(err)
				}
				if _, err := s.db.Exec(`INSERT INTO task_feedback_policy_binding SELECT ?,graph_revision,response_guidance FROM task_feedback_policy_binding WHERE feedback_ref=?`, newRef, f.Ref); err != nil {
					t.Fatal(err)
				}
				f.Ref = newRef
			}
			page, err := s.TaskFeedbacks(context.Background(), ref, "")
			want := 0
			if mode == "protected" {
				want = 1
			}
			if err != nil || len(page.Items) != want {
				t.Fatal("list disagrees with artifact liveness", page, err)
			}
			if mode != "protected" {
				if _, err := r.RespondTaskFeedback(context.Background(), f.Ref, "Review response"); !errors.Is(err, task.ErrFeedbackStale) {
					t.Fatal("unusable artifact response accepted", err)
				}
			}
		})
	}
}

func TestTaskFeedbackListDoesNotAcquireWriteReservation(t *testing.T) {
	s, _, a, p, ref := feedbackFixture(t)
	requestFeedback(t, s, a, p, ref)
	other, err := Open(context.Background(), filepath.Dir(s.path))
	if err != nil {
		t.Fatal(err)
	}
	defer other.Close()
	writer, err := other.db.BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer writer.Rollback()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	page, err := s.TaskFeedbacks(ctx, ref, "")
	if err != nil || len(page.Items) != 1 {
		t.Fatal("read waited for another process's write reservation", page, err)
	}
}

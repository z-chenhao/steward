package sqlite

import (
	"context"
	"database/sql"
	"errors"

	"github.com/z-chenhao/assistant/internal/agent"
	"github.com/z-chenhao/assistant/internal/artifact"
	"github.com/z-chenhao/assistant/internal/task"
	"github.com/z-chenhao/assistant/internal/tool"
)

type taskChange struct {
	title                   string
	request                 task.ManageRequest
	scope                   artifact.Scope
	agentID, conversationID string
	reference, id           string
	feedbackRef             string
	feedbackPolicy          feedbackPolicy
	status, receiptStatus   string
	epoch, version, at      int64
	artifactsJSON           string
	source                  string
}

// These helpers join executeTask's fenced transaction. Request/source replay
// admission and final receipts remain with the caller and commit together.
func (t *tools) createManagedTask(ctx context.Context, tx *sql.Tx, change *taskChange) error {
	var err error
	change.id, change.reference = newID(), "task:"+newID()
	policy := change.request.CompletionPolicy
	if policy == "" {
		policy = "once"
	}
	title := change.title
	if title == "" {
		title = change.request.Title
		if title == "" {
			title = change.request.Task
		}
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO task(task_id,task_ref,agent_id,conversation_id,title,anchor_task,anchor_artifact_refs,status,task_epoch,created_at,updated_at,version,completion_policy) VALUES(?,?,?,?,?,?,?,'active',1,?,?,1,?)`, change.id, change.reference, change.agentID, change.conversationID, task.Title(title), change.request.Task, change.artifactsJSON, change.at, change.at, policy); err != nil {
		return err
	}
	for _, inputRef := range change.request.ArtifactRefs {
		if err = t.runs.host.Artifacts().PinTx(ctx, tx, change.scope, inputRef, "task", change.id); err != nil {
			return err
		}
	}
	if err = t.runs.createRoot(ctx, tx, change.id); err != nil {
		return err
	}
	return nil
}
func (t *tools) changeManagedTask(ctx context.Context, tx *sql.Tx, change *taskChange) (tool.Result, error) {
	var err error

	if err = tx.QueryRowContext(ctx, `SELECT task_id,status,task_epoch,version FROM task WHERE task_ref=? AND agent_id=? AND conversation_id=?`, change.reference, change.agentID, change.conversationID).Scan(&change.id, &change.status, &change.epoch, &change.version); err == sql.ErrNoRows {
		return tool.Failure("NOT_FOUND"), nil
	} else if err != nil {
		return tool.Result{}, err
	}
	if change.request.Operation == "append" {
		var revoked bool
		if err = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM task t JOIN memory_privacy p USING(agent_id) WHERE t.task_id=? AND t.created_at<=p.updated_at)`, change.id).Scan(&revoked); err != nil {
			return tool.Result{}, err
		}
		if revoked {
			return tool.Failure("PERMISSION_DENIED"), nil
		}
	}
	if change.request.Operation == "request_input" || change.request.Operation == "request_review" {
		return t.requestTaskFeedback(ctx, tx, change)
	}
	if change.request.Operation == "complete" {
		// Complete is a control action, not a stop/drain request.
		// Include orphaned invocations on terminal Runs: an unknown
		// external outcome must never become a completion claim.
		var pending bool
		if err = tx.QueryRowContext(ctx, `SELECT
                      EXISTS(SELECT 1 FROM run WHERE task_id=? AND task_epoch=? AND status IN ('queued','running','waiting'))
                      OR EXISTS(SELECT 1 FROM tool_invocation i JOIN run r USING(run_id) WHERE r.task_id=? AND r.task_epoch=? AND i.status IN ('queued','running','waiting','unknown'))`, change.id, change.epoch, change.id, change.epoch).Scan(&pending); err != nil {
			return tool.Result{}, err
		}
		if !task.CanComplete(task.Status(change.status), pending) {
			return tool.Failure("INVALID_ARGUMENT"), nil
		}
		res, err := tx.ExecContext(ctx, `UPDATE task SET status='completed',explicit_end_epoch=task_epoch,completed_at=?,updated_at=?,version=version+1 WHERE task_id=? AND status='active' AND task_epoch=? AND version=?`, change.at, change.at, change.id, change.epoch, change.version)
		if err != nil {
			return tool.Result{}, err
		}
		if n, _ := res.RowsAffected(); n != 1 {
			return tool.Result{}, agent.ErrConflict
		}
		change.receiptStatus = "completed"
		if err = revokeGitHubBinding(ctx, tx, "", change.id); err != nil {
			return tool.Result{}, err
		}
	} else if change.request.Operation == "pause" || change.request.Operation == "cancel" {
		change.receiptStatus = "paused"
		if change.request.Operation == "cancel" {
			change.receiptStatus = "cancelled"
		}
		if change.status != change.receiptStatus {
			if !task.CanStop(task.Status(change.status), task.Status(change.receiptStatus)) {
				return tool.Failure("INVALID_ARGUMENT"), nil
			}
			if err = t.runs.stopTask(ctx, tx, change.id, change.receiptStatus, change.epoch, change.version); err != nil {
				return tool.Result{}, err
			}
			change.epoch++
		}
	} else {
		var active int
		if err = tx.QueryRowContext(ctx, `SELECT count(*) FROM run WHERE task_id=? AND worker_position='root' AND status IN ('queued','running','waiting')`, change.id).Scan(&active); err != nil {
			return tool.Result{}, err
		}
		var liveTree int
		if err = tx.QueryRowContext(ctx, `SELECT count(*) FROM run WHERE task_id=? AND status IN ('queued','running','waiting')`, change.id).Scan(&liveTree); err != nil {
			return tool.Result{}, err
		}
		plan, err := task.PlanAppend(task.Status(change.status), change.epoch, active, liveTree)
		if errors.Is(err, task.ErrDraining) {
			return tool.Result{}, agent.ErrBusy
		}
		if err != nil {
			return tool.Result{}, err
		}
		fresh := plan.FreshRoot
		change.epoch = plan.Epoch

		res, err := tx.ExecContext(ctx, `UPDATE task SET status='active',task_epoch=?,completed_at=NULL,cancelled_at=NULL,updated_at=?,version=version+1 WHERE task_id=? AND version=?`, change.epoch, change.at, change.id, change.version)
		if err != nil {
			return tool.Result{}, err
		}
		n, _ := res.RowsAffected()
		if n != 1 {
			return tool.Result{}, agent.ErrConflict
		}
		for _, inputRef := range change.request.ArtifactRefs {
			if err = t.runs.host.Artifacts().PinTx(ctx, tx, change.scope, inputRef, "task", change.id); err != nil {
				return tool.Result{}, err
			}
		}
		if _, err = tx.ExecContext(ctx, `INSERT INTO task_instruction(instruction_id,task_id,task_epoch,source_message_id,instruction,artifact_refs,created_at) VALUES(?,?,?,?,?,?,?)`, newID(), change.id, change.epoch, change.source, change.request.Instruction, change.artifactsJSON, change.at); err != nil {
			return tool.Result{}, err
		}
		if fresh {
			if err = t.runs.createRoot(ctx, tx, change.id); err != nil {
				return tool.Result{}, err
			}
		} else {
			var rootID string
			if err = tx.QueryRowContext(ctx, `SELECT run_id FROM run WHERE task_id=? AND worker_position='root' AND status IN ('queued','running','waiting')`, change.id).Scan(&rootID); err != nil {
				return tool.Result{}, err
			}
			if err = invalidateApprovals(ctx, tx, rootID); err != nil {
				return tool.Result{}, err
			}
			if err = runEvent(ctx, tx, change.id, "task.steering", change.epoch); err != nil {
				return tool.Result{}, err
			}
		}
	}
	return tool.Result{}, nil
}

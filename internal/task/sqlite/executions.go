package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"slices"

	"github.com/z-chenhao/assistant/internal/task"
)

// Read dispatched Roots, not Task aggregates or undispatched schedules. Keep
// history when a Task starts a later epoch; old results never become current.
const executionsCTE = `WITH executions AS (
 SELECT r.run_id,t.task_ref,t.title,t.completion_policy,r.status,r.created_at,r.updated_at,r.task_epoch,t.anchor_task,
 (EXISTS(SELECT 1 FROM task_feedback f LEFT JOIN memory_privacy p ON p.agent_id=t.agent_id WHERE f.task_id=t.task_id AND f.task_epoch=r.task_epoch AND f.task_epoch=t.task_epoch AND t.status IN ('active','completed') AND f.status='pending' AND f.expires_at>unixepoch('subsec')*1000000 AND f.privacy_epoch=coalesce(p.epoch,0) AND t.created_at>coalesce(p.updated_at,0)) OR (t.status='active' AND t.task_epoch=r.task_epoch AND (r.status='failed' OR EXISTS(
 SELECT 1 FROM run w WHERE (w.run_id=r.run_id OR w.root_run_id=r.run_id) AND w.status='waiting' AND w.wait_reason IN ('approval','user_input')
 ) OR EXISTS(SELECT 1 FROM tool_invocation i JOIN run w USING(run_id) WHERE (w.run_id=r.run_id OR w.root_run_id=r.run_id) AND i.status='unknown') OR EXISTS(SELECT 1 FROM spend_approval a WHERE a.root_run_id=r.run_id AND a.status='pending')))) needs_attention
 FROM run r JOIN task t ON t.task_id=r.task_id AND t.agent_id=r.agent_id
 WHERE r.worker_position='root' AND t.agent_id=(SELECT agent_id FROM agent_config WHERE singleton=1)
) `
const executionColumns = `run_id,task_ref,title,completion_policy,status,created_at,updated_at,task_epoch,needs_attention`

func scanExecution(row scanner) (task.Execution, int64, error) {
	var v task.Execution
	var created, updated int64
	err := row.Scan(&v.ID, &v.TaskRef, &v.Title, &v.CompletionPolicy, &v.Status, &created, &updated, &v.Epoch, &v.NeedsAttention)
	v.CreatedAt = stamp(created)
	v.UpdatedAt = stamp(updated)
	return v, created, err
}
func (r *Reader) Executions(ctx context.Context, q task.ListQuery) (task.ExecutionList, error) {
	out := task.ExecutionList{Items: []task.Execution{}}
	if q.Validate() != nil {
		return out, task.ErrInvalid
	}
	if q.View == "" {
		q.View = "all"
	}
	if q.Limit == 0 {
		q.Limit = 25
	}
	scope := "executions:" + q.View + ":" + q.Query
	if q.NeedsMe {
		scope = "executions-needs-me:" + q.View + ":" + q.Query
	}
	c, err := decode(q.Cursor, scope)
	if err != nil {
		return out, err
	}
	query := executionsCTE + `SELECT ` + executionColumns + ` FROM executions WHERE 1=1`
	args := []any{}
	switch q.View {
	case "open":
		query += ` AND status IN ('queued','running','waiting')`
	case "closed":
		query += ` AND status IN ('completed','failed','cancelled')`
	case "attention":
		query += ` AND needs_attention`
	}
	if q.NeedsMe && q.View != "attention" {
		query += ` AND needs_attention`
	}
	if q.Query != "" {
		query += ` AND (instr(steward_task_fold(title),steward_task_fold(?))>0 OR instr(steward_task_fold(anchor_task),steward_task_fold(?))>0)`
		args = append(args, q.Query, q.Query)
	}
	if q.Cursor != "" {
		query += ` AND (created_at<? OR (created_at=? AND run_id<?))`
		args = append(args, c.At, c.At, c.Ref)
	}
	query += ` ORDER BY created_at DESC,run_id DESC LIMIT ?`
	args = append(args, q.Limit+1)
	rows, err := r.db.QueryContext(ctx, query, args...)
	if err != nil {
		return out, err
	}
	defer rows.Close()
	var lastAt int64
	for rows.Next() {
		v, at, e := scanExecution(rows)
		if e != nil {
			return out, e
		}
		if len(out.Items) == q.Limit {
			out.Next = encode(scope, lastAt, out.Items[len(out.Items)-1].ID)
			break
		}
		out.Items = append(out.Items, v)
		lastAt = at
	}
	return out, rows.Err()
}
func (r *Reader) Execution(ctx context.Context, id string) (task.ExecutionDetail, error) {
	var out task.ExecutionDetail
	if id == "" || len(id) > 256 {
		return out, task.ErrInvalid
	}
	tx, err := r.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return out, err
	}
	defer tx.Rollback()
	v, _, err := scanExecution(tx.QueryRowContext(ctx, executionsCTE+`SELECT `+executionColumns+` FROM executions WHERE run_id=?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return out, task.ErrNotFound
	}
	if err != nil {
		return out, err
	}
	out.Execution = v
	if err = tx.QueryRowContext(ctx, `SELECT t.anchor_task,CASE WHEN t.task_epoch=r.task_epoch THEN t.progress_summary ELSE '' END FROM run r JOIN task t USING(task_id) WHERE r.run_id=?`, id).Scan(&out.Description, &out.ProgressSummary); err != nil {
		return out, err
	}
	runs, err := tx.QueryContext(ctx, executionsCTE+`SELECT `+executionColumns+` FROM executions WHERE task_ref=? ORDER BY task_epoch DESC,created_at DESC,run_id DESC LIMIT 20`, v.TaskRef)
	if err != nil {
		return out, err
	}
	for runs.Next() {
		x, _, e := scanExecution(runs)
		if e != nil {
			runs.Close()
			return out, e
		}
		out.RecentRuns = append(out.RecentRuns, x)
	}
	err = runs.Err()
	runs.Close()
	if err != nil {
		return out, err
	}
	rows, err := tx.QueryContext(ctx, `SELECT i.instruction,i.task_epoch,i.created_at FROM task_instruction i JOIN task t USING(task_id) WHERE t.task_ref=? AND t.agent_id=(SELECT agent_id FROM agent_config WHERE singleton=1) AND i.task_epoch<=? ORDER BY sequence DESC LIMIT 20`, v.TaskRef, v.Epoch)
	if err != nil {
		return out, err
	}
	for rows.Next() {
		var x task.Instruction
		var at int64
		if err = rows.Scan(&x.Text, &x.Epoch, &at); err != nil {
			rows.Close()
			return out, err
		}
		x.At = stamp(at)
		out.Instructions = append(out.Instructions, x)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return out, err
	}
	slices.Reverse(out.Instructions)
	out.Messages, _, err = relatedDeliveries(ctx, tx, v.TaskRef, id, 0)
	if err != nil {
		return out, err
	}
	return out, tx.Commit()
}

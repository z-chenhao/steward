package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"slices"

	"github.com/z-chenhao/assistant/internal/conversation"
	"github.com/z-chenhao/assistant/internal/task"
)

func taskHistory(ctx context.Context, tx *sql.Tx, out *task.Detail, after string) error {
	scope := "history:" + out.TaskRef
	c, err := decode(after, scope)
	if err != nil {
		return err
	}
	rows, err := tx.QueryContext(ctx, `WITH events AS (
 SELECT 'instruction:'||i.instruction_id id,'instruction' kind,i.instruction text,i.created_at at FROM task_instruction i JOIN task t USING(task_id) WHERE t.task_ref=? AND t.agent_id=(SELECT agent_id FROM agent_config WHERE singleton=1)
 UNION ALL SELECT u.event_id,u.kind,'',u.occurred_at FROM task_update u JOIN task t USING(task_id) WHERE t.task_ref=? AND t.agent_id=(SELECT agent_id FROM agent_config WHERE singleton=1)
 ) SELECT id,kind,text,at FROM events WHERE (?='' OR at<? OR (at=? AND id<?)) ORDER BY at DESC,id DESC LIMIT 21`, out.TaskRef, out.TaskRef, after, c.At, c.At, c.Ref)
	if err != nil {
		return err
	}
	defer rows.Close()
	var lastAt int64
	var lastID string
	for rows.Next() {
		var id string
		var at int64
		var v task.History
		if err = rows.Scan(&id, &v.Kind, &v.Text, &at); err != nil {
			return err
		}
		if len(out.History) == 20 {
			out.HistoryNext = encode(scope, lastAt, lastID)
			break
		}
		v.ID = id
		v.At = stamp(at)
		out.History = append(out.History, v)
		lastAt, lastID = at, id
	}
	return rows.Err()
}

// Only Main's finalized deliveries are public Task and execution results.
func relatedDeliveries(ctx context.Context, tx *sql.Tx, taskRef, rootRun string, before int64) ([]conversation.Message, int64, error) {
	query := `SELECT DISTINCT c.sequence,c.message_json FROM task_update u JOIN task t USING(task_id) JOIN main_inbox i USING(event_id) JOIN main_reply m ON m.run_id=i.run_id JOIN conversation_intent c ON json_extract(c.message_json,'$.message_id')=m.message_id WHERE t.task_ref=? AND t.agent_id=(SELECT agent_id FROM agent_config WHERE singleton=1) AND json_extract(c.message_json,'$.conversation_id')=t.conversation_id`
	args := []any{taskRef}
	if rootRun != "" {
		query += " AND u.root_run_id=?"
		args = append(args, rootRun)
	}
	if before > 0 {
		query += " AND c.sequence<?"
		args = append(args, before)
	}
	rows, err := tx.QueryContext(ctx, query+" ORDER BY c.sequence DESC LIMIT 11", args...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	var messages []conversation.Message
	for rows.Next() {
		if len(messages) == 10 {
			return messages, messages[len(messages)-1].Sequence, nil
		}
		var seq int64
		var raw string
		var m conversation.Message
		if err = rows.Scan(&seq, &raw); err != nil {
			return messages, 0, err
		}
		if err = json.Unmarshal([]byte(raw), &m); err != nil {
			return messages, 0, err
		}
		m.Sequence = seq
		messages = append(messages, m)
	}
	return messages, 0, rows.Err()
}

func taskConversation(ctx context.Context, tx *sql.Tx, out *task.Detail, before int64) error {
	messages, next, err := relatedDeliveries(ctx, tx, out.TaskRef, "", before)
	out.Messages, out.DeliveriesBefore = messages, next
	if err != nil {
		return err
	}
	approvals, err := tx.QueryContext(ctx, `SELECT a.approval_id FROM approval a JOIN tool_invocation i USING(invocation_id) JOIN run rr USING(run_id) JOIN task t USING(task_id) WHERE t.task_ref=? AND t.agent_id=(SELECT agent_id FROM agent_config WHERE singleton=1) AND a.status='pending'
 UNION ALL SELECT a.approval_id FROM spend_approval a JOIN run rr ON rr.run_id=a.root_run_id JOIN task t USING(task_id) WHERE t.task_ref=? AND t.agent_id=(SELECT agent_id FROM agent_config WHERE singleton=1) AND a.status='pending'`, out.TaskRef, out.TaskRef)
	if err != nil {
		return err
	}
	for approvals.Next() {
		var id string
		if err = approvals.Scan(&id); err != nil {
			approvals.Close()
			return err
		}
		out.ApprovalIDs = append(out.ApprovalIDs, id)
	}
	err = approvals.Err()
	approvals.Close()
	if err != nil {
		return err
	}
	return tx.QueryRowContext(ctx, `SELECT coalesce(min(c.sequence),0) FROM task_request q JOIN task t ON json_extract(q.receipt_json,'$.taskRef')=t.task_ref JOIN conversation_intent c ON json_extract(c.message_json,'$.message_id')=q.source_message_id AND json_extract(c.message_json,'$.conversation_id')=t.conversation_id WHERE t.task_ref=? AND t.agent_id=(SELECT agent_id FROM agent_config WHERE singleton=1)`, out.TaskRef).Scan(&out.SourceSequence)
}

// Instructions are actual accepted additions, never inferred future steps. The
// newest bounded set is shown in acceptance order independently of history pages.
func taskInstructions(ctx context.Context, tx *sql.Tx, out *task.Detail) error {
	rows, err := tx.QueryContext(ctx, `SELECT i.instruction,i.created_at,i.task_epoch FROM task_instruction i JOIN task t USING(task_id) WHERE t.task_ref=? AND t.agent_id=(SELECT agent_id FROM agent_config WHERE singleton=1) ORDER BY i.sequence DESC LIMIT 21`, out.TaskRef)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var v task.Instruction
		var at int64
		if err = rows.Scan(&v.Text, &at, &v.Epoch); err != nil {
			return err
		}
		if len(out.Instructions) == 20 {
			out.MoreInstructions = true
			break
		}
		v.At = stamp(at)
		out.Instructions = append(out.Instructions, v)
	}
	slices.Reverse(out.Instructions)
	return rows.Err()
}

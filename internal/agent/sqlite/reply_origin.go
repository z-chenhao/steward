package sqlite

import (
	"context"
	"database/sql"
	"github.com/z-chenhao/assistant/internal/interaction"
)

// This private transport reference is earned by a consumed, bound input. It is
// never model-provided and conveys no Task or Tool authority.
type replyOrigin struct{ binding, source, trialTask string }

func taskReplyOrigin(ctx context.Context, tx *sql.Tx, taskID string) (replyOrigin, error) {
	var origin replyOrigin
	err := tx.QueryRowContext(ctx, `SELECT m.binding_id,m.reply_source_id FROM task t
 JOIN task_request q ON json_extract(q.receipt_json,'$.taskRef')=t.task_ref
 JOIN tool_invocation v ON v.invocation_id=q.invocation_id
 JOIN message_route m ON m.message_id=q.source_message_id
 WHERE t.task_id=? AND json_extract(v.arguments_json,'$.operation')='create' AND m.reply_source_id!=''
 ORDER BY v.created_at,v.invocation_id LIMIT 1`, taskID).Scan(&origin.binding, &origin.source)
	if err == sql.ErrNoRows {
		// Keep each owner-initiated trial in its own follow-up and return its
		// approvals and finalized answer only to the original workspace.
		err = tx.QueryRowContext(ctx, `SELECT b.binding_id,a.task_id FROM automation a JOIN channel_binding b ON b.channel='web' AND b.subject=? AND b.destination=? WHERE a.automation_id='trial:'||? AND a.task_id=?`, interaction.WebSubject, interaction.WebDestination, taskID, taskID).Scan(&origin.binding, &origin.trialTask)
		if err == sql.ErrNoRows {
			return replyOrigin{}, nil
		}
	}
	return origin, err
}

func runReplyOrigin(ctx context.Context, tx *sql.Tx, runID string) (replyOrigin, error) {
	// Worker approval/needs-input notices use the same original Task source.
	var taskID sql.NullString
	if err := tx.QueryRowContext(ctx, `SELECT task_id FROM run WHERE run_id=?`, runID).Scan(&taskID); err != nil {
		return replyOrigin{}, err
	}
	if taskID.Valid {
		return taskReplyOrigin(ctx, tx, taskID.String)
	}
	rows, err := tx.QueryContext(ctx, `SELECT u.task_id FROM main_inbox b JOIN task_update u USING(event_id) WHERE b.run_id=?
 UNION SELECT task_id FROM github_task_followup WHERE run_id=?`, runID, runID)
	if err != nil {
		return replyOrigin{}, err
	}
	var tasks []string
	for rows.Next() {
		var id string
		if err = rows.Scan(&id); err != nil {
			rows.Close()
			return replyOrigin{}, err
		}
		tasks = append(tasks, id)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return replyOrigin{}, err
	}
	var selected replyOrigin
	for i, id := range tasks {
		origin, err := taskReplyOrigin(ctx, tx, id)
		if err != nil {
			return replyOrigin{}, err
		}
		if i > 0 && selected != origin {
			return replyOrigin{}, nil
		}
		selected = origin
	}
	return selected, nil
}

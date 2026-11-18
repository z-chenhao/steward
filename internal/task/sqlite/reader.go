// Package sqlite reads the owner's task workspace through an independent,
// lazy read-only pool. Browsing never controls Tasks or dispatches work.
package sqlite

import (
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/z-chenhao/assistant/internal/task"
	_ "modernc.org/sqlite"
)

type Reader struct{ db *sql.DB }

func Open(path string) (*Reader, error) {
	u := url.URL{Scheme: "file", Path: path}
	q := u.Query()
	q.Set("mode", "ro")
	q.Add("_pragma", "query_only(1)")
	q.Add("_pragma", "busy_timeout(500)")
	u.RawQuery = q.Encode()
	db, err := sql.Open("sqlite", u.String())
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(2)
	db.SetMaxIdleConns(2)
	return &Reader{db: db}, nil
}
func (r *Reader) Close() error { return r.db.Close() }

type cursor struct {
	Scope string
	At    int64
	Ref   string
}

func decode(raw, scope string) (cursor, error) {
	if raw == "" {
		return cursor{}, nil
	}
	b, e := base64.RawURLEncoding.DecodeString(raw)
	var c cursor
	if e != nil || json.Unmarshal(b, &c) != nil || c.Scope != scope || c.At <= 0 || len(c.Ref) > 300 || c.Ref == "" {
		return c, task.ErrInvalid
	}
	return c, nil
}
func encode(scope string, at int64, ref string) string {
	b, _ := json.Marshal(cursor{scope, at, ref})
	return base64.RawURLEncoding.EncodeToString(b)
}
func stamp(at int64) string { return time.UnixMicro(at).UTC().Format(time.RFC3339Nano) }

// Aggregate only the current epoch, retaining the distinction between Task
// state and a failed/stopped attempt. No Worker result is a user delivery.
const progressCTE = `WITH current_runs AS (
 SELECT r.* FROM run r JOIN task t ON t.task_id=r.task_id AND t.task_epoch=r.task_epoch
 WHERE t.agent_id=(SELECT agent_id FROM agent_config WHERE singleton=1)
), counts AS (
 SELECT task_id,max(updated_at) updated_at,
 sum(worker_position='child' AND status='queued') queued,
 sum(worker_position='child' AND status='running') running,
 sum(worker_position='child' AND status='completed') succeeded,
 sum(worker_position='child' AND status IN ('failed','cancelled')) failed,
 sum(worker_position='child' AND status='waiting') waiting,
 max(status='waiting' AND wait_reason='approval') approval,
 max(status='waiting' AND wait_reason='user_input') input
 FROM current_runs GROUP BY task_id
), workspace AS (
 SELECT t.*,max(t.updated_at,coalesce(c.updated_at,0)) activity_at,
 coalesce(root.status,'') root_status,coalesce(root.wait_reason,'') wait_reason,
 coalesce(c.queued,0) queued,coalesce(c.running,0) running,coalesce(c.succeeded,0) succeeded,coalesce(c.failed,0) failed,coalesce(c.waiting,0) waiting,
 (coalesce(c.approval,0) OR EXISTS(SELECT 1 FROM spend_approval a WHERE a.root_run_id=root.run_id AND a.status='pending')) approval,
 coalesce(c.input,0) input,
 EXISTS(SELECT 1 FROM tool_invocation i JOIN current_runs r USING(run_id) WHERE r.task_id=t.task_id AND i.status='unknown') unknown
 ,coalesce(a.automation_id,'') schedule_id,coalesce(a.instruction,'') schedule_instruction,coalesce(a.trigger_type,'') trigger_type,coalesce(a.trigger_spec,'') trigger_spec,coalesce(a.timezone,'') timezone,coalesce(a.version,0) schedule_revision,coalesce(a.enabled,0) schedule_enabled,a.next_fire_at,a.below_value,a.presentation_json schedule_presentation,
 EXISTS(SELECT 1 FROM rsi_graph g,json_each(g.graph_json,'$.bindings') b JOIN rsi_package p ON p.digest=json_extract(b.value,'$.package_digest'),json_each(p.manifest_json,'$.provides') x WHERE g.revision=a.method_graph AND json_extract(x.value,'$.contract')='steward.schedule-display/1') schedule_display_managed,coalesce(a.intent_privacy_epoch=coalesce((SELECT epoch FROM memory_privacy WHERE agent_id=t.agent_id),0),0) schedule_visible
 FROM task t LEFT JOIN counts c USING(task_id) LEFT JOIN current_runs root ON root.task_id=t.task_id AND root.worker_position='root' LEFT JOIN automation a ON a.task_id=t.task_id AND a.agent_id=t.agent_id AND a.automation_id NOT LIKE 'trial:%'
 WHERE t.agent_id=(SELECT agent_id FROM agent_config WHERE singleton=1)
)
`
const columns = `task_ref,title,status,progress_summary,activity_at,created_at,task_epoch,root_status,wait_reason,queued,running,succeeded,failed,waiting,approval,input,unknown,anchor_task,completion_policy,schedule_id,schedule_instruction,trigger_type,trigger_spec,timezone,schedule_revision,schedule_enabled,next_fire_at,below_value,schedule_presentation,schedule_display_managed,schedule_visible`

type scanner interface{ Scan(...any) error }

func scan(row scanner) (task.Entry, int64, error) {
	var v task.Entry
	var at, created int64
	p := &v.Progress
	var schedule task.Schedule
	var scheduleID string
	var visible, displayManaged bool
	var presentation sql.NullString
	var next, below sql.NullInt64
	err := row.Scan(&v.TaskRef, &v.Title, &v.Status, &v.ProgressSummary, &at, &created, &v.Epoch, &p.Root, &p.Wait, &p.Queued, &p.Running, &p.Succeeded, &p.Failed, &p.Waiting, &p.Approval, &p.Input, &p.Unknown, &v.GoalSummary, &v.CompletionPolicy, &scheduleID, &schedule.Instruction, &schedule.Type, &schedule.Spec, &schedule.Timezone, &schedule.Version, &schedule.Enabled, &next, &below, &presentation, &displayManaged, &visible)
	if err != nil {
		return v, created, err
	}
	if scheduleID != "" {
		if !visible {
			schedule = task.Schedule{Version: schedule.Version, Enabled: schedule.Enabled, Restricted: true}
		} else {
			schedule.Presentation, err = decodeSchedulePresentation(presentation, displayManaged, schedule.Type)
			if err != nil {
				return v, created, err
			}
			if next.Valid {
				schedule.NextAt = stamp(next.Int64)
			}
			if below.Valid {
				value := below.Int64
				schedule.Below = &value
			}
		}
		schedule.Ref = scheduleID
		v.Schedule = &schedule
	}
	v.UpdatedAt = stamp(at)
	v.CreatedAt = stamp(created)
	v.Stage = p.Stage(v.Status)
	v.NeedsAttention = v.Status == "active" && p.Attention()
	return v, created, err
}

func (r *Reader) List(ctx context.Context, q task.ListQuery) (task.List, error) {
	out := task.List{Items: []task.Entry{}}
	if q.Validate() != nil {
		return out, task.ErrInvalid
	}
	if q.View == "" {
		q.View = "open"
	}
	if q.Limit == 0 {
		q.Limit = 25
	}
	scope := "list:" + q.View + ":" + q.Query
	c, err := decode(q.Cursor, scope)
	if err != nil {
		return out, err
	}
	query := progressCTE + `SELECT ` + columns + ` FROM workspace WHERE 1=1`
	args := []any{}
	switch q.View {
	case "open":
		query += ` AND status IN ('active','paused')`
	case "closed":
		query += ` AND status IN ('completed','cancelled')`
	case "attention":
		query += ` AND status='active' AND (approval OR input OR unknown OR root_status='failed')`
	}
	if q.Query != "" {
		query += ` AND (instr(lower(title),lower(?))>0 OR instr(lower(anchor_task),lower(?))>0)`
		args = append(args, q.Query, q.Query)
	}
	if q.Cursor != "" {
		query += ` AND (created_at<? OR (created_at=? AND task_ref<?))`
		args = append(args, c.At, c.At, c.Ref)
	}
	query += ` ORDER BY created_at DESC,task_ref DESC LIMIT ?`
	args = append(args, q.Limit+1)
	rows, err := r.db.QueryContext(ctx, query, args...)
	if err != nil {
		return out, err
	}
	defer rows.Close()
	var lastAt int64
	for rows.Next() {
		v, created, e := scan(rows)
		if e != nil {
			return out, e
		}
		if len(out.Items) == q.Limit {
			out.Next = encode(scope, lastAt, out.Items[len(out.Items)-1].TaskRef)
			break
		}
		out.Items = append(out.Items, v)
		lastAt = created
	}
	return out, rows.Err()
}

func (r *Reader) Detail(ctx context.Context, ref, after string) (task.Detail, error) {
	return r.DetailPage(ctx, ref, after, 0)
}

func (r *Reader) DetailPage(ctx context.Context, ref, after string, before int64) (task.Detail, error) {
	var out task.Detail
	if !task.ValidRef(ref) || len(after) > 1024 || before < 0 {
		return out, task.ErrInvalid
	}
	tx, err := r.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return out, err
	}
	defer tx.Rollback()
	v, _, err := scan(tx.QueryRowContext(ctx, progressCTE+`SELECT `+columns+` FROM workspace WHERE task_ref=?`, ref))
	if errors.Is(err, sql.ErrNoRows) {
		return out, task.ErrNotFound
	}
	if err != nil {
		return out, err
	}
	out.Entry = v
	var raw string
	err = tx.QueryRowContext(ctx, `SELECT anchor_task,anchor_artifact_refs FROM task WHERE task_ref=? AND agent_id=(SELECT agent_id FROM agent_config WHERE singleton=1)`, ref).Scan(&out.GoalSummary, &raw)
	if err != nil {
		return out, err
	}
	if json.Unmarshal([]byte(raw), &out.Artifacts) != nil {
		return out, task.ErrInvalid
	}
	if err = taskInstructions(ctx, tx, &out); err != nil {
		return out, err
	}
	if err = taskHistory(ctx, tx, &out, after); err != nil {
		return out, err
	}
	if err = taskConversation(ctx, tx, &out, before); err != nil {
		return out, err
	}
	return out, tx.Commit()
}

// Activity retains the existing terminal/API summary contract while sharing
// the same facts and read-only pool with the paginated Web workspace.
func (r *Reader) Activity(ctx context.Context) ([]task.Summary, error) {
	rows, err := r.db.QueryContext(ctx, progressCTE+`SELECT `+columns+` FROM workspace ORDER BY activity_at DESC,task_ref LIMIT 20`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []task.Summary{}
	for rows.Next() {
		v, _, e := scan(rows)
		if e != nil {
			return nil, e
		}
		stage := strings.ReplaceAll(strings.ReplaceAll(v.Stage, "the task", "the work"), "your approval", "approval") + "."
		parts := []string{stage}
		if v.ProgressSummary != "" {
			parts = append(parts, "Last update: "+v.ProgressSummary)
		}
		p := v.Progress
		if p.Children() > 0 {
			parts = append(parts, fmt.Sprintf("Children: queued %d, running %d, succeeded %d, failed %d, waiting %d (%d/%d finished)", p.Queued, p.Running, p.Succeeded, p.Failed, p.Waiting, p.Succeeded+p.Failed, p.Children()))
		}
		v.Summary.ProgressSummary = strings.Join(parts, " ")
		v.Summary.GoalSummary = "" // Preserve the existing terminal activity projection.
		out = append(out, v.Summary)
	}
	return out, rows.Err()
}

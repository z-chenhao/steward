// Package sqlite owns bounded diagnostic projections, a read-only inspector,
// and the independent optional dispatch recorder. It never writes domain state.
package sqlite

import (
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/url"
	"sync/atomic"
	"time"

	"github.com/z-chenhao/assistant/internal/observability"
	"github.com/z-chenhao/assistant/internal/rsi"
	_ "modernc.org/sqlite"
)

type Inspector struct {
	db      *sql.DB
	guard   *sql.DB
	plugins *atomic.Pointer[rsi.Catalog]
}

// Open creates a lazy pool: a failed observation read never blocks Runtime
// startup or takes its writer slot. mode=ro and query_only enforce this boundary.
func Open(path string) (*Inspector, error) {
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
	// Source snapshots may occupy both reader connections. Revocation checks
	// need a fresh snapshot, so they must not wait for their own source pool.
	guard, err := sql.Open("sqlite", u.String())
	if err != nil {
		db.Close()
		return nil, err
	}
	guard.SetMaxOpenConns(2)
	guard.SetMaxIdleConns(2)
	return &Inspector{db: db, guard: guard, plugins: &atomic.Pointer[rsi.Catalog]{}}, nil
}
func (s *Inspector) Close() error {
	var err error
	if s.guard != nil {
		err = s.guard.Close()
	}
	return errors.Join(s.db.Close(), err)
}

const executionColumns = `r.run_id,CASE WHEN r.kind='main' THEN 'main' ELSE r.worker_position END,r.status,coalesce(r.wait_reason,''),coalesce(r.parent_run_id,''),coalesce(r.root_run_id,''),r.depth,coalesce(r.task_epoch,0),coalesce(t.task_ref,''),r.created_at,r.started_at,r.ended_at`
const executionFrom = ` FROM run r LEFT JOIN task t ON t.task_id=r.task_id WHERE r.agent_id=(SELECT agent_id FROM agent_config WHERE singleton=1)`

type scanner interface{ Scan(...any) error }

func scanExecution(row scanner) (observability.Execution, int64, error) {
	var v observability.Execution
	var created int64
	var started, ended sql.NullInt64
	err := row.Scan(&v.ID, &v.Role, &v.Status, &v.WaitReason, &v.Parent, &v.Root, &v.Depth, &v.Epoch, &v.TaskRef, &created, &started, &ended)
	v.CreatedAt = stamp(created)
	if started.Valid {
		v.StartedAt = stamp(started.Int64)
	}
	if ended.Valid {
		v.EndedAt = stamp(ended.Int64)
	}
	return v, created, err
}
func stamp(at int64) string { return time.UnixMicro(at).UTC().Format(time.RFC3339Nano) }
func pageLimit(n int) int {
	if n == 0 {
		return 40
	}
	return n
}

type cursor struct {
	Scope string `json:"s"`
	At    int64  `json:"a"`
	ID    string `json:"i"`
}

func readCursor(raw, scope string) (cursor, error) {
	if raw == "" {
		return cursor{}, nil
	}
	var c cursor
	b, err := base64.RawURLEncoding.DecodeString(raw)
	if err != nil || json.Unmarshal(b, &c) != nil || c.Scope != scope || c.ID == "" || len(c.ID) > 256 {
		return c, observability.ErrInvalid
	}
	return c, nil
}
func nextCursor(scope string, at int64, id string) string {
	b, _ := json.Marshal(cursor{scope, at, id})
	return base64.RawURLEncoding.EncodeToString(b)
}

func (s Inspector) Executions(ctx context.Context, q observability.ExecutionQuery) (observability.Executions, error) {
	out := observability.Executions{Items: []observability.Execution{}}
	if err := q.Validate(); err != nil {
		return out, err
	}
	c, err := readCursor(q.Cursor, "executions")
	if err != nil {
		return out, err
	}
	query := "SELECT " + executionColumns + executionFrom
	args := []any{}
	if q.Role == "main" {
		query += " AND r.kind='main'"
	} else if q.Role != "" {
		query += " AND r.worker_position=?"
		args = append(args, q.Role)
	}
	if q.Status != "" {
		query += " AND r.status=?"
		args = append(args, q.Status)
	}
	if !q.Since.IsZero() {
		query += " AND r.created_at>=?"
		args = append(args, q.Since.UnixMicro())
	}
	if !q.Until.IsZero() {
		query += " AND r.created_at<?"
		args = append(args, q.Until.UnixMicro())
	}
	if c.ID != "" {
		query += " AND (r.created_at,r.run_id)<(?,?)"
		args = append(args, c.At, c.ID)
	}
	limit := pageLimit(q.Limit)
	query += " ORDER BY r.created_at DESC,r.run_id DESC LIMIT ?"
	args = append(args, limit+1)
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return out, err
	}
	defer rows.Close()
	var at int64
	for rows.Next() {
		v, created, err := scanExecution(rows)
		if err != nil {
			return out, err
		}
		if len(out.Items) == limit {
			out.Next = nextCursor("executions", at, out.Items[limit-1].ID)
			break
		}
		out.Items = append(out.Items, v)
		at = created
	}
	return out, rows.Err()
}

func execution(ctx context.Context, tx *sql.Tx, id string) (observability.Execution, error) {
	v, _, err := scanExecution(tx.QueryRowContext(ctx, "SELECT "+executionColumns+executionFrom+" AND r.run_id=?", id))
	if errors.Is(err, sql.ErrNoRows) {
		err = observability.ErrNotFound
	}
	return v, err
}

func (s Inspector) Relationships(ctx context.Context, q observability.RelationshipQuery) (observability.Relationships, error) {
	out := observability.Relationships{Executions: []observability.Execution{}, Tasks: []observability.TaskLink{}}
	if err := q.Validate(); err != nil {
		return out, err
	}
	// A short deferred snapshot makes all relationship reads consistent while
	// WAL writers retain their independent authoritative connection.
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return out, err
	}
	defer tx.Rollback()
	if q.RunID != "" {
		selected, err := execution(ctx, tx, q.RunID)
		if err != nil {
			return out, err
		}
		out.Selected = &selected
		if selected.Role == "main" {
			err = relatedTasks(ctx, tx, q, &out)
			return out, err
		}
		q.TaskRef = selected.TaskRef
	}
	var taskID string
	var created int64
	task := observability.TaskNode{}
	err = tx.QueryRowContext(ctx, `SELECT task_id,task_ref,substr(title,1,240),length(title)>240,status,created_at FROM task WHERE task_ref=? AND agent_id=(SELECT agent_id FROM agent_config WHERE singleton=1)`, q.TaskRef).Scan(&taskID, &task.Ref, &task.Title, &task.TitleTruncated, &task.Status, &created)
	if errors.Is(err, sql.ErrNoRows) {
		err = observability.ErrNotFound
	}
	if err != nil {
		return out, err
	}
	task.CreatedAt = stamp(created)
	out.Task = &task
	var creator string
	err = tx.QueryRowContext(ctx, `SELECT i.run_id FROM task_request q JOIN tool_invocation i USING(invocation_id) JOIN run r ON r.run_id=i.run_id WHERE json_extract(q.receipt_json,'$.taskRef')=? AND json_extract(i.arguments_json,'$.operation')='create' AND r.agent_id=(SELECT agent_id FROM agent_config WHERE singleton=1) ORDER BY i.created_at,i.invocation_id LIMIT 1`, q.TaskRef).Scan(&creator)
	if err == nil {
		v, e := execution(ctx, tx, creator)
		if e != nil {
			return out, e
		}
		out.Creator = &v
	} else if !errors.Is(err, sql.ErrNoRows) {
		return out, err
	}
	err = taskExecutions(ctx, tx, taskID, q, &out)
	return out, err
}

func taskExecutions(ctx context.Context, tx *sql.Tx, taskID string, q observability.RelationshipQuery, out *observability.Relationships) error {
	scope := "task:" + q.TaskRef
	c, err := readCursor(q.Cursor, scope)
	if err != nil {
		return err
	}
	query := "SELECT " + executionColumns + executionFrom + " AND r.task_id=?"
	args := []any{taskID}
	if c.ID != "" {
		query += " AND (r.created_at,r.run_id)>(?,?)"
		args = append(args, c.At, c.ID)
	}
	limit := pageLimit(q.Limit)
	query += " ORDER BY r.created_at,r.run_id LIMIT ?"
	args = append(args, limit+1)
	rows, err := tx.QueryContext(ctx, query, args...)
	if err != nil {
		return err
	}
	defer rows.Close()
	var at int64
	for rows.Next() {
		v, created, err := scanExecution(rows)
		if err != nil {
			return err
		}
		if len(out.Executions) == limit {
			out.Next = nextCursor(scope, at, out.Executions[limit-1].ID)
			break
		}
		out.Executions = append(out.Executions, v)
		at = created
	}
	return rows.Err()
}

func relatedTasks(ctx context.Context, tx *sql.Tx, q observability.RelationshipQuery, out *observability.Relationships) error {
	scope := "main:" + q.RunID
	c, err := readCursor(q.Cursor, scope)
	if err != nil {
		return err
	}
	query := `SELECT t.task_ref,substr(t.title,1,240),length(t.title)>240,t.status,t.created_at,coalesce(json_extract(i.arguments_json,'$.operation'),'unknown'),i.invocation_id,i.created_at FROM task_request q JOIN tool_invocation i USING(invocation_id) JOIN task t ON t.task_ref=json_extract(q.receipt_json,'$.taskRef') JOIN run r ON r.run_id=i.run_id WHERE i.run_id=? AND t.agent_id=r.agent_id AND r.agent_id=(SELECT agent_id FROM agent_config WHERE singleton=1)`
	args := []any{q.RunID}
	if c.ID != "" {
		query += " AND (i.created_at,i.invocation_id)>(?,?)"
		args = append(args, c.At, c.ID)
	}
	limit := pageLimit(q.Limit)
	query += " ORDER BY i.created_at,i.invocation_id LIMIT ?"
	args = append(args, limit+1)
	rows, err := tx.QueryContext(ctx, query, args...)
	if err != nil {
		return err
	}
	defer rows.Close()
	var at int64
	for rows.Next() {
		var link observability.TaskLink
		var created, accepted int64
		if err = rows.Scan(&link.Ref, &link.Title, &link.TitleTruncated, &link.Status, &created, &link.Operation, &link.Invocation, &accepted); err != nil {
			return err
		}
		if len(out.Tasks) == limit {
			out.Next = nextCursor(scope, at, out.Tasks[limit-1].Invocation)
			break
		}
		link.CreatedAt = stamp(created)
		link.AcceptedAt = stamp(accepted)
		out.Tasks = append(out.Tasks, link)
		at = accepted
	}
	return rows.Err()
}

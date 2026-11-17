package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/z-chenhao/assistant/internal/rsi"
	"github.com/z-chenhao/assistant/pkg/rsiplugin"
)

// Work consumes committed lifecycle events with a separate durable lease. It
// never borrows a released Run claim or executes a second agent loop.
func (s *Store) Work(ctx context.Context, catalog rsi.Catalog) (int, error) {
	count := 0
	for count < 100 {
		var id, run string
		var payload sql.NullString
		token := uuid.NewString()
		err := s.write(ctx, func(tx *sql.Tx) error {
			now := time.Now().UnixMicro()
			err := tx.QueryRowContext(ctx, `SELECT e.event_id,e.run_id,e.input_json FROM rsi_event e JOIN run r USING(run_id) WHERE e.status='pending' OR (e.status='running' AND e.lease_until<=?) ORDER BY e.rowid LIMIT 1`, now).Scan(&id, &run, &payload)
			if err != nil {
				return err
			}
			res, err := tx.ExecContext(ctx, `UPDATE rsi_event SET status='running',token=?,lease_until=? WHERE event_id=? AND (status='pending' OR lease_until<=?)`, token, now+int64(time.Minute/time.Microsecond), id, now)
			if err != nil {
				return err
			}
			n, _ := res.RowsAffected()
			if n != 1 {
				return rsi.ErrConflict
			}
			return nil
		})
		if errors.Is(err, sql.ErrNoRows) {
			return count, nil
		}
		if err != nil {
			return count, err
		}
		consumer := *s
		consumer.Fence = func(ctx context.Context, tx *sql.Tx) error {
			var valid bool
			now := time.Now().UnixMicro()
			err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM rsi_event e JOIN run r USING(run_id) LEFT JOIN memory_privacy p USING(agent_id) LEFT JOIN task t USING(task_id) WHERE e.event_id=? AND e.token=? AND e.status='running' AND e.lease_until>? AND r.desired_state!='memory_forgotten' AND r.created_at>coalesce(p.updated_at,0) AND (r.task_id IS NULL OR (t.task_epoch=r.task_epoch AND t.status!='cancelled')))`, id, token, now).Scan(&valid)
			if err != nil {
				return err
			}
			if !valid {
				return rsi.ErrRevoked
			}
			_, err = tx.ExecContext(ctx, `UPDATE rsi_event SET lease_until=? WHERE event_id=? AND token=?`, now+int64(time.Minute/time.Microsecond), id, token)
			return err
		}
		var input rsiplugin.HookInput
		var hookErr error
		if !payload.Valid {
			hookErr = rsi.ErrRevoked
		} else {
			hookErr = rsiplugin.Decode([]byte(payload.String), &input)
		}
		if hookErr == nil {
			_, hookErr = (rsi.Host{Catalog: catalog, Store: &consumer}).Hook(ctx, input)
		}
		status, code := "completed", ""
		if hookErr != nil {
			status = "failed"
			code = "PLUGIN_EVENT_FAILED"
			if errors.Is(hookErr, rsi.ErrUnknown) {
				code = "RESULT_UNKNOWN"
			}
			if errors.Is(hookErr, rsi.ErrRevoked) {
				code = "REVOKED"
			}
		}
		commitCtx := context.WithoutCancel(ctx)
		err = s.write(commitCtx, func(tx *sql.Tx) error {
			res, e := tx.ExecContext(commitCtx, `UPDATE rsi_event SET status=?,error_code=?,token=NULL,lease_until=0 WHERE event_id=? AND token=?`, status, code, id, token)
			if e != nil {
				return e
			}
			n, _ := res.RowsAffected()
			if n != 1 {
				return rsi.ErrConflict
			}
			return audit(commitCtx, tx, "event."+status, id, struct{ Code string }{code})
		})
		if err != nil {
			return count, err
		}
		count++
	}
	return count, nil
}

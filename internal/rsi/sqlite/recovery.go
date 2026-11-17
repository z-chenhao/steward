package sqlite

import (
	"context"
	"database/sql"

	"github.com/z-chenhao/assistant/internal/rsi"
)

func (s *Store) Calls(ctx context.Context, run string) ([]rsi.CallStatus, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT instance_id,operation_id,result_json IS NOT NULL FROM rsi_call WHERE run_id=? ORDER BY rowid LIMIT 200`, run)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []rsi.CallStatus{}
	for rows.Next() {
		var item rsi.CallStatus
		if err = rows.Scan(&item.InstanceID, &item.OperationID, &item.Completed); err != nil {
			return nil, err
		}
		out = append(out, item)
	}
	return out, rows.Err()
}

// RetryCall is an explicit owner reconciliation for isolated pure computation.
// A completed output cannot be discarded. No SDK or hook exposes this control.
func (s *Store) RetryCall(ctx context.Context, run, instance, key string) error {
	return s.write(ctx, func(tx *sql.Tx) error {
		if _, err := binding(ctx, tx, run); err != nil {
			return err
		}
		res, err := tx.ExecContext(ctx, `DELETE FROM rsi_call WHERE run_id=? AND instance_id=? AND operation_id=? AND result_json IS NULL AND EXISTS(SELECT 1 FROM run r LEFT JOIN memory_privacy p USING(agent_id) WHERE r.run_id=? AND r.desired_state='running' AND r.created_at>coalesce(p.updated_at,0))`, run, instance, key, run)
		if err != nil {
			return err
		}
		n, _ := res.RowsAffected()
		if n != 1 {
			return rsi.ErrConflict
		}
		return audit(ctx, tx, "call.retry_authorized", key, struct{ Run, Instance string }{run, instance})
	})
}

func (s *Store) RetryEvent(ctx context.Context, id string) error {
	return s.write(ctx, func(tx *sql.Tx) error {
		res, err := tx.ExecContext(ctx, `UPDATE rsi_event SET status='pending',error_code='',token=NULL,lease_until=0 WHERE event_id=? AND status='failed' AND error_code!='REVOKED' AND input_json IS NOT NULL`, id)
		if err != nil {
			return err
		}
		n, _ := res.RowsAffected()
		if n != 1 {
			return rsi.ErrConflict
		}
		return audit(ctx, tx, "event.retry_authorized", id, struct{}{})
	})
}

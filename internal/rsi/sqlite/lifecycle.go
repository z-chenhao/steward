package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"

	"github.com/z-chenhao/assistant/internal/rsi"
	"github.com/z-chenhao/assistant/pkg/rsiplugin"
)

// Deactivate is an explicit owner operation: new Runs select the built-in
// composition while existing bindings keep their retained graph and resources.
func (s *Store) Deactivate(ctx context.Context, operation, expected string) (rsi.Receipt, error) {
	var out rsi.Receipt
	if operation == "" || len(operation) > 256 {
		return out, rsi.ErrInvalid
	}
	hash, _ := rsiplugin.Digest("steward.rsi.deactivate/1", struct{ Operation, Expected string }{operation, expected})
	err := s.write(ctx, func(tx *sql.Tx) error {
		var prior, value string
		err := tx.QueryRowContext(ctx, `SELECT request_hash,receipt_json FROM rsi_activation WHERE operation_id=?`, operation).Scan(&prior, &value)
		if err == nil {
			if prior != hash {
				return rsi.ErrConflict
			}
			return json.Unmarshal([]byte(value), &out)
		}
		if err != sql.ErrNoRows {
			return err
		}
		var current string
		var epoch int64
		if err = tx.QueryRowContext(ctx, `SELECT coalesce(active_graph,''),epoch FROM rsi_control WHERE singleton=1`).Scan(&current, &epoch); err != nil {
			return err
		}
		if current != expected {
			return rsi.ErrConflict
		}
		res, err := tx.ExecContext(ctx, `UPDATE rsi_control SET active_graph=NULL,epoch=epoch+1 WHERE singleton=1 AND epoch=?`, epoch)
		if err != nil {
			return err
		}
		n, _ := res.RowsAffected()
		if n != 1 {
			return rsi.ErrConflict
		}
		out = rsi.Receipt{OperationID: operation, Previous: current, Epoch: epoch + 1}
		if _, err = tx.ExecContext(ctx, `INSERT INTO rsi_activation VALUES(?,?,?)`, operation, hash, raw(out)); err != nil {
			return err
		}
		return audit(ctx, tx, "graph.deactivated", operation, out)
	})
	return out, err
}
func (s *Store) Retire(ctx context.Context, digest string) error {
	return s.write(ctx, func(tx *sql.Tx) error {
		var active bool
		if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM rsi_control c JOIN rsi_graph g ON g.revision=c.active_graph,json_each(g.graph_json,'$.bindings') b WHERE json_extract(b.value,'$.package_digest')=?)`, digest).Scan(&active); err != nil {
			return err
		}
		if active {
			return rsi.ErrConflict
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO rsi_retired VALUES(?) ON CONFLICT DO NOTHING`, digest); err != nil {
			return err
		}
		return audit(ctx, tx, "package.retired", digest, struct{}{})
	})
}

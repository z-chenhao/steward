package sqlite

import (
	"context"
	"database/sql"

	"github.com/z-chenhao/assistant/internal/agent"
)

// MainActivity observes ingress without injecting it into a frozen exchange.
// Native call/result pairs and accepted generation requests remain immutable.
func (r *Runs) MainActivity(ctx context.Context, c agent.RunClaim, after, step int64) (agent.MainActivity, error) {
	var out agent.MainActivity
	err := r.read(ctx, func(tx *sql.Tx) error {
		if err := fence(ctx, tx, c); err != nil {
			return err
		}
		return tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM main_input WHERE run_id=? AND sequence>?),
 EXISTS(SELECT 1 FROM model_generation WHERE run_id=? AND step_index=?) OR
 EXISTS(SELECT 1 FROM context_compaction WHERE run_id=? AND step_index=?)`, c.RunID(), after, c.RunID(), step, c.RunID(), step).Scan(&out.Input, &out.Frozen)
	})
	return out, err
}

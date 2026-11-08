package sqlite

import (
	"context"
	"database/sql"
	"time"
)

// Offered evidence is separately measurable from model uptake or answer quality.
// Replaying a completed invocation never increments the count.
func recordMemoryAccess(ctx context.Context, tx *sql.Tx, runID, invocation, operation string, refs []string) error {
	for _, ref := range refs {
		if _, e := tx.ExecContext(ctx, `INSERT OR IGNORE INTO memory_access SELECT t.invocation_id,r.agent_id,?,?,? FROM tool_invocation t JOIN run r USING(run_id) WHERE t.invocation_id=? AND t.run_id=? AND t.status='running'`, ref, operation, time.Now().UnixMicro(), invocation, runID); e != nil {
			return e
		}
	}
	return nil
}

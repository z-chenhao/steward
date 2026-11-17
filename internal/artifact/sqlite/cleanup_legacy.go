package sqlite

import (
	"context"
	"database/sql"
	"time"

	"github.com/z-chenhao/assistant/internal/artifact"
)

// Actual consumers: schema69 migration-only native-owner installations and
// explicitly native SDK fixtures. Activation permanently closes this entry;
// delete after those original consumers are retired. Never a plugin fallback.
// CleanupLegacy is explicitly dry-run by default at the CLI. Referenced objects and
// active/paused Task outputs cannot become candidates. Recheck under one lock.
func (s *Store) CleanupLegacy(ctx context.Context, a artifact.Scope, dry bool) (artifact.CleanupReport, error) {
	report := artifact.CleanupReport{DryRun: dry, Artifacts: []string{}}
	err := s.transaction(ctx, func(tx *sql.Tx) error {
		if !a.Owner {
			return artifact.ErrNotFound
		}
		if err := s.scope(ctx, tx, a); err != nil {
			return err
		}

		managed, err := s.ManagedOwnerTx(ctx, tx)
		if err != nil {
			return err
		}
		if managed {
			return artifact.ErrUnavailable
		}
		rows, err := tx.QueryContext(ctx, `SELECT artifact_ref,version FROM artifact a WHERE owner_user_id=? AND owner_agent_id=? AND deleted_at IS NULL AND expires_at<=? AND NOT EXISTS(SELECT 1 FROM artifact_reference r WHERE r.artifact_ref=a.artifact_ref) AND NOT EXISTS(SELECT 1 FROM task t WHERE t.task_id=a.task_id AND (t.status IN ('active','paused') OR coalesce(t.completed_at,t.cancelled_at,t.created_at)+a.expires_at-a.created_at>?)) ORDER BY expires_at,artifact_ref LIMIT ?`, a.UserID, a.AgentID, time.Now().UnixMicro(), time.Now().UnixMicro(), s.Policy.CleanupBatch)
		if err != nil {
			return err
		}
		type candidate struct {
			ref     string
			version int64
		}
		list := []candidate{}
		for rows.Next() {
			var c candidate
			if err = rows.Scan(&c.ref, &c.version); err != nil {
				rows.Close()
				return err
			}
			list = append(list, c)
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return err
		}
		for _, c := range list {
			report.Artifacts = append(report.Artifacts, c.ref)
			if !dry {
				if err = s.tombstone(ctx, tx, c.ref, c.version); err != nil {
					return err
				}
			}
		}
		// Physical unlink happens only after committing tombstones. Recovery is
		// idempotent if the process stops between these two stages.
		if dry {
			report.Objects, err = s.orphans(ctx, tx, true)
		}
		return err
	})
	if err != nil || dry {
		return report, err
	}
	recovered, err := s.Recover(ctx)
	report.Objects = recovered.Objects
	return report, err
}

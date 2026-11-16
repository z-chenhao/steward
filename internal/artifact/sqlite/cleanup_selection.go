package sqlite

import (
	"context"
	"database/sql"
	"time"

	"github.com/z-chenhao/assistant/internal/artifact"
)

type CleanupCandidate struct {
	Ref                string
	Version, ExpiresAt int64
}

// Eligibility is authority, not ranking. The candidate receives only expiry
// ordinals after these original ownership/reference/Task checks have passed.
const cleanupEligibility = ` FROM artifact a WHERE owner_user_id=? AND owner_agent_id=? AND deleted_at IS NULL AND expires_at<=? AND NOT EXISTS(SELECT 1 FROM artifact_reference r WHERE r.artifact_ref=a.artifact_ref) AND NOT EXISTS(SELECT 1 FROM task t WHERE t.task_id=a.task_id AND (t.status IN ('active','paused') OR coalesce(t.completed_at,t.cancelled_at,t.created_at)+a.expires_at-a.created_at>?))`

func (s *Store) CleanupCandidatesTx(ctx context.Context, tx *sql.Tx, scope artifact.Scope) ([]CleanupCandidate, error) {
	if !scope.Owner {
		return nil, artifact.ErrNotFound
	}
	if err := s.scope(ctx, tx, scope); err != nil {
		return nil, err
	}
	now := time.Now().UnixMicro()
	rows, err := tx.QueryContext(ctx, `SELECT artifact_ref,version,expires_at`+cleanupEligibility+` ORDER BY artifact_ref`, scope.UserID, scope.AgentID, now, now)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	values := []CleanupCandidate{}
	for rows.Next() {
		var value CleanupCandidate
		if err := rows.Scan(&value.Ref, &value.Version, &value.ExpiresAt); err != nil {
			return nil, err
		}
		values = append(values, value)
	}
	return values, rows.Err()
}

// ApplyCleanup validates the entire selected set before tombstoning anything.
// Pins/expiry/versions can change while policy runs; a stale set is rejected as
// a whole. Physical unlink still follows the committed tombstones via Recover.
func (s *Store) ApplyCleanup(ctx context.Context, scope artifact.Scope, selected []CleanupCandidate, dry bool, fence func(context.Context, *sql.Tx) error) (artifact.CleanupReport, error) {
	report := artifact.CleanupReport{DryRun: dry, Artifacts: []string{}}
	if len(selected) > 10000 || fence == nil {
		return report, artifact.ErrInvalid
	}
	err := s.transaction(ctx, func(tx *sql.Tx) error {
		if !scope.Owner {
			return artifact.ErrNotFound
		}
		if err := s.scope(ctx, tx, scope); err != nil {
			return err
		}
		if err := fence(ctx, tx); err != nil {
			return err
		}
		now := time.Now().UnixMicro()
		seen := map[string]bool{}
		for _, value := range selected {
			if seen[value.Ref] {
				return artifact.ErrInvalid
			}
			seen[value.Ref] = true
			var eligible bool
			if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1`+cleanupEligibility+` AND artifact_ref=? AND version=? AND expires_at=?)`, scope.UserID, scope.AgentID, now, now, value.Ref, value.Version, value.ExpiresAt).Scan(&eligible); err != nil {
				return err
			}
			if !eligible {
				return artifact.ErrConflict
			}
		}
		for _, value := range selected {
			report.Artifacts = append(report.Artifacts, value.Ref)
			if !dry {
				if err := s.tombstone(ctx, tx, value.Ref, value.Version); err != nil {
					return err
				}
			}
		}
		if dry {
			var err error
			report.Objects, err = s.orphans(ctx, tx, true)
			return err
		}
		return nil
	})
	if err != nil || dry {
		return report, err
	}
	recovered, err := s.Recover(ctx)
	report.Objects = recovered.Objects
	return report, err
}

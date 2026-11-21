package sqlite

import (
	"context"
	"database/sql"
	"time"

	"github.com/z-chenhao/assistant/internal/artifact"
)

type SearchCandidate struct {
	Ref, ContentHash, Name, MediaType string
	Version, CreatedAt                int64
}

// VisitSearchCandidatesTx streams authorized, live metadata in canonical
// reference order. Reopening selected references still requires DescribeTx.
func (s *Store) VisitSearchCandidatesTx(ctx context.Context, tx *sql.Tx, scope artifact.Scope, visit func(SearchCandidate)) error {
	if err := s.scope(ctx, tx, scope); err != nil {
		return err
	}
	now := time.Now().UnixMicro()
	rows, err := tx.QueryContext(ctx, `SELECT a.artifact_ref,a.content_hash,a.version,a.created_at,a.name,a.media_type
	 FROM artifact a WHERE a.owner_user_id=? AND a.owner_agent_id=? AND a.deleted_at IS NULL
	 AND (? OR (a.visibility='task' AND a.task_id=? AND a.task_id!='') OR (a.visibility='run' AND a.run_id=?)
	  OR EXISTS(SELECT 1 FROM artifact_reference r WHERE r.artifact_ref=a.artifact_ref AND
	      ((r.holder_kind='task' AND r.holder_id=?) OR (r.holder_kind='run' AND r.holder_id=?))))
	 AND (a.expires_at>? OR EXISTS(
	    SELECT 1 FROM task t WHERE t.task_id IN (SELECT a.task_id UNION ALL SELECT holder_id FROM artifact_reference
	      WHERE artifact_ref=a.artifact_ref AND holder_kind='task')
	    AND (t.status IN ('active','paused') OR coalesce(t.completed_at,t.cancelled_at,t.created_at)+a.expires_at-a.created_at>?)))
	 ORDER BY a.artifact_ref`, scope.UserID, scope.AgentID, scope.Owner, scope.TaskID, scope.RunID, scope.TaskID, scope.RunID, now, now)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var c SearchCandidate
		if err := rows.Scan(&c.Ref, &c.ContentHash, &c.Version, &c.CreatedAt, &c.Name, &c.MediaType); err != nil {
			return err
		}
		if validRef(c.Ref) {
			visit(c)
		}
	}
	return rows.Err()
}

package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"strings"

	"github.com/z-chenhao/assistant/internal/artifact"
)

// Historical consumer: Artifact searches in Runs whose immutable service list
// predates steward.artifact-selection/1, including their inherited descendants.
// This is not a failure fallback. Remove after those retained executions are
// explicitly retired; do not reinterpret their frozen tool behavior.
// SearchLegacyTx uses the same Unicode lowercasing for query and metadata.
// Matching and visibility checks both precede the accessible-result bound.
func (s *Store) SearchLegacyTx(ctx context.Context, tx *sql.Tx, a artifact.Scope, query string, limit int) ([]artifact.Descriptor, error) {
	if limit == 0 {
		limit = 50
	}
	if limit < 1 || limit > 50 || len(query) > 4096 {
		return nil, artifact.ErrInvalid
	}
	query = strings.ToLower(strings.TrimSpace(query))
	if err := s.scope(ctx, tx, a); err != nil {
		return nil, err
	}
	rows, err := tx.QueryContext(ctx, `SELECT artifact_ref,name,media_type FROM artifact WHERE owner_user_id=? AND owner_agent_id=? AND deleted_at IS NULL ORDER BY created_at DESC,artifact_ref`, a.UserID, a.AgentID)
	if err != nil {
		return nil, err
	}
	refs := []string{}
	for rows.Next() {
		var ref, name, mediaType string
		if err = rows.Scan(&ref, &name, &mediaType); err != nil {
			rows.Close()
			return nil, err
		}
		// SQLite lower() only handles ASCII; use Go for both operands.
		if query == "" || strings.Contains(strings.ToLower(name), query) || strings.Contains(strings.ToLower(mediaType), query) {
			refs = append(refs, ref)
		}
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}
	out := []artifact.Descriptor{}
	for _, ref := range refs {
		d, e := s.DescribeTx(ctx, tx, a, ref, true)
		if errors.Is(e, artifact.ErrNotFound) {
			continue
		}
		if e != nil {
			return nil, e
		}
		out = append(out, d)
		if len(out) == limit {
			break
		}
	}
	return out, nil
}

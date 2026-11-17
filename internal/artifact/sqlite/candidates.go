package sqlite

import (
	"context"
	"database/sql"
	"errors"

	"github.com/z-chenhao/assistant/internal/artifact"
)

// CandidatesTx gathers immutable metadata only after the same live visibility,
// grant, expiry and Task-protection checks as opening the original reference.
// Reference order is a canonical source identity, not business ranking. Neither
// matching nor result-window policy runs in this authority adapter.
func (s *Store) CandidatesTx(ctx context.Context, tx *sql.Tx, scope artifact.Scope) ([]artifact.Descriptor, error) {
	if err := s.scope(ctx, tx, scope); err != nil {
		return nil, err
	}
	rows, err := tx.QueryContext(ctx, `SELECT artifact_ref FROM artifact WHERE owner_user_id=? AND owner_agent_id=? AND deleted_at IS NULL ORDER BY artifact_ref`, scope.UserID, scope.AgentID)
	if err != nil {
		return nil, err
	}
	var refs []string
	for rows.Next() {
		var ref string
		if err := rows.Scan(&ref); err != nil {
			rows.Close()
			return nil, err
		}
		refs = append(refs, ref)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}
	values := []artifact.Descriptor{}
	for _, ref := range refs {
		value, err := s.DescribeTx(ctx, tx, scope, ref, true)
		if errors.Is(err, artifact.ErrNotFound) {
			continue
		}
		if err != nil {
			return nil, err
		}
		values = append(values, value)
	}
	return values, nil
}

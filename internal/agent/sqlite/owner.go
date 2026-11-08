package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"unicode"

	"github.com/z-chenhao/assistant/internal/artifact"
)

// OwnerID is Host deployment identity, never model arguments or an HTTP header.
// A runtime is single-owner; shared multi-tenant routing belongs above it.
func (s *Store) OwnerID() string {
	if owner := s.runtimeOwner.Load(); owner != nil {
		return *owner
	}
	return artifact.LocalUser
}

func (s *Store) BindOwner(ctx context.Context, owner string) error {
	if owner == "" {
		owner = artifact.LocalUser
	}
	if len(owner) > 128 || strings.TrimSpace(owner) != owner || strings.IndexFunc(owner, unicode.IsControl) >= 0 {
		return errors.New("invalid runtime owner")
	}
	err := s.Transaction(ctx, func(tx *sql.Tx) error {
		var current string
		e := tx.QueryRowContext(ctx, `SELECT user_id FROM runtime_owner WHERE singleton=1`).Scan(&current)
		if e == nil {
			if current != owner {
				return errors.New("runtime owner mismatch")
			}
			return nil
		}
		if e != sql.ErrNoRows {
			return e
		}
		// Also protect roots written by an older/local composition after migration.
		var occupied bool
		if e = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM run) OR EXISTS(SELECT 1 FROM conversation_intent) OR EXISTS(SELECT 1 FROM auth_connection) OR EXISTS(SELECT 1 FROM model_profile) OR EXISTS(SELECT 1 FROM memory_mutation)`).Scan(&occupied); e != nil {
			return e
		}
		if occupied && owner != artifact.LocalUser {
			return errors.New("existing local runtime cannot be reassigned")
		}
		_, e = tx.ExecContext(ctx, `INSERT INTO runtime_owner VALUES(1,?)`, owner)
		return e
	})
	if err == nil {
		s.runtimeOwner.Store(&owner)
	}
	return err
}

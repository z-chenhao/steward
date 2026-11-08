package sqlite

import (
	"context"
	"encoding/json"
	"strings"
	"time"

	"github.com/z-chenhao/assistant/internal/memory"
	"github.com/z-chenhao/assistant/internal/recall"
)

// AgeSemantic demotes only automatically derived Core facts without confirmation
// for a year. Explicit memories are outside this policy. Immutable successors
// preserve the original observation time and provenance; decay is not evidence.
func (s *Store) AgeSemantic(ctx context.Context, now time.Time) error {
	rows, err := s.db.QueryContext(ctx, `SELECT m.memory_id,m.user_id,m.scope_type,m.scope_id,m.content,m.source_refs,m.created_at,x.facet_key,x.metadata,x.confirmed_at FROM memory_record m JOIN memory_semantic_record x USING(memory_id) WHERE (m.tier='core' OR x.expires_at=0) AND x.confirmed_at<=? AND m.deleted_at IS NULL AND NOT EXISTS(SELECT 1 FROM memory_record n WHERE n.supersedes_memory_id=m.memory_id) AND NOT EXISTS(SELECT 1 FROM memory_retirement r WHERE r.memory_id=m.memory_id) LIMIT 32`, now.Add(-365*24*time.Hour).UnixMicro())
	if err != nil {
		return recall.ErrUnavailable
	}
	type aging struct {
		record    memory.Record
		key, meta string
		confirmed int64
	}
	items := []aging{}
	for rows.Next() {
		var a aging
		var refs string
		var at int64
		if rows.Scan(&a.record.ID, &a.record.UserID, &a.record.ScopeType, &a.record.ScopeID, &a.record.Content, &refs, &at, &a.key, &a.meta, &a.confirmed) != nil || json.Unmarshal([]byte(refs), &a.record.SourceRefs) != nil {
			rows.Close()
			return recall.ErrUnavailable
		}
		a.record.CreatedAt = time.UnixMicro(at).UTC()
		items = append(items, a)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return recall.ErrUnavailable
	}
	for _, a := range items {
		old := a.record.ID
		if strings.HasPrefix(a.record.Content, "sealed:") {
			a.record.Content, err = s.vault.Open(ctx, a.record.Content, binding(a.record))
			if err != nil {
				return err
			}
		}
		metadata, e := s.vault.Open(ctx, a.meta, "semantic:"+old)
		if e != nil {
			return e
		}
		a.record.ID = "decay:" + old
		a.record.Supersedes = old
		a.record.Tier = "recall"
		sealed, e := s.vault.Seal(ctx, metadata, "semantic:"+a.record.ID)
		if e != nil {
			return e
		}
		tx, e := s.db.BeginTx(ctx, nil)
		if e != nil {
			return recall.ErrUnavailable
		}
		e = s.apply(ctx, tx, a.record)
		if e == nil {
			_, e = tx.ExecContext(ctx, `INSERT INTO memory_semantic_record VALUES(?,?,?,?,?)`, a.record.ID, a.key, sealed, a.confirmed, time.UnixMicro(a.confirmed).Add(545*24*time.Hour).UnixMicro())
		}
		if e == nil {
			_, e = tx.ExecContext(ctx, `UPDATE memory_fact SET memory_id=? WHERE memory_id=?`, a.record.ID, old)
		}
		if e == nil {
			e = tx.Commit()
		} else {
			tx.Rollback()
		}
		if e != nil {
			return e
		}
	}
	return nil
}

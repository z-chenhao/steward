package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"

	"github.com/z-chenhao/assistant/internal/recall"
)

// A document edit changes representation, not the identity of an existing fact.
// Its facet and confirmation lifetime remain Host-owned and are not promoted by
// Dream or by the model's choice of title/content.
func (s *Store) inheritSemanticDocument(ctx context.Context, tx *sql.Tx, old, id string) error {
	var key, sealed string
	var confirmed, expires int64
	err := tx.QueryRowContext(ctx, `SELECT facet_key,metadata,confirmed_at,expires_at FROM memory_semantic_record WHERE memory_id=?`, old).Scan(&key, &sealed, &confirmed, &expires)
	if err == sql.ErrNoRows {
		return nil
	}
	if err != nil {
		return recall.ErrUnavailable
	}
	plain, err := s.vault.Open(ctx, sealed, "semantic:"+old)
	if err != nil {
		return err
	}
	var meta semanticMetadata
	if json.Unmarshal([]byte(plain), &meta) != nil {
		return recall.ErrUnavailable
	}
	encrypted, err := s.vault.Seal(ctx, plain, "semantic:"+id)
	if err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `INSERT OR IGNORE INTO memory_semantic_record VALUES(?,?,?,?,?)`, id, key, encrypted, confirmed, expires); err != nil {
		return recall.ErrUnavailable
	}
	_, err = tx.ExecContext(ctx, `UPDATE memory_fact SET memory_id=? WHERE fact_key=? AND memory_id IN (WITH RECURSIVE ancestors(id) AS (SELECT ? UNION SELECT r.supersedes_memory_id FROM memory_record r JOIN ancestors a ON r.memory_id=a.id WHERE r.supersedes_memory_id IS NOT NULL) SELECT id FROM ancestors) AND NOT EXISTS(SELECT 1 FROM memory_record WHERE supersedes_memory_id=?)`, id, key, old, id)
	return err
}

// Repair pre-fix document successors using the original immutable lineage.
// This is idempotent, runs when the Vault becomes available, and does not
// rewrite old document bodies, requests, quotes or accepted receipts.
func (s *Store) repairSemanticDocuments(ctx context.Context) error {
	if s.vault.Ref == "" {
		return nil
	}
	rows, err := s.db.QueryContext(ctx, `WITH RECURSIVE ancestors(leaf,id,depth) AS (
 SELECT d.memory_id,r.supersedes_memory_id,1 FROM memory_document d JOIN memory_record r USING(memory_id) WHERE r.supersedes_memory_id IS NOT NULL AND NOT EXISTS(SELECT 1 FROM memory_semantic_record x WHERE x.memory_id=d.memory_id)
 UNION ALL SELECT a.leaf,r.supersedes_memory_id,a.depth+1 FROM ancestors a JOIN memory_record r ON r.memory_id=a.id WHERE r.supersedes_memory_id IS NOT NULL)
 SELECT a.leaf,a.id FROM ancestors a JOIN memory_semantic_record x ON x.memory_id=a.id ORDER BY a.leaf,a.depth`)
	if err != nil {
		return err
	}
	type pair struct{ leaf, old string }
	all := []pair{}
	seen := map[string]bool{}
	for rows.Next() {
		var p pair
		if err = rows.Scan(&p.leaf, &p.old); err != nil {
			break
		}
		if !seen[p.leaf] {
			seen[p.leaf] = true
			all = append(all, p)
		}
	}
	if err == nil {
		err = rows.Err()
	}
	rows.Close()
	if err != nil {
		return err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for _, p := range all {
		if err = s.inheritSemanticDocument(ctx, tx, p.old, p.leaf); err != nil {
			return err
		}
	}
	// A separate newer canonical fact may already have been accepted by the old
	// buggy pipeline. Retire only the stale derived branch, retaining its audit.
	_, err = tx.ExecContext(ctx, `INSERT OR IGNORE INTO memory_retirement
 SELECT m.memory_id,'semantic_lineage_repaired',unixepoch('subsec')*1000000 FROM memory_record m JOIN memory_document d USING(memory_id) JOIN memory_semantic_record x USING(memory_id)
 JOIN memory_fact f ON f.user_id=m.user_id AND f.agent_id=m.scope_id AND f.fact_key=x.facet_key JOIN memory_semantic_record current ON current.memory_id=f.memory_id
 WHERE f.memory_id<>m.memory_id AND current.confirmed_at>=x.confirmed_at AND m.deleted_at IS NULL AND NOT EXISTS(SELECT 1 FROM memory_record n WHERE n.supersedes_memory_id=m.memory_id)`)
	if err != nil {
		return err
	}
	return tx.Commit()
}

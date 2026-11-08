package sqlite

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math"
	"strings"
	"time"

	"github.com/z-chenhao/assistant/internal/memory"
	"github.com/z-chenhao/assistant/internal/recall"
	"github.com/z-chenhao/assistant/internal/recall/sqliteindex"
)

const maintenanceSchema = `
CREATE TABLE memory_receipt(event_id TEXT PRIMARY KEY,request_hash TEXT NOT NULL,applied_at INTEGER NOT NULL) STRICT;
CREATE TRIGGER memory_receipt_immutable BEFORE UPDATE ON memory_receipt BEGIN SELECT RAISE(ABORT,'immutable curator receipt'); END;
CREATE TRIGGER memory_receipt_audit BEFORE DELETE ON memory_receipt BEGIN SELECT RAISE(ABORT,'retain curator receipt'); END;
CREATE TRIGGER memory_receipt_no_replace BEFORE INSERT ON memory_receipt WHEN EXISTS(SELECT 1 FROM memory_receipt WHERE event_id=NEW.event_id) BEGIN SELECT RAISE(ABORT,'curator receipt exists'); END;
CREATE TABLE memory_evidence(user_id TEXT NOT NULL,agent_id TEXT NOT NULL,fact_key TEXT NOT NULL,source_type TEXT NOT NULL,source_ref TEXT NOT NULL,PRIMARY KEY(user_id,agent_id,fact_key,source_type,source_ref)) STRICT;
CREATE TABLE memory_fact(user_id TEXT NOT NULL,agent_id TEXT NOT NULL,fact_key TEXT NOT NULL,memory_id TEXT NOT NULL REFERENCES memory_record(memory_id),PRIMARY KEY(user_id,agent_id,fact_key)) STRICT;
CREATE TABLE memory_suppression(source_type TEXT NOT NULL,source_ref TEXT NOT NULL,PRIMARY KEY(source_type,source_ref)) STRICT;
CREATE TABLE memory_protection(singleton INTEGER PRIMARY KEY CHECK(singleton=1),cleanup_pending INTEGER NOT NULL CHECK(cleanup_pending IN(0,1))) STRICT;
CREATE TABLE memory_category_event(event_id TEXT PRIMARY KEY,user_id TEXT NOT NULL,agent_id TEXT NOT NULL,category TEXT NOT NULL,accepted_at INTEGER NOT NULL,targets_json TEXT NOT NULL CHECK(json_valid(targets_json))) STRICT;
CREATE TABLE memory_category_allow(event_id TEXT PRIMARY KEY,user_id TEXT NOT NULL,agent_id TEXT NOT NULL,category TEXT NOT NULL,accepted_at INTEGER NOT NULL) STRICT;
CREATE TRIGGER category_targets_immutable BEFORE UPDATE ON memory_category_event BEGIN SELECT RAISE(ABORT,'immutable category targets'); END;
CREATE TRIGGER category_targets_audit BEFORE DELETE ON memory_category_event BEGIN SELECT RAISE(ABORT,'retain category targets'); END;
CREATE TRIGGER category_allow_immutable BEFORE UPDATE ON memory_category_allow BEGIN SELECT RAISE(ABORT,'immutable category receipt'); END;
CREATE TRIGGER category_allow_audit BEFORE DELETE ON memory_category_allow BEGIN SELECT RAISE(ABORT,'retain category receipt'); END;
CREATE TABLE memory_policy(user_id TEXT NOT NULL,agent_id TEXT NOT NULL,category TEXT NOT NULL,blocked INTEGER NOT NULL CHECK(blocked IN(0,1)),created_at INTEGER NOT NULL,updated_at INTEGER NOT NULL,version INTEGER NOT NULL DEFAULT 1 CHECK(version>=1),PRIMARY KEY(user_id,agent_id,category)) STRICT;
CREATE TRIGGER memory_policy_cas BEFORE UPDATE ON memory_policy WHEN NEW.user_id!=OLD.user_id OR NEW.agent_id!=OLD.agent_id OR NEW.category!=OLD.category OR NEW.created_at!=OLD.created_at OR NEW.version!=OLD.version+1 BEGIN SELECT RAISE(ABORT,'invalid memory policy transition'); END;
INSERT INTO memory_protection VALUES(1,0);
INSERT OR IGNORE INTO memory_suppression
SELECT json_extract(j.value,'$.source_type'),json_extract(j.value,'$.source_ref') FROM memory_record m,json_each(m.source_refs) j WHERE m.memory_id IN
(WITH RECURSIVE lineage(id) AS (SELECT memory_id FROM memory_record WHERE deleted_at IS NOT NULL UNION SELECT m.memory_id FROM memory_record m JOIN lineage l ON m.supersedes_memory_id=l.id UNION SELECT m.supersedes_memory_id FROM memory_record m JOIN lineage l ON m.memory_id=l.id WHERE m.supersedes_memory_id IS NOT NULL) SELECT id FROM lineage);
WITH RECURSIVE lineage(id,at) AS (SELECT memory_id,deleted_at FROM memory_record WHERE deleted_at IS NOT NULL UNION SELECT m.memory_id,l.at FROM memory_record m JOIN lineage l ON m.supersedes_memory_id=l.id UNION SELECT m.supersedes_memory_id,l.at FROM memory_record m JOIN lineage l ON m.memory_id=l.id WHERE m.supersedes_memory_id IS NOT NULL)
UPDATE memory_record SET deleted_at=(SELECT max(at) FROM lineage WHERE id=memory_id),version=version+1 WHERE deleted_at IS NULL AND memory_id IN(SELECT id FROM lineage);
`

func (s *Store) ConfigureVault(v memory.Vault) error {
	s.clearWorking()
	s.vault = v
	if err := s.protectExisting(context.Background()); err != nil {
		return err
	}
	return s.repairSemanticDocuments(context.Background())
}
func (s *Store) CheckRef(ctx context.Context, scope recall.Scope, id string, deleted bool) error {
	var n int
	e := s.db.QueryRowContext(ctx, `SELECT count(*) FROM memory_record WHERE memory_id=? AND user_id=? AND ((scope_type='user' AND scope_id=?) OR (scope_type='agent' AND scope_id=?)) AND (? OR deleted_at IS NULL)`, id, scope.UserID, scope.UserID, scope.AgentID, deleted).Scan(&n)
	if e != nil {
		return recall.ErrUnavailable
	}
	if n != 1 {
		return recall.ErrConflict
	}
	return nil
}
func binding(r memory.Record) string {
	return r.ID + "\x00" + r.UserID + "\x00" + r.ScopeType + "\x00" + r.ScopeID
}

// Curate has one transactional serialization point, including evidence counts,
// deduplication, promotion and receipt. Event IDs and payload hashes are frozen.
func (s *Store) Curate(ctx context.Context, eventID string, scope recall.Scope, ref memory.SourceRef, at time.Time, content []string, explicit bool, supersedes string) error {
	raw, _ := json.Marshal(struct {
		Scope      recall.Scope
		Ref        memory.SourceRef
		Content    []string
		Explicit   bool
		Supersedes string
		At         int64
	}{scope, ref, content, explicit, supersedes, at.UnixMicro()})
	sum := sha256.Sum256(raw)
	hash := hex.EncodeToString(sum[:])
	for _, text := range content {
		if s.vault.Ref != "" || memory.Sensitive(text) {
			var e error
			hash, e = s.vault.Digest(ctx, string(raw))
			if e != nil {
				return e
			}
			break
		}
	}
	tx, e := s.db.BeginTx(ctx, nil)
	if e != nil {
		return recall.ErrUnavailable
	}
	defer tx.Rollback()
	var prior string
	e = tx.QueryRowContext(ctx, `SELECT request_hash FROM memory_receipt WHERE event_id=?`, eventID).Scan(&prior)
	if e == nil {
		if prior != hash {
			return recall.ErrConflict
		}
		return nil
	}
	if e != sql.ErrNoRows {
		return recall.ErrUnavailable
	}
	var suppressed int
	if tx.QueryRowContext(ctx, `SELECT count(*) FROM memory_suppression WHERE source_type=? AND source_ref=?`, ref.Type, ref.Ref).Scan(&suppressed) != nil {
		return recall.ErrUnavailable
	}
	if suppressed == 0 {
		for i, text := range content {
			if memory.HasSecret(text) {
				continue
			}
			if !explicit {
				var blocked int
				if tx.QueryRowContext(ctx, `SELECT coalesce((SELECT blocked FROM memory_policy WHERE user_id=?1 AND agent_id=?2 AND category=?3),(SELECT blocked FROM memory_policy WHERE user_id=?1 AND agent_id=?2 AND category='all'),0)`, scope.UserID, scope.AgentID, memory.Category(text)).Scan(&blocked) != nil {
					return recall.ErrUnavailable
				}
				if blocked > 0 {
					continue
				}
			}
			key := memory.Fingerprint(text)
			if s.vault.Ref != "" || memory.Sensitive(text) {
				key, e = s.vault.Digest(ctx, text)
				if e != nil {
					return e
				}
			}
			evidence, e := tx.ExecContext(ctx, `INSERT OR IGNORE INTO memory_evidence VALUES(?,?,?,?,?)`, scope.UserID, scope.AgentID, key, ref.Type, ref.Ref)
			if e != nil {
				return recall.ErrUnavailable
			}
			fresh, _ := evidence.RowsAffected()
			var count int
			if tx.QueryRowContext(ctx, `SELECT count(*) FROM memory_evidence WHERE user_id=? AND agent_id=? AND fact_key=? AND source_type IN('conversation','feedback')`, scope.UserID, scope.AgentID, key).Scan(&count) != nil {
				return recall.ErrUnavailable
			}
			var old string
			e = tx.QueryRowContext(ctx, `SELECT f.memory_id FROM memory_fact f JOIN memory_record m USING(memory_id) WHERE f.user_id=? AND f.agent_id=? AND f.fact_key=? AND m.deleted_at IS NULL`, scope.UserID, scope.AgentID, key).Scan(&old)
			if e != nil && e != sql.ErrNoRows {
				return recall.ErrUnavailable
			}
			tier := "recall"
			if memory.Stable(text) && (explicit || count >= 3) {
				tier = "core"
			}
			if old != "" && supersedes == "" {
				var oldTier string
				var oldAt int64
				if tx.QueryRowContext(ctx, `SELECT tier,created_at FROM memory_record WHERE memory_id=?`, old).Scan(&oldTier, &oldAt) != nil {
					return recall.ErrUnavailable
				}
				// Explicit independent evidence owns its new timestamp/source even
				// when the content deduplicates. Preserve the old immutable record;
				// the fresh leaf can cross a later privacy floor without reviving it.
				renew := explicit && fresh > 0 && at.UnixMicro() > oldAt
				if (oldTier == tier || oldTier == "core") && !renew {
					continue
				}
				if oldTier == "core" {
					tier = "core"
				}
			}
			if supersedes != "" {
				old = strings.TrimPrefix(supersedes, "memory:")
			}
			r := memory.Record{ID: fmt.Sprintf("%s-%d", eventID, i), UserID: scope.UserID, ScopeType: "agent", ScopeID: scope.AgentID, Tier: tier, Content: text, SourceRefs: []memory.SourceRef{ref}, Supersedes: old, CreatedAt: at}
			if e = s.apply(ctx, tx, r); e != nil {
				return e
			}
			if _, e = tx.ExecContext(ctx, `INSERT INTO memory_fact VALUES(?,?,?,?) ON CONFLICT(user_id,agent_id,fact_key) DO UPDATE SET memory_id=excluded.memory_id`, scope.UserID, scope.AgentID, key, r.ID); e != nil {
				return recall.ErrUnavailable
			}
		}
	}
	if _, e = tx.ExecContext(ctx, `INSERT INTO memory_receipt VALUES(?,?,?)`, eventID, hash, at.UnixMicro()); e != nil {
		return recall.ErrUnavailable
	}
	if tx.Commit() != nil {
		return recall.ErrUnavailable
	}
	return nil
}

func (s *Store) search(ctx context.Context, scope recall.Scope, q recall.Query) ([]recall.Hit, error) {
	if !q.Valid() || scope.UserID == "" || scope.AgentID == "" {
		return nil, recall.ErrInvalid
	}
	start, end := int64(math.MinInt64), int64(math.MaxInt64)
	if q.TimeRange != nil {
		if q.TimeRange.Start != nil {
			start = q.TimeRange.Start.UnixMicro()
		}
		if q.TimeRange.End != nil {
			end = q.TimeRange.End.UnixMicro()
		}
	}
	var sealed int
	if e := s.db.QueryRowContext(ctx, `SELECT count(*) FROM memory_record WHERE user_id=? AND ((scope_type='user' AND scope_id=?) OR (scope_type='agent' AND scope_id=?)) AND content LIKE 'sealed:v1:%' AND deleted_at IS NULL AND created_at>=? AND created_at<=?`, scope.UserID, scope.UserID, scope.AgentID, start, end).Scan(&sealed); e != nil {
		return nil, recall.ErrUnavailable
	}
	if sealed == 0 {
		if err := s.lockSearch(ctx); err != nil {
			return nil, err
		}
		if s.workingScope != scope {
			s.clearWorking()
		}
		s.unlockSearch()
		return s.visibleSearch(ctx, s.db, scope, q)
	}
	working, err := s.encryptedIndex(ctx, scope)
	if err != nil {
		return nil, err
	}
	return s.visibleSearch(ctx, working, scope, q)
}

func (s *Store) encryptedIndex(ctx context.Context, scope recall.Scope) (*sql.DB, error) {
	if err := s.lockSearch(ctx); err != nil {
		return nil, err
	}
	defer s.unlockSearch()
	local, ok := s.engine.(interface{ Local() bool })
	if !s.localSearch && (!ok || !local.Local()) {
		return nil, recall.ErrUnavailable
	}
	rows, e := s.db.QueryContext(ctx, `SELECT m.memory_id,m.user_id,m.scope_type,m.scope_id,m.content,m.created_at,m.tier`+currentMemoryRows+` ORDER BY m.memory_id`, scope.UserID, scope.UserID, scope.AgentID)
	if e != nil {
		return nil, recall.ErrUnavailable
	}
	var docs []sqliteindex.Document
	for rows.Next() {
		var r memory.Record
		var at int64
		if rows.Scan(&r.ID, &r.UserID, &r.ScopeType, &r.ScopeID, &r.Content, &at, &r.Tier) != nil {
			rows.Close()
			return nil, recall.ErrUnavailable
		}
		text := r.Content
		if strings.HasPrefix(text, "sealed:v1:") {
			text, e = s.vault.Open(ctx, text, binding(r))
			if e != nil {
				rows.Close()
				return nil, e
			}
		}
		agentID := ""
		if r.ScopeType == "agent" {
			agentID = r.ScopeID
		}
		docs = append(docs, sqliteindex.Document{Ref: "memory:" + r.ID, UserID: r.UserID, AgentID: agentID, Content: text, OccurredAt: at, Core: r.Tier == "core", Active: true})
	}
	e = rows.Err()
	rows.Close()
	if e != nil {
		return nil, recall.ErrUnavailable
	}
	if err := s.syncWorking(ctx, scope, docs); err != nil {
		return nil, err
	}
	if !s.localSearch {
		if err := sqliteindex.Bind(ctx, s.working, s.engine); err != nil {
			return nil, err
		}
	}
	return s.working, nil
}

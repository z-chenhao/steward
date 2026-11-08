package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/z-chenhao/assistant/internal/memory"
	"github.com/z-chenhao/assistant/internal/recall"
)

const semanticSchema = `
CREATE TABLE memory_semantic_record(memory_id TEXT PRIMARY KEY REFERENCES memory_record(memory_id),facet_key TEXT NOT NULL,metadata TEXT NOT NULL,confirmed_at INTEGER NOT NULL,expires_at INTEGER NOT NULL) STRICT;
CREATE TABLE memory_retirement(memory_id TEXT PRIMARY KEY REFERENCES memory_record(memory_id),reason TEXT NOT NULL,retired_at INTEGER NOT NULL) STRICT;
CREATE TABLE memory_semantic_event(event_id TEXT PRIMARY KEY,request_hash TEXT NOT NULL,completed_at INTEGER NOT NULL) STRICT;
CREATE TRIGGER semantic_event_immutable BEFORE UPDATE ON memory_semantic_event BEGIN SELECT RAISE(ABORT,'immutable semantic receipt'); END;
CREATE TRIGGER semantic_event_audit BEFORE DELETE ON memory_semantic_event BEGIN SELECT RAISE(ABORT,'retain semantic receipt'); END;
`

type semanticMetadata struct {
	Facet, Category string
	Tentative       bool
}

// RetireAutomatic quarantines only records made by the old automatic extractor.
// Originals, explicit remembers and audit records remain intact. Re-derivation
// uses the original event/source, never the unreviewed derived text as evidence.
func (s *Store) RetireAutomatic(ctx context.Context, eventIDs []string) error {
	if len(eventIDs) == 0 {
		return nil
	}
	tx, e := s.db.BeginTx(ctx, nil)
	if e != nil {
		return recall.ErrUnavailable
	}
	defer tx.Rollback()
	changed := false
	for _, id := range eventIDs {
		var result sql.Result
		if result, e = tx.ExecContext(ctx, `INSERT OR IGNORE INTO memory_retirement SELECT memory_id,'legacy_extraction',? FROM memory_record WHERE substr(memory_id,1,?)=?`, time.Now().UnixMicro(), len(id)+1, id+"-"); e != nil {
			return recall.ErrUnavailable
		}
		n, _ := result.RowsAffected()
		changed = changed || n > 0
	}
	if e = tx.Commit(); e != nil {
		return recall.ErrUnavailable
	}
	if changed {
		s.resetSemanticWorking(ctx)
	}
	return nil
}

func (s *Store) semanticContext(ctx context.Context, scope recall.Scope, floor int64, ids string) ([]memory.Fact, error) {
	rows, e := s.db.QueryContext(ctx, `SELECT m.memory_id,m.content,m.tier,m.scope_type,m.scope_id,m.created_at,coalesce((SELECT metadata FROM memory_semantic_record x WHERE x.memory_id=m.memory_id),'')`+currentMemoryRows+` AND m.created_at>? AND (NOT EXISTS(SELECT 1 FROM memory_document doc WHERE doc.memory_id=m.memory_id) OR EXISTS(SELECT 1 FROM memory_semantic_record semantic WHERE semantic.memory_id=m.memory_id)) AND (?='null' OR m.memory_id IN(SELECT value FROM json_each(?))) ORDER BY m.created_at DESC,m.memory_id LIMIT 129`, scope.UserID, scope.UserID, scope.AgentID, floor, ids, ids)
	if e != nil {
		return nil, recall.ErrUnavailable
	}
	type stored struct {
		record memory.Record
		meta   string
	}
	all := []stored{}
	for rows.Next() {
		var r stored
		var at int64
		r.record.UserID = scope.UserID
		r.record.ScopeType = "agent"
		r.record.ScopeID = scope.AgentID
		if rows.Scan(&r.record.ID, &r.record.Content, &r.record.Tier, &r.record.ScopeType, &r.record.ScopeID, &at, &r.meta) != nil {
			rows.Close()
			return nil, recall.ErrUnavailable
		}
		r.record.CreatedAt = time.UnixMicro(at).UTC()
		all = append(all, r)
	}
	e = rows.Err()
	rows.Close()
	if e != nil || len(all) > 128 {
		return nil, recall.ErrUnavailable
	}
	out := []memory.Fact{}
	size := 0
	for _, item := range all {
		r := item.record
		body := r.Content
		if strings.HasPrefix(body, "sealed:") {
			body, e = s.vault.Open(ctx, body, binding(r))
			if e != nil {
				return nil, recall.ErrUnavailable
			}
		}
		meta := semanticMetadata{Category: memory.Category(body)}
		if item.meta != "" {
			plain, err := s.vault.Open(ctx, item.meta, "semantic:"+r.ID)
			if err != nil || json.Unmarshal([]byte(plain), &meta) != nil {
				return nil, recall.ErrUnavailable
			}
		}
		size += len(body)
		if size > 64<<10 {
			return nil, recall.ErrUnavailable
		}
		out = append(out, memory.Fact{Ref: "memory:" + r.ID, Content: body, Facet: meta.Facet, Category: meta.Category, At: r.CreatedAt})
	}
	return out, nil
}

func (s *Store) MergeSemantic(ctx context.Context, scope recall.Scope, in memory.CurationInput, proposal memory.Proposal) error {
	if scope.UserID == "" || scope.AgentID == "" || in.Observation.EventID == "" || in.Observation.At.IsZero() || in.Observation.Source.Ref == "" {
		return recall.ErrInvalid
	}
	raw, _ := json.Marshal(proposal)
	checked, e := memory.ParseProposal(string(raw), in)
	if e != nil {
		return e
	}
	digestInput, _ := json.Marshal(struct {
		Input    memory.CurationInput
		Proposal memory.Proposal
	}{in, checked})
	hash := memory.Fingerprint(string(digestInput))
	if s.vault.Ref != "" {
		hash, e = s.vault.Digest(ctx, string(digestInput))
		if e != nil {
			return e
		}
	}
	tx, e := s.db.BeginTx(ctx, nil)
	if e != nil {
		return recall.ErrUnavailable
	}
	defer tx.Rollback()
	var prior string
	e = tx.QueryRowContext(ctx, `SELECT request_hash FROM memory_semantic_event WHERE event_id=?`, in.Observation.EventID).Scan(&prior)
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
	if tx.QueryRowContext(ctx, `SELECT count(*) FROM memory_suppression WHERE source_type=? AND source_ref=?`, in.Observation.Source.Type, in.Observation.Source.Ref).Scan(&suppressed) != nil {
		return recall.ErrUnavailable
	}
	existing := map[string]memory.Fact{}
	for _, f := range in.Existing {
		existing[f.Ref] = f
	}
	for i, c := range checked.Facts {
		if suppressed != 0 {
			continue
		}
		var blocked int
		if tx.QueryRowContext(ctx, `SELECT coalesce((SELECT blocked FROM memory_policy WHERE user_id=?1 AND agent_id=?2 AND category=?3),(SELECT blocked FROM memory_policy WHERE user_id=?1 AND agent_id=?2 AND category='all'),0)`, scope.UserID, scope.AgentID, c.Category).Scan(&blocked) != nil {
			return recall.ErrUnavailable
		}
		if blocked != 0 {
			continue
		}
		old := strings.TrimPrefix(c.Target, "memory:")
		if old != "" {
			var n int
			if tx.QueryRowContext(ctx, `SELECT count(*)`+currentMemoryRows+` AND m.memory_id=?`, scope.UserID, scope.UserID, scope.AgentID, old).Scan(&n) != nil {
				return recall.ErrUnavailable
			}
			if n != 1 {
				return recall.ErrConflict
			}
			if f := existing[c.Target]; f.Facet != "" && f.Facet != c.Facet {
				return memory.ErrProposal
			}
		}
		key := memory.Fingerprint(scope.UserID + ":" + scope.AgentID + ":" + c.Facet)
		if s.vault.Ref != "" {
			key, e = s.vault.Digest(ctx, "facet-v1:"+scope.UserID+":"+scope.AgentID+":"+c.Facet)
			if e != nil {
				return e
			}
		}
		var head string
		e = tx.QueryRowContext(ctx, `SELECT f.memory_id FROM memory_fact f JOIN memory_record m USING(memory_id) WHERE f.user_id=? AND f.agent_id=? AND f.fact_key=? AND m.deleted_at IS NULL AND NOT EXISTS(SELECT 1 FROM memory_retirement x WHERE x.memory_id=m.memory_id) AND NOT EXISTS(SELECT 1 FROM memory_semantic_record x WHERE x.memory_id=m.memory_id AND x.expires_at>0 AND x.expires_at<=unixepoch('subsec')*1000000) AND NOT EXISTS(SELECT 1 FROM memory_record n WHERE n.supersedes_memory_id=m.memory_id)`, scope.UserID, scope.AgentID, key).Scan(&head)
		if e != nil && e != sql.ErrNoRows {
			return recall.ErrUnavailable
		}
		if old != "" && head != "" && head != old {
			return recall.ErrConflict
		}
		if c.Operation == "add" && head != "" {
			return recall.ErrConflict
		}
		if c.Operation == "retract" {
			if _, e = tx.ExecContext(ctx, `INSERT INTO memory_retirement VALUES(?,'withdrawn',?)`, old, in.Observation.At.UnixMicro()); e != nil {
				return recall.ErrUnavailable
			}
			continue
		}
		content := c.Content
		if c.Operation == "support" {
			content = existing[c.Target].Content
		}
		if c.Operation == "add" && head == "" {
			// A renewed expired/withdrawn facet starts a new confidence period.
			// Original records and sealed event receipts retain past evidence.
			if _, e = tx.ExecContext(ctx, `DELETE FROM memory_evidence WHERE user_id=? AND agent_id=? AND fact_key=?`, scope.UserID, scope.AgentID, key); e != nil {
				return recall.ErrUnavailable
			}
		}
		var duplicate int
		if tx.QueryRowContext(ctx, `SELECT count(*) FROM memory_evidence WHERE user_id=? AND agent_id=? AND fact_key=? AND source_type=? AND source_ref=?`, scope.UserID, scope.AgentID, key, in.Observation.Source.Type, in.Observation.Source.Ref).Scan(&duplicate) != nil {
			return recall.ErrUnavailable
		}
		if c.Operation == "support" && duplicate > 0 {
			continue
		}
		if _, e = tx.ExecContext(ctx, `INSERT OR IGNORE INTO memory_evidence VALUES(?,?,?,?,?)`, scope.UserID, scope.AgentID, key, in.Observation.Source.Type, in.Observation.Source.Ref); e != nil {
			return recall.ErrUnavailable
		}
		var count int
		if tx.QueryRowContext(ctx, `SELECT count(*) FROM memory_evidence WHERE user_id=? AND agent_id=? AND fact_key=? AND source_type IN('conversation','feedback')`, scope.UserID, scope.AgentID, key).Scan(&count) != nil {
			return recall.ErrUnavailable
		}
		tier := "recall"
		if !c.Tentative && count >= 3 {
			tier = "core"
		}
		if old != "" {
			var priorTier string
			if tx.QueryRowContext(ctx, `SELECT tier FROM memory_record WHERE memory_id=?`, old).Scan(&priorTier) != nil {
				return recall.ErrUnavailable
			}
			if priorTier == "core" && !c.Tentative {
				tier = "core"
			}
		}
		r := memory.Record{ID: fmt.Sprintf("semantic:%s:%d", in.Observation.EventID, i), UserID: scope.UserID, ScopeType: "agent", ScopeID: scope.AgentID, Tier: tier, Content: content, SourceRefs: []memory.SourceRef{in.Observation.Source}, Supersedes: old, CreatedAt: in.Observation.At}
		if old != "" {
			var refs string
			if tx.QueryRowContext(ctx, `SELECT source_refs FROM memory_record WHERE memory_id=?`, old).Scan(&refs) != nil {
				return recall.ErrUnavailable
			}
			if json.Unmarshal([]byte(refs), &r.SourceRefs) != nil {
				return recall.ErrUnavailable
			}
			// Keep exact audit provenance without an unbounded prompt/index payload.
			seen := map[memory.SourceRef]bool{}
			allRefs := append(r.SourceRefs, in.Observation.Source)
			r.SourceRefs = nil
			for _, ref := range allRefs {
				if !seen[ref] {
					r.SourceRefs = append(r.SourceRefs, ref)
					seen[ref] = true
				}
			}
			if len(r.SourceRefs) > 64 {
				r.SourceRefs = r.SourceRefs[len(r.SourceRefs)-64:]
			}
		}
		if e = s.apply(ctx, tx, r); e != nil {
			return e
		}
		metadata, _ := json.Marshal(semanticMetadata{c.Facet, c.Category, c.Tentative})
		sealed, e := s.vault.Seal(ctx, string(metadata), "semantic:"+r.ID)
		if e != nil {
			return e
		}
		expires := int64(0)
		if tier == "recall" {
			age := 180 * 24 * time.Hour
			if c.Tentative {
				age = 14 * 24 * time.Hour
			}
			expires = r.CreatedAt.Add(age).UnixMicro()
		}
		if _, e = tx.ExecContext(ctx, `INSERT INTO memory_semantic_record VALUES(?,?,?,?,?)`, r.ID, key, sealed, r.CreatedAt.UnixMicro(), expires); e != nil {
			return recall.ErrUnavailable
		}
		if _, e = tx.ExecContext(ctx, `INSERT INTO memory_fact VALUES(?,?,?,?) ON CONFLICT(user_id,agent_id,fact_key) DO UPDATE SET memory_id=excluded.memory_id`, scope.UserID, scope.AgentID, key, r.ID); e != nil {
			return recall.ErrUnavailable
		}
	}
	if _, e = tx.ExecContext(ctx, `INSERT INTO memory_semantic_event VALUES(?,?,?)`, in.Observation.EventID, hash, time.Now().UnixMicro()); e != nil {
		return recall.ErrUnavailable
	}
	if tx.Commit() != nil {
		return recall.ErrUnavailable
	}
	return nil
}

func (s *Store) addProvenance(ctx context.Context, hits []recall.Hit) ([]recall.Hit, error) {
	for i := range hits {
		var raw string
		if s.db.QueryRowContext(ctx, `SELECT source_refs,coalesce((SELECT layer FROM memory_document x WHERE x.memory_id=memory_record.memory_id),'') FROM memory_record WHERE memory_id=?`, strings.TrimPrefix(hits[i].SourceRef, "memory:")).Scan(&raw, &hits[i].Layer) != nil {
			return nil, recall.ErrUnavailable
		}
		if hits[i].Layer != "" {
			hits[i].AuthorRole = "agent " + hits[i].Layer
		}
		if hits[i].Layer != "" {
			var sealed string
			if s.db.QueryRowContext(ctx, `SELECT metadata FROM memory_document WHERE memory_id=?`, strings.TrimPrefix(hits[i].SourceRef, "memory:")).Scan(&sealed) != nil {
				return nil, recall.ErrUnavailable
			}
			plain, e := s.vault.Open(ctx, sealed, "document:"+strings.TrimPrefix(hits[i].SourceRef, "memory:"))
			if e != nil {
				return nil, e
			}
			var meta documentMetadata
			if json.Unmarshal([]byte(plain), &meta) != nil {
				return nil, recall.ErrUnavailable
			}
			hits[i].Evidence = meta.Evidence
		}
		var refs []memory.SourceRef
		if json.Unmarshal([]byte(raw), &refs) != nil {
			return nil, recall.ErrUnavailable
		}
		for _, ref := range refs {
			hits[i].Sources = append(hits[i].Sources, ref.Ref)
		}
	}
	return hits, nil
}

// Legacy plaintext indexes can still contain retired audit records. Both local
// and vector paths recheck current membership before returning decrypted hits.
func (s *Store) visibleSearch(ctx context.Context, index *sql.DB, scope recall.Scope, q recall.Query) ([]recall.Hit, error) {
	count := q.Count()
	q.Limit = 50
	hits, e := s.searchIndex(ctx, index, scope, q)
	if e != nil {
		return nil, e
	}
	out := []recall.Hit{}
	for _, hit := range hits {
		var n int
		if s.db.QueryRowContext(ctx, `SELECT count(*)`+currentMemoryRows+` AND m.memory_id=?`, scope.UserID, scope.UserID, scope.AgentID, strings.TrimPrefix(hit.SourceRef, "memory:")).Scan(&n) != nil {
			return nil, recall.ErrUnavailable
		}
		if n == 1 {
			out = append(out, hit)
		}
	}
	if len(out) > count {
		out = out[:count]
	}
	return out, nil
}

func (s *Store) resetSemanticWorking(ctx context.Context) {
	if s.lockSearch(ctx) == nil {
		s.clearWorking()
		s.unlockSearch()
	}
}

func (s *Store) SemanticContext(ctx context.Context, scope recall.Scope, floor int64) ([]memory.Fact, error) {
	return s.semanticContext(ctx, scope, floor, "null")
}

// Large corpora select related candidates; they no longer stop all maintenance
// merely because the total fact count exceeds one model request's capacity.
func (s *Store) SemanticCandidates(ctx context.Context, scope recall.Scope, floor int64, text string) ([]memory.Fact, error) {
	var count int
	if err := s.db.QueryRowContext(ctx, `SELECT count(*)`+currentMemoryRows+` AND m.created_at>? AND (NOT EXISTS(SELECT 1 FROM memory_document doc WHERE doc.memory_id=m.memory_id) OR EXISTS(SELECT 1 FROM memory_semantic_record semantic WHERE semantic.memory_id=m.memory_id))`, scope.UserID, scope.UserID, scope.AgentID, floor).Scan(&count); err != nil {
		return nil, recall.ErrUnavailable
	}
	if count <= 128 {
		return s.SemanticContext(ctx, scope, floor)
	}
	if len(text) > 4000 {
		text = text[:4000]
		for !utf8.ValidString(text) {
			text = text[:len(text)-1]
		}
	}
	if strings.TrimSpace(text) == "" {
		return []memory.Fact{}, nil
	}
	hits, err := s.Search(ctx, scope, recall.Query{Query: text, Limit: 50})
	if err != nil {
		return nil, err
	}
	ids := []string{}
	for _, hit := range hits {
		if hit.Layer == "" || hit.Layer == "notes" {
			ids = append(ids, strings.TrimPrefix(hit.SourceRef, "memory:"))
		}
	}
	raw, _ := json.Marshal(ids)
	return s.semanticContext(ctx, scope, floor, string(raw))
}

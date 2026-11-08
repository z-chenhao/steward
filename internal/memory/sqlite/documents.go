package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/z-chenhao/assistant/internal/memory"
	"github.com/z-chenhao/assistant/internal/recall"
)

const documentSchema = `
CREATE TABLE memory_document(memory_id TEXT PRIMARY KEY REFERENCES memory_record(memory_id),layer TEXT NOT NULL CHECK(layer IN('notes','representations','injected')),metadata TEXT NOT NULL,revision INTEGER NOT NULL CHECK(revision>0)) STRICT;
CREATE TABLE memory_document_dependency(memory_id TEXT NOT NULL REFERENCES memory_record(memory_id),source_id TEXT NOT NULL REFERENCES memory_record(memory_id),PRIMARY KEY(memory_id,source_id)) STRICT;
CREATE TABLE memory_document_receipt(event_id TEXT PRIMARY KEY,request_hash TEXT NOT NULL,memory_id TEXT NOT NULL REFERENCES memory_record(memory_id)) STRICT;
CREATE TRIGGER memory_document_immutable BEFORE UPDATE ON memory_document BEGIN SELECT RAISE(ABORT,'immutable memory document'); END;
CREATE TRIGGER memory_document_audit BEFORE DELETE ON memory_document BEGIN SELECT RAISE(ABORT,'retain memory document'); END;
CREATE TRIGGER memory_document_receipt_immutable BEFORE UPDATE ON memory_document_receipt BEGIN SELECT RAISE(ABORT,'immutable memory document receipt'); END;
CREATE TRIGGER memory_document_receipt_audit BEFORE DELETE ON memory_document_receipt BEGIN SELECT RAISE(ABORT,'retain memory document receipt'); END;
`

type documentMetadata struct {
	NavigationTerms []string          `json:",omitempty"`
	Evidence        []recall.Evidence `json:",omitempty"`
	Origin          string
	Title           string
	Links           []string
}

func (s *Store) ListDocumentPage(ctx context.Context, scope recall.Scope, floor int64, layer string, limit int, cursor string) ([]memory.Document, string, error) {
	page, snapshot, err := s.Inspect(ctx, scope, floor, memory.InspectionQuery{Layer: layer, Limit: limit, Cursor: cursor})
	if err != nil {
		return nil, "", err
	}
	if err = s.RecheckInspection(ctx, snapshot); err != nil {
		return nil, "", err
	}
	out := []memory.Document{}
	for _, item := range page.Items {
		out = append(out, memory.Document{Ref: item.Ref, Layer: item.Layer, Title: item.Title, Revision: item.Revision, CreatedAt: item.CreatedAt})
	}
	return out, page.Next, nil
}

func (s *Store) ListDocuments(ctx context.Context, scope recall.Scope, floor int64, layer string, limit int) ([]memory.Document, error) {
	if layer != "" && !memory.DocumentLayer(layer) || limit < 1 || limit > 128 {
		return nil, recall.ErrInvalid
	}
	rows, err := s.db.QueryContext(ctx, `SELECT m.memory_id`+currentMemoryRows+` AND m.created_at>? AND (EXISTS(SELECT 1 FROM memory_document x WHERE x.memory_id=m.memory_id AND (?='' OR x.layer=?)) OR (? IN ('','notes') AND NOT EXISTS(SELECT 1 FROM memory_document x WHERE x.memory_id=m.memory_id))) ORDER BY m.created_at DESC,m.memory_id DESC LIMIT ?`, scope.UserID, scope.UserID, scope.AgentID, floor, layer, layer, layer, limit)
	if err != nil {
		return nil, recall.ErrUnavailable
	}
	ids := []string{}
	for rows.Next() {
		var id string
		if err = rows.Scan(&id); err != nil {
			break
		}
		ids = append(ids, id)
	}
	if err == nil {
		err = rows.Err()
	}
	rows.Close()
	if err != nil {
		return nil, recall.ErrUnavailable
	}
	out := []memory.Document{}
	for _, id := range ids {
		d, e := s.ReadDocument(ctx, scope, floor, "memory:"+id)
		if e != nil {
			return nil, e
		}
		out = append(out, d)
	}
	return out, nil
}

func (s *Store) ReadDocument(ctx context.Context, scope recall.Scope, floor int64, ref string) (memory.Document, error) {
	v, err := s.ReadEvidence(ctx, scope, strings.TrimPrefix(ref, "memory:"), floor, 0, s.vault)
	if err != nil {
		return memory.Document{}, err
	}
	var d memory.Document
	var sealed, refs string
	err = s.db.QueryRowContext(ctx, `SELECT x.layer,x.metadata,x.revision,m.source_refs FROM memory_document x JOIN memory_record m USING(memory_id) WHERE x.memory_id=?`, strings.TrimPrefix(ref, "memory:")).Scan(&d.Layer, &sealed, &d.Revision, &refs)
	if errors.Is(err, sql.ErrNoRows) { // Existing facts remain readable as L2 notes.
		d.Layer = memory.Notes
		d.Revision = 1
		err = s.db.QueryRowContext(ctx, `SELECT source_refs FROM memory_record WHERE memory_id=?`, strings.TrimPrefix(ref, "memory:")).Scan(&refs)
	} else if err == nil {
		plain, e := s.vault.Open(ctx, sealed, "document:"+strings.TrimPrefix(ref, "memory:"))
		if e != nil {
			return d, e
		}
		var meta documentMetadata
		if json.Unmarshal([]byte(plain), &meta) != nil {
			return d, recall.ErrUnavailable
		}
		d.Title, d.Links, d.Evidence = meta.Title, meta.Links, meta.Evidence
	}
	if err != nil || json.Unmarshal([]byte(refs), &d.Sources) != nil {
		return d, recall.ErrUnavailable
	}
	d.Ref, d.Content, d.CreatedAt = ref, v.Text, v.CreatedAt
	if d.Title == "" {
		d.Title = "Memory note"
	}
	if err = s.RecheckEvidence(ctx, v); err != nil {
		return memory.Document{}, err
	}
	return d, nil
}

// WriteDocument serializes publication and its receipt in the same transaction.
// Expected leaf identity plus revision is the CAS. Retry never creates a second
// revision; a different payload cannot reuse a receipt.
func (s *Store) WriteDocument(ctx context.Context, scope recall.Scope, floor int64, eventID, id string, at time.Time, sources []memory.SourceRef, w memory.DocumentWrite) (memory.Document, error) {
	if !w.Valid() || id == "" || eventID == "" || scope.UserID == "" || scope.AgentID == "" || at.UnixMicro() <= floor || len(sources) == 0 {
		return memory.Document{}, recall.ErrInvalid
	}
	raw, _ := json.Marshal(struct {
		Scope    recall.Scope
		Sources  []memory.SourceRef
		Write    memory.DocumentWrite
		Evidence []recall.Evidence `json:",omitempty"`
	}{scope, sources, w, w.Evidence})
	hash, err := s.vault.Digest(ctx, string(raw))
	if err != nil {
		return memory.Document{}, err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return memory.Document{}, recall.ErrUnavailable
	}
	defer tx.Rollback()
	revision := w.Revision + 1
	if w.Retire {
		revision = w.Revision
	}
	var prior, target string
	err = tx.QueryRowContext(ctx, `SELECT request_hash,memory_id FROM memory_document_receipt WHERE event_id=?`, eventID).Scan(&prior, &target)
	if err == nil {
		if prior != hash {
			return memory.Document{}, recall.ErrConflict
		}
		tx.Rollback()
		return memory.Document{Ref: "memory:" + target, Layer: w.Layer, Revision: revision}, nil
	}
	if err != sql.ErrNoRows {
		return memory.Document{}, recall.ErrUnavailable
	}
	for _, source := range sources {
		var blocked int
		if tx.QueryRowContext(ctx, `SELECT count(*) FROM memory_suppression WHERE source_type=? AND source_ref=?`, source.Type, source.Ref).Scan(&blocked) != nil {
			return memory.Document{}, recall.ErrUnavailable
		}
		if blocked > 0 {
			return memory.Document{}, recall.ErrConflict
		}
	}
	var blocked int
	if tx.QueryRowContext(ctx, `SELECT coalesce((SELECT blocked FROM memory_policy WHERE user_id=?1 AND agent_id=?2 AND category=?3),(SELECT blocked FROM memory_policy WHERE user_id=?1 AND agent_id=?2 AND category='all'),0)`, scope.UserID, scope.AgentID, memory.Category(w.Content)).Scan(&blocked) != nil {
		return memory.Document{}, recall.ErrUnavailable
	}
	if blocked > 0 {
		return memory.Document{}, recall.ErrConflict
	}
	if w.Ref != "" {
		var revision int64
		var layer string
		err = tx.QueryRowContext(ctx, `SELECT coalesce((SELECT revision FROM memory_document WHERE memory_id=m.memory_id),1),coalesce((SELECT layer FROM memory_document WHERE memory_id=m.memory_id),'notes')`+currentMemoryRows+` AND m.memory_id=? AND m.created_at>?`, scope.UserID, scope.UserID, scope.AgentID, strings.TrimPrefix(w.Ref, "memory:"), floor).Scan(&revision, &layer)
		if err == sql.ErrNoRows || err == nil && (revision != w.Revision || layer != w.Layer) {
			return memory.Document{}, recall.ErrConflict
		}
		if err != nil {
			return memory.Document{}, recall.ErrUnavailable
		}
	}
	if w.Layer == memory.Injected && w.Ref == "" {
		var n int
		err = tx.QueryRowContext(ctx, `SELECT count(*)`+currentMemoryRows+` AND m.created_at>? AND EXISTS(SELECT 1 FROM memory_document x WHERE x.memory_id=m.memory_id AND layer='injected')`, scope.UserID, scope.UserID, scope.AgentID, floor).Scan(&n)
		if err != nil {
			return memory.Document{}, recall.ErrUnavailable
		}
		if n != 0 {
			return memory.Document{}, recall.ErrConflict
		}
	}
	if w.Retire {
		id = strings.TrimPrefix(w.Ref, "memory:")
		if _, err = tx.ExecContext(ctx, `INSERT INTO memory_retirement VALUES(?,'document_retired',?)`, id, at.UnixMicro()); err != nil {
			return memory.Document{}, recall.ErrConflict
		}
		if _, err = tx.ExecContext(ctx, `UPDATE recall_document SET active=0 WHERE ref=?`, w.Ref); err != nil {
			return memory.Document{}, recall.ErrUnavailable
		}
	} else {
		for _, link := range w.Links {
			var n int
			if tx.QueryRowContext(ctx, `SELECT count(*)`+currentMemoryRows+` AND m.memory_id=? AND m.created_at>?`, scope.UserID, scope.UserID, scope.AgentID, strings.TrimPrefix(link, "memory:"), floor).Scan(&n) != nil || n != 1 {
				return memory.Document{}, recall.ErrConflict
			}
		}
		for _, link := range w.Links {
			var raw string
			if e := tx.QueryRowContext(ctx, `SELECT source_refs FROM memory_record WHERE memory_id=?`, strings.TrimPrefix(link, "memory:")).Scan(&raw); e != nil {
				return memory.Document{}, recall.ErrUnavailable
			}
			var inherited []memory.SourceRef
			if json.Unmarshal([]byte(raw), &inherited) != nil {
				return memory.Document{}, recall.ErrUnavailable
			}
			sources = append(sources, inherited...)
		}
		// Current support comes from this edit and its live links. Historical
		// support remains in immutable predecessors; Forget traverses that lineage.
		seen := map[string]bool{}
		unique := []memory.SourceRef{}
		for _, source := range sources {
			if !seen[source.Type+"\x00"+source.Ref] {
				seen[source.Type+"\x00"+source.Ref] = true
				unique = append(unique, source)
			}
		}
		if len(unique) > 256 {
			return memory.Document{}, recall.ErrInvalid
		}
		sources = unique
		r := memory.Record{ID: id, UserID: scope.UserID, ScopeType: "agent", ScopeID: scope.AgentID, Tier: "recall", Content: w.Content, SourceRefs: sources, Supersedes: strings.TrimPrefix(w.Ref, "memory:"), CreatedAt: at}
		if w.Ref != "" {
			var tier string
			e := tx.QueryRowContext(ctx, `SELECT tier FROM memory_record WHERE memory_id=? AND EXISTS(SELECT 1 FROM memory_semantic_record WHERE memory_id=?)`, strings.TrimPrefix(w.Ref, "memory:"), strings.TrimPrefix(w.Ref, "memory:")).Scan(&tier)
			if e == nil {
				r.Tier = tier
			} else if e != sql.ErrNoRows {
				return memory.Document{}, e
			}
		}
		if err = s.apply(ctx, tx, r); err != nil {
			return memory.Document{}, err
		}
		for _, link := range w.Links {
			target := strings.TrimPrefix(link, "memory:")
			if _, e := tx.ExecContext(ctx, `INSERT OR IGNORE INTO memory_document_dependency SELECT ?,source_id FROM memory_document_dependency WHERE memory_id=? UNION SELECT ?,?`, id, target, id, target); e != nil {
				return memory.Document{}, recall.ErrUnavailable
			}
		}
		meta, _ := json.Marshal(documentMetadata{NavigationTerms: navigationTerms(w.Title + "\n" + w.Content), Title: w.Title, Links: w.Links, Evidence: w.Evidence, Origin: func() string {
			if strings.HasPrefix(eventID, "dream:") {
				return "dream"
			}
			return "written"
		}()})
		sealed, e := s.vault.Seal(ctx, string(meta), "document:"+id)
		if e != nil {
			return memory.Document{}, e
		}
		if _, err = tx.ExecContext(ctx, `INSERT INTO memory_document VALUES(?,?,?,?)`, id, w.Layer, sealed, w.Revision+1); err != nil {
			return memory.Document{}, recall.ErrUnavailable
		}
		if w.Ref != "" {
			if err = s.inheritSemanticDocument(ctx, tx, strings.TrimPrefix(w.Ref, "memory:"), id); err != nil {
				return memory.Document{}, err
			}
		}
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO memory_document_receipt VALUES(?,?,?)`, eventID, hash, id); err != nil {
		return memory.Document{}, recall.ErrUnavailable
	}
	if err = tx.Commit(); err != nil {
		return memory.Document{}, recall.ErrUnavailable
	}
	return memory.Document{Ref: "memory:" + id, Layer: w.Layer, Title: w.Title, Revision: revision, CreatedAt: at}, nil
}

func (s *Store) HasDocumentReceipt(ctx context.Context, scope recall.Scope, ref, event string) (bool, error) {
	var exists bool
	err := s.db.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM memory_document_receipt receipt JOIN memory_record m USING(memory_id) WHERE receipt.event_id=? AND m.memory_id=? AND m.user_id=? AND m.scope_type='agent' AND m.scope_id=?)`, event, strings.TrimPrefix(ref, "memory:"), scope.UserID, scope.AgentID).Scan(&exists)
	return exists, err
}

// StaleInjected distinguishes invalidated dependencies from an intentionally
// empty/retired context. It reads metadata only and never resurrects old sources.
func (s *Store) StaleInjected(ctx context.Context, scope recall.Scope, floor int64) (bool, error) {
	key, err := s.StaleInjectedKey(ctx, scope, floor)
	return key != "", err
}

func (s *Store) StaleInjectedKey(ctx context.Context, scope recall.Scope, floor int64) (string, error) {
	var active int
	if err := s.db.QueryRowContext(ctx, `SELECT count(*)`+currentMemoryRows+` AND m.created_at>? AND EXISTS(SELECT 1 FROM memory_document x WHERE x.memory_id=m.memory_id AND x.layer='injected')`, scope.UserID, scope.UserID, scope.AgentID, floor).Scan(&active); err != nil {
		return "", recall.ErrUnavailable
	}
	if active > 0 {
		return "", nil
	}
	var stale string
	err := s.db.QueryRowContext(ctx, `SELECT coalesce((SELECT m.memory_id FROM memory_record m JOIN memory_document x USING(memory_id) WHERE m.user_id=? AND m.scope_type='agent' AND m.scope_id=? AND m.created_at>? AND m.deleted_at IS NULL AND x.layer='injected' AND NOT EXISTS(SELECT 1 FROM memory_retirement r WHERE r.memory_id=m.memory_id) AND NOT EXISTS(SELECT 1 FROM memory_record n WHERE n.supersedes_memory_id=m.memory_id) AND EXISTS(SELECT 1 FROM memory_document_dependency dep JOIN memory_record source ON source.memory_id=dep.source_id WHERE dep.memory_id=m.memory_id AND (source.deleted_at IS NOT NULL OR EXISTS(SELECT 1 FROM memory_record n WHERE n.supersedes_memory_id=source.memory_id) OR EXISTS(SELECT 1 FROM memory_retirement r WHERE r.memory_id=source.memory_id))) ORDER BY m.created_at DESC,m.memory_id DESC LIMIT 1),'')`, scope.UserID, scope.AgentID, floor).Scan(&stale)
	if err != nil {
		return "", recall.ErrUnavailable
	}
	return stale, nil
}

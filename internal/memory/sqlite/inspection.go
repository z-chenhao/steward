package sqlite

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"strings"
	"time"

	"github.com/z-chenhao/assistant/internal/memory"
	"github.com/z-chenhao/assistant/internal/recall"
)

type inspectionRow struct {
	Record   memory.Record
	At       int64
	Next     string
	Retired  bool
	Layer    string
	Metadata string
	Revision int64
}
type InspectionSnapshot struct {
	scope       recall.Scope
	floor       int64
	fingerprint [32]byte
}

// A read snapshot is closed before SecretStore calls. The fingerprint rechecks
// membership, representation, provenance and replacement changes before return.
func (s *Store) inspectionRows(ctx context.Context, scope recall.Scope, floor int64) ([]inspectionRow, error) {
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return nil, memory.ErrInspectionUnavailable
	}
	defer tx.Rollback()
	rows, err := tx.QueryContext(ctx, `SELECT m.memory_id,m.user_id,m.scope_type,m.scope_id,m.tier,m.content,m.source_refs,coalesce(m.supersedes_memory_id,''),m.created_at,coalesce((SELECT layer FROM memory_document x WHERE x.memory_id=m.memory_id),'notes'),coalesce((SELECT metadata FROM memory_document x WHERE x.memory_id=m.memory_id),''),coalesce((SELECT revision FROM memory_document x WHERE x.memory_id=m.memory_id),1),(EXISTS(SELECT 1 FROM memory_document_dependency dep JOIN memory_record source ON source.memory_id=dep.source_id WHERE dep.memory_id=m.memory_id AND (source.deleted_at IS NOT NULL OR EXISTS(SELECT 1 FROM memory_record successor WHERE successor.supersedes_memory_id=source.memory_id) OR EXISTS(SELECT 1 FROM memory_retirement retired WHERE retired.memory_id=source.memory_id))) OR EXISTS(SELECT 1 FROM memory_retirement x WHERE x.memory_id=m.memory_id) OR EXISTS(SELECT 1 FROM memory_semantic_record x WHERE x.memory_id=m.memory_id AND x.expires_at>0 AND x.expires_at<=unixepoch('subsec')*1000000)),coalesce((SELECT n.memory_id FROM memory_record n WHERE n.supersedes_memory_id=m.memory_id AND n.deleted_at IS NULL),'') FROM memory_record m LEFT JOIN recall_document d ON d.ref='memory:'||m.memory_id WHERE m.user_id=? AND ((m.scope_type='user' AND m.scope_id=?) OR (m.scope_type='agent' AND m.scope_id=?)) AND m.deleted_at IS NULL AND m.created_at>? AND (d.active=1 OR substr(lower(m.content),1,7)='sealed:' OR EXISTS(SELECT 1 FROM memory_record n WHERE n.supersedes_memory_id=m.memory_id AND n.deleted_at IS NULL)) ORDER BY m.created_at DESC,m.memory_id DESC`, scope.UserID, scope.UserID, scope.AgentID, floor)
	if err != nil {
		return nil, memory.ErrInspectionUnavailable
	}
	defer rows.Close()
	out := []inspectionRow{}
	for rows.Next() {
		var row inspectionRow
		var refs string
		r := &row.Record
		if rows.Scan(&r.ID, &r.UserID, &r.ScopeType, &r.ScopeID, &r.Tier, &r.Content, &refs, &r.Supersedes, &row.At, &row.Layer, &row.Metadata, &row.Revision, &row.Retired, &row.Next) != nil || json.Unmarshal([]byte(refs), &r.SourceRefs) != nil {
			return nil, memory.ErrInspectionUnavailable
		}
		r.CreatedAt = time.UnixMicro(row.At).UTC()
		out = append(out, row)
	}
	if rows.Err() != nil {
		return nil, memory.ErrInspectionUnavailable
	}
	return out, nil
}
func inspectionFingerprint(rows []inspectionRow) [32]byte {
	raw, _ := json.Marshal(rows)
	return sha256.Sum256(raw)
}
func (s *Store) Inspect(ctx context.Context, scope recall.Scope, floor int64, q memory.InspectionQuery) (memory.InspectionPage, InspectionSnapshot, error) {
	out := memory.InspectionPage{Items: []memory.InspectionRecord{}}
	snapshot := InspectionSnapshot{scope: scope, floor: floor}
	if err := q.Validate(); err != nil {
		return out, snapshot, err
	}
	position, err := q.Position(scope.UserID+":"+scope.AgentID, floor)
	if err != nil {
		return out, snapshot, err
	}
	rows, err := s.inspectionRows(ctx, scope, floor)
	if err != nil {
		return out, snapshot, err
	}
	snapshot.fingerprint = inspectionFingerprint(rows)
	visible := map[string]bool{}
	for _, row := range rows {
		visible[row.Record.ID] = true
	}
	for _, row := range rows {
		r := row.Record
		v := memory.InspectionRecord{Layer: row.Layer, Revision: row.Revision, Ref: "memory:" + r.ID, Tier: r.Tier, CreatedAt: r.CreatedAt, Status: "active", Origin: "unknown", ContentState: "available", Sources: []memory.InspectionSource{}}
		if row.Retired {
			v.Status = "retired"
			v.ContentState = "retired"
		}
		if row.Next != "" {
			v.Status = "superseded"
			v.ContentState = "retired"
			if visible[row.Next] {
				v.ReplacedBy = "memory:" + row.Next
			}
		}
		if visible[r.Supersedes] {
			v.Supersedes = "memory:" + r.Supersedes
		}
		seen := map[string]bool{}
		explicit, automatic := false, false
		for _, source := range r.SourceRefs {
			if len(source.Ref) > 256 || seen[source.Type+source.Ref] {
				continue
			}
			seen[source.Type+source.Ref] = true
			explicit = explicit || source.Type == "feedback"
			automatic = automatic || source.Type == "conversation" || source.Type == "task"
			v.Sources = append(v.Sources, memory.InspectionSource{Kind: source.Type, Ref: source.Ref})
		}
		if automatic {
			v.Origin = "automatic"
		}
		if explicit {
			v.Origin = "explicit"
		}
		if q.Ref != "" && q.Ref != v.Ref {
			continue
		}
		if q.Ref == "" {
			if !q.Since.IsZero() && r.CreatedAt.Before(q.Since) || !q.Until.IsZero() && !r.CreatedAt.Before(q.Until) {
				continue
			}
			status := q.Status
			if status == "" {
				status = "active"
			}
			if status != "all" && status != v.Status || q.Tier != "" && q.Tier != v.Tier || q.Origin != "" && q.Origin != v.Origin {
				continue
			}
		}
		layer := q.Layer
		if layer == "" {
			layer = memory.Notes
		}
		if q.Ref == "" && v.Layer != layer {
			continue
		}
		if v.Status == "active" {
			v.Content = r.Content
			if strings.HasPrefix(strings.ToLower(r.Content), "sealed:") {
				v.Content, err = s.vault.Open(ctx, r.Content, binding(r))
				if err != nil {
					v.Content = ""
					v.ContentState = "unavailable"
					if q.Search != "" {
						return memory.InspectionPage{}, snapshot, memory.ErrInspectionUnavailable
					}
				}
			}
		}
		if row.Metadata != "" && v.ContentState == "available" {
			plain, e := s.vault.Open(ctx, row.Metadata, "document:"+r.ID)
			var meta documentMetadata
			if e != nil || json.Unmarshal([]byte(plain), &meta) != nil {
				v.Content = ""
				v.ContentState = "unavailable"
				if q.Search != "" {
					return memory.InspectionPage{}, snapshot, memory.ErrInspectionUnavailable
				}
			} else {
				v.Title, v.Links = meta.Title, meta.Links
				if meta.Origin != "" {
					v.Origin = meta.Origin
				}
			}
		}
		if q.Ref == "" && q.Search != "" && !strings.Contains(strings.ToLower(v.Title+" "+v.Content+" "+v.Ref), strings.ToLower(q.Search)) {
			continue
		}
		out.Total++
		if position.Ref != "" && (row.At > position.At || row.At == position.At && v.Ref >= position.Ref) {
			continue
		}
		if len(out.Items) < q.PageSize() {
			out.Items = append(out.Items, v)
		} else if out.Next == "" {
			last := out.Items[len(out.Items)-1]
			out.Next = q.Next(scope.UserID+":"+scope.AgentID, floor, last.CreatedAt.UnixMicro(), last.Ref)
		}
	}
	if q.Ref != "" && len(out.Items) == 0 {
		return out, snapshot, memory.ErrInspectionNotFound
	}
	return out, snapshot, nil
}
func (s *Store) RecheckInspection(ctx context.Context, snapshot InspectionSnapshot) error {
	rows, err := s.inspectionRows(ctx, snapshot.scope, snapshot.floor)
	if err != nil {
		return err
	}
	if inspectionFingerprint(rows) != snapshot.fingerprint {
		return memory.ErrInspectionUnavailable
	}
	return nil
}

package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"time"

	"github.com/z-chenhao/assistant/internal/artifact"
	"github.com/z-chenhao/assistant/internal/evidence"
	"github.com/z-chenhao/assistant/internal/memory"
	memorysqlite "github.com/z-chenhao/assistant/internal/memory/sqlite"
	"github.com/z-chenhao/assistant/internal/recall"
)

// InspectMemory is an owner-only read. No decrypted content enters an agent
// Tool result, event, checkpoint or durable search index.
func (r *Runs) InspectMemory(ctx context.Context, q memory.InspectionQuery, store *memorysqlite.Store) (memory.InspectionPage, error) {
	if err := q.Validate(); err != nil {
		return memory.InspectionPage{}, err
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	var access evidenceAccess
	err := r.evidenceReadTx(ctx, func(tx *sql.Tx) error { var err error; access, err = r.evidenceAccess(ctx, tx, nil); return err })
	if err != nil {
		return memory.InspectionPage{}, memory.ErrInspectionUnavailable
	}
	if q.Layer == memory.Transcripts {
		return r.inspectMemoryTranscripts(ctx, q, access)
	}
	if q.View == "maintenance" {
		return r.inspectMemoryMaintenance(ctx, q, access)
	}
	if store == nil {
		return memory.InspectionPage{}, memory.ErrInspectionUnavailable
	}
	scope := recall.Scope{UserID: access.scope.UserID, AgentID: access.scope.AgentID}
	page, snapshot, err := store.Inspect(ctx, scope, access.floor, q)
	if err != nil {
		return memory.InspectionPage{}, err
	}
	for i := range page.Items {
		record := &page.Items[i]
		for _, ref := range record.Links {
			doc, e := store.ReadDocument(ctx, scope, access.floor, ref)
			if e != nil {
				continue
			}
			record.Related = append(record.Related, memory.InspectionRelated{Ref: ref, Title: doc.Title})
		}
		sources := []memory.InspectionSource{}
		for _, source := range record.Sources {
			switch source.Kind {
			case "conversation", "task":
				if !strings.HasPrefix(source.Ref, source.Kind+":") || !evidence.ValidRef(source.Ref) {
					continue
				}
				visible, e := r.memoryEvidenceSourceVisible(ctx, access, scope, source.Ref)
				if e != nil {
					return memory.InspectionPage{}, memory.ErrInspectionUnavailable
				}
				if !visible {
					continue
				}
			case "run":
				id := strings.Split(strings.TrimPrefix(source.Ref, "run:"), ":")[0]
				var visible bool
				if err = r.host.db.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM run WHERE run_id=? AND agent_id=? AND kind='worker' AND status IN('completed','failed','cancelled') AND created_at>?)`, id, scope.AgentID, access.floor).Scan(&visible); err != nil {
					return memory.InspectionPage{}, err
				}
				if !visible {
					continue
				}
				source.RunID = id
			case "feedback":
				id, ok := strings.CutPrefix(source.Ref, "feedback:")
				if !ok {
					continue
				}
				if strings.HasPrefix(id, "control:") {
					var exists bool
					if err = r.host.db.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM memory_mutation WHERE agent_id=? AND source_type='feedback' AND source_ref=? AND accepted_at>?)`, scope.AgentID, id, access.floor).Scan(&exists); err != nil {
						return memory.InspectionPage{}, memory.ErrInspectionUnavailable
					}
					if !exists {
						exists, err = store.HasDocumentReceipt(ctx, scope, record.Ref, "document:"+id)
						if err != nil {
							return memory.InspectionPage{}, memory.ErrInspectionUnavailable
						}
					}
					if !exists {
						continue
					}
					source.Kind = "control"
				} else {
					err = r.host.db.QueryRowContext(ctx, `SELECT i.run_id FROM tool_invocation i JOIN run r USING(run_id) WHERE r.agent_id=? AND i.invocation_id=? AND i.tool_name='builtin__memory_manage' AND i.created_at>?`, scope.AgentID, id, access.floor).Scan(&source.RunID)
					if errors.Is(err, sql.ErrNoRows) {
						continue
					}
					if err != nil {
						return memory.InspectionPage{}, memory.ErrInspectionUnavailable
					}
					source.Kind = "tool"
				}
			default:
				continue
			}
			if q.Source == source.Ref && (source.Kind == "conversation" || source.Kind == "task") {
				body, e := r.ReadEvidence(ctx, evidence.Request{Ref: source.Ref, Offset: q.Offset, Limit: 4096})
				if e != nil {
					if errors.Is(e, artifact.ErrNotFound) {
						return memory.InspectionPage{}, memory.ErrInspectionNotFound
					}
					if errors.Is(e, evidence.ErrInvalid) {
						return memory.InspectionPage{}, memory.ErrInspectionInvalid
					}
					return memory.InspectionPage{}, memory.ErrInspectionUnavailable
				}
				source.Text, source.NextOffset = body.Text, body.NextOffset
			}
			sources = append(sources, source)
		}
		record.SourceCount = len(sources)
		if q.SourcesOffset > len(sources) {
			return memory.InspectionPage{}, memory.ErrInspectionInvalid
		}
		end := min(q.SourcesOffset+20, len(sources))
		if end < len(sources) {
			next := end
			record.NextSourcesOffset = &next
		}
		record.Sources = sources[q.SourcesOffset:end]

		if q.Source != "" {
			found := false
			for _, source := range record.Sources {
				found = found || source.Ref == q.Source && (source.Kind == "conversation" || source.Kind == "task")
			}
			if !found {
				return memory.InspectionPage{}, memory.ErrInspectionNotFound
			}
		}
	}
	r.host.boundary("after_memory_inspection")
	if err = store.RecheckInspection(ctx, snapshot); err != nil {
		return memory.InspectionPage{}, err
	}
	if err = r.recheckMemoryOwner(ctx, access); err != nil {
		return memory.InspectionPage{}, err
	}
	return page, nil
}
func (r *Runs) recheckMemoryOwner(ctx context.Context, access evidenceAccess) error {
	err := r.evidenceReadTx(ctx, func(tx *sql.Tx) error {
		current, err := r.evidenceAccess(ctx, tx, nil)
		if err != nil {
			return err
		}
		if current.scope != access.scope || current.epoch != access.epoch || current.floor != access.floor {
			return memory.ErrInspectionNotFound
		}
		return nil
	})
	if err != nil {
		if errors.Is(err, memory.ErrInspectionNotFound) {
			return err
		}
		return memory.ErrInspectionUnavailable
	}
	return nil
}
func (r *Runs) inspectMemoryMaintenance(ctx context.Context, q memory.InspectionQuery, access evidenceAccess) (memory.InspectionPage, error) {
	out := memory.InspectionPage{Items: []memory.InspectionRecord{}, Maintenance: []memory.MaintenanceRecord{}}
	position, err := q.Position(access.scope.AgentID, access.floor)
	if err != nil {
		return out, err
	}
	err = r.evidenceReadTx(ctx, func(tx *sql.Tx) error {
		if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM memory_mutation WHERE agent_id=? AND (operation IN('remember','forget') OR operation='observe' AND source_type='dream')`, access.scope.AgentID).Scan(&out.Total); err != nil {
			return err
		}
		rows, err := tx.QueryContext(ctx, `SELECT e.event_id,CASE WHEN e.source_type='dream' THEN 'dream' ELSE e.operation END,e.accepted_at,e.applied_at,e.failed_at,coalesce(e.last_error,''),e.attempts,coalesce((SELECT i.run_id FROM tool_invocation i JOIN run r USING(run_id) WHERE i.invocation_id=e.source_ref AND i.tool_name='builtin__memory_manage' AND r.agent_id=e.agent_id AND i.created_at>?), '') FROM memory_mutation e WHERE e.agent_id=? AND (e.operation IN('remember','forget') OR e.operation='observe' AND e.source_type='dream') AND (?='' OR e.event_id=?) AND (?='' OR (e.accepted_at,e.event_id)<(?,?)) ORDER BY e.accepted_at DESC,e.event_id DESC LIMIT ?`, access.floor, access.scope.AgentID, q.Ref, strings.TrimPrefix(q.Ref, "memory:maintenance-"), position.Ref, position.At, strings.TrimPrefix(position.Ref, "memory:"), q.PageSize()+1)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var item memory.MaintenanceRecord
			var id string
			var at int64
			var applied, failed sql.NullInt64
			if err = rows.Scan(&id, &item.Operation, &at, &applied, &failed, &item.Error, &item.Attempts, &item.RunID); err != nil {
				return err
			}
			item.Ref = "memory-event:" + id
			item.CreatedAt = time.UnixMicro(at).UTC()
			item.Status = "pending"
			if applied.Valid {
				item.Status = "applied"
			}
			if failed.Valid {
				item.Status = "rejected"
			}
			if len(out.Maintenance) == q.PageSize() {
				last := out.Maintenance[len(out.Maintenance)-1]
				out.Next = q.Next(access.scope.AgentID, access.floor, last.CreatedAt.UnixMicro(), "memory:"+strings.TrimPrefix(last.Ref, "memory-event:"))
				break
			}
			out.Maintenance = append(out.Maintenance, item)
		}
		return rows.Err()
	})
	if err != nil {
		return memory.InspectionPage{}, memory.ErrInspectionUnavailable
	}
	if q.Ref != "" && len(out.Maintenance) != 1 {
		return out, memory.ErrInspectionNotFound
	}
	for i := range out.Maintenance {
		m, e := r.memoryMetrics(ctx, strings.TrimPrefix(out.Maintenance[i].Ref, "memory-event:"))
		if e != nil {
			return memory.InspectionPage{}, e
		}
		out.Maintenance[i].Metrics = &m
	}
	if err = r.recheckMemoryOwner(ctx, access); err != nil {
		return memory.InspectionPage{}, err
	}
	return out, nil
}

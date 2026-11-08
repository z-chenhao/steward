package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"

	"github.com/z-chenhao/assistant/internal/conversation"
	"github.com/z-chenhao/assistant/internal/evidence"
	"github.com/z-chenhao/assistant/internal/memory"
)

// Owner projection of original Conversation evidence, not a database browser.
// The original remains in its owning store and detail reads reuse authorization.
func (r *Runs) inspectMemoryTranscripts(ctx context.Context, q memory.InspectionQuery, a evidenceAccess) (memory.InspectionPage, error) {
	out := memory.InspectionPage{Items: []memory.InspectionRecord{}}
	position, err := q.Position(a.scope.AgentID, a.floor)
	if err != nil {
		return out, err
	}
	err = r.evidenceReadTx(ctx, func(tx *sql.Tx) error {
		where := `json_extract(message_json,'$.conversation_id')=? AND memory_created_at>?`
		args := []any{a.scope.AgentID, a.floor}
		if !q.Since.IsZero() {
			where += ` AND memory_created_at>=?`
			args = append(args, q.Since.UnixMicro())
		}
		if !q.Until.IsZero() {
			where += ` AND memory_created_at<?`
			args = append(args, q.Until.UnixMicro())
		}
		if q.Ref != "" {
			id, ok := strings.CutPrefix(q.Ref, "memory:transcript-")
			if !ok {
				return memory.ErrInspectionNotFound
			}
			where += ` AND message_id=?`
			args = append(args, id)
		}
		if q.Search == "" {
			if e := tx.QueryRowContext(ctx, `SELECT count(*) FROM conversation_intent WHERE `+where, args...).Scan(&out.Total); e != nil {
				return e
			}
		}
		// Exact substring search may scan text, but uses bounded keyset batches.
		// Ordinary pages and ID details read only page-size + 1 rows from the index.
		scanAt, scanID := int64(0), ""
		for {
			predicate := where
			params := append([]any(nil), args...)
			if q.Search != "" && scanID != "" {
				predicate += ` AND (memory_created_at,message_id)<(?,?)`
				params = append(params, scanAt, scanID)
			} else if q.Search == "" && position.Ref != "" {
				predicate += ` AND (memory_created_at,message_id)<(?,?)`
				params = append(params, position.At, strings.TrimPrefix(position.Ref, "memory:transcript-"))
			}
			limit := q.PageSize() + 1
			if q.Search != "" {
				limit = 128
			}
			params = append(params, limit)
			rows, e := tx.QueryContext(ctx, `SELECT message_json,memory_created_at,message_id,coalesce((SELECT run_id FROM main_input i WHERE i.message_id=c.message_id AND i.agent_id=?),'') FROM conversation_intent c WHERE `+predicate+` ORDER BY memory_created_at DESC,message_id DESC LIMIT ?`, append([]any{a.scope.AgentID}, params...)...)
			if e != nil {
				return e
			}
			count := 0
			for rows.Next() {
				count++
				var raw, id, runID string
				var at int64
				if e = rows.Scan(&raw, &at, &id, &runID); e != nil {
					break
				}
				scanAt, scanID = at, id
				var m conversation.Message
				if e = json.Unmarshal([]byte(raw), &m); e != nil {
					break
				}
				var body strings.Builder
				for _, part := range m.Content {
					if part.Type == "text" {
						body.WriteString(part.Text)
						body.WriteByte('\n')
					}
				}
				ref := "memory:transcript-" + id
				if q.Search != "" {
					if !strings.Contains(strings.ToLower(body.String()+id), strings.ToLower(q.Search)) {
						continue
					}
					out.Total++
				}
				if position.Ref != "" && (at > position.At || at == position.At && ref >= position.Ref) {
					continue
				}
				if len(out.Items) == q.PageSize() {
					if out.Next == "" {
						last := out.Items[len(out.Items)-1]
						out.Next = q.Next(a.scope.AgentID, a.floor, last.CreatedAt.UnixMicro(), last.Ref)
					}
					continue
				}
				page := evidence.Page{}
				if e = evidence.Window(&page, body.String(), evidence.Request{Limit: 4096}); e != nil {
					break
				}
				out.Items = append(out.Items, memory.InspectionRecord{Ref: ref, Layer: memory.Transcripts, Title: m.Role, Content: page.Text, ContentState: "available", Status: "active", Origin: "original", CreatedAt: m.CreatedAt, Sources: []memory.InspectionSource{{Kind: "conversation", Ref: "conversation:" + id, RunID: runID}}, SourceCount: 1})
			}
			if e == nil {
				e = rows.Err()
			}
			rows.Close()
			if e != nil {
				return e
			}
			if q.Search == "" || count < limit {
				break
			}
		}
		return nil
	})
	if err != nil {
		if errors.Is(err, memory.ErrInspectionNotFound) {
			return out, err
		}
		return out, memory.ErrInspectionUnavailable
	}
	if q.Ref != "" && len(out.Items) != 1 {
		return out, memory.ErrInspectionNotFound
	}
	if q.Ref != "" && q.Source != "" {
		if q.Source != out.Items[0].Sources[0].Ref {
			return out, memory.ErrInspectionNotFound
		}
		page, e := r.ReadEvidence(ctx, evidence.Request{Ref: q.Source, Offset: q.Offset, Limit: 4096})
		if e != nil {
			return out, memory.ErrInspectionNotFound
		}
		out.Items[0].Sources[0].Text = page.Text
		out.Items[0].Sources[0].NextOffset = page.NextOffset
	}
	if err = r.recheckMemoryOwner(ctx, a); err != nil {
		return memory.InspectionPage{}, err
	}
	return out, nil
}

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
	"github.com/z-chenhao/assistant/internal/tool"
)

// Run/receipt references select immutable originals. They grant no authority;
// the Host checks ownership, terminal state and the same Forget floor first.
func (r *Runs) workerMemoryOriginal(ctx context.Context, tx *sql.Tx, agentID, ref string, floor int64) (string, time.Time, string, error) {
	parts := strings.Split(strings.TrimSuffix(strings.TrimPrefix(ref, "run:"), ":task"), ":tool:")
	if len(parts) > 2 || parts[0] == "" {
		return "", time.Time{}, "", recall.ErrInvalid
	}
	var at int64
	err := tx.QueryRowContext(ctx, `SELECT created_at FROM run WHERE run_id=? AND agent_id=? AND kind='worker' AND status IN('completed','failed','cancelled') AND created_at>?`, parts[0], agentID, floor).Scan(&at)
	if err == sql.ErrNoRows {
		return "", time.Time{}, "", recall.ErrConflict
	}
	if err != nil {
		return "", time.Time{}, "", err
	}
	if len(parts) == 1 {
		var anchor string
		err = tx.QueryRowContext(ctx, `SELECT anchor_task FROM task WHERE task_id=(SELECT task_id FROM run WHERE run_id=? AND agent_id=?)`, parts[0], agentID).Scan(&anchor)
		if err != nil {
			return "", time.Time{}, "", err
		}
		if memory.HasSecret(anchor) {
			return "", time.Time{}, "", recall.ErrConflict
		}
		return anchor, time.UnixMicro(at).UTC(), "task", nil
	}
	var name, status, args, raw string
	err = tx.QueryRowContext(ctx, `SELECT tool_name,status,arguments_json,result_json,coalesce(result_ready_at,updated_at) FROM tool_invocation WHERE invocation_id=? AND run_id=? AND status IN('succeeded','failed','cancelled','unknown')`, parts[1], parts[0]).Scan(&name, &status, &args, &raw, &at)
	if err == sql.ErrNoRows {
		return "", time.Time{}, "", recall.ErrConflict
	}
	if err != nil {
		return "", time.Time{}, "", err
	}
	var result tool.Result
	if json.Unmarshal([]byte(raw), &result) != nil {
		return "", time.Time{}, "", recall.ErrUnavailable
	}
	if status == "succeeded" && (name == memory.ManageName || name == recall.ToolName || name == "builtin__codemode_execute" || name == "builtin__tool_search") {
		return "", time.Time{}, "", recall.ErrConflict
	}
	body := fmt.Sprintf("Tool: %s\nStatus: %s\nArguments:\n%s\nResult:\n%s", name, status, args, result.Text)
	if result.Error != nil {
		b, _ := json.Marshal(result.Error)
		body += "\nError: " + string(b)
	}
	if memory.HasSecret(body) {
		return "", time.Time{}, "", recall.ErrConflict
	}
	return body, time.UnixMicro(at).UTC(), "tool", nil
}

func (r *Runs) dreamWorkerEvidence(ctx context.Context, scope recall.Scope, in *memory.CurationInput, at time.Time, floor int64, budget int) error {
	return r.evidenceReadTx(ctx, func(tx *sql.Tx) error {
		var after, offset int64
		if e := tx.QueryRowContext(ctx, `SELECT coalesce((SELECT worker_sequence FROM memory_dream_coverage WHERE agent_id=?),0),coalesce((SELECT worker_offset FROM memory_dream_coverage WHERE agent_id=?),0)`, scope.AgentID, scope.AgentID).Scan(&after, &offset); e != nil {
			return e
		}
		in.WorkerThrough, in.WorkerOffset = after, offset
		rows, e := tx.QueryContext(ctx, `SELECT e.sequence,e.source_ref FROM memory_mutation e JOIN run r ON r.run_id=e.source_ref AND r.agent_id=e.agent_id WHERE e.agent_id=? AND e.operation='observe' AND e.source_type IN('task','run') AND (e.sequence>? OR (e.sequence=? AND ?>0)) AND e.accepted_at<=? AND e.accepted_at>? AND r.kind='worker' AND r.status IN('completed','failed','cancelled') AND NOT EXISTS(SELECT 1 FROM memory_mutation prior WHERE prior.agent_id=e.agent_id AND prior.source_ref=e.source_ref AND prior.operation='observe' AND prior.source_type IN('task','run') AND prior.sequence<e.sequence) ORDER BY e.sequence LIMIT 8`, scope.AgentID, after, after, offset, at.UnixMicro(), floor)
		if e != nil {
			return e
		}
		type event struct {
			sequence int64
			run      string
		}
		events := []event{}
		for rows.Next() {
			var v event
			if e = rows.Scan(&v.sequence, &v.run); e != nil {
				break
			}
			events = append(events, v)
		}
		if e == nil {
			e = rows.Err()
		}
		rows.Close()
		if e != nil {
			return e
		}
		for _, event := range events {
			start := int64(0)
			if event.sequence == after {
				start = offset
			}
			// Pagination counts immutable source selectors, including excluded receipts,
			// so a secret/derived value cannot stall or shift later evidence.
			tools, e := tx.QueryContext(ctx, `SELECT invocation_id FROM tool_invocation WHERE run_id=? AND status IN('succeeded','failed','cancelled','unknown') ORDER BY created_at,invocation_id LIMIT 65 OFFSET ?`, event.run, max(0, start-1))
			if e != nil {
				return e
			}
			refs := []string{}
			if start == 0 {
				refs = append(refs, "run:"+event.run+":task")
			}
			toolCount, more := 0, false
			for tools.Next() {
				var id string
				if e = tools.Scan(&id); e != nil {
					break
				}
				if toolCount == 64 {
					more = true
					break
				}
				refs = append(refs, "run:"+event.run+":tool:"+id)
				toolCount++
			}
			if e == nil {
				e = tools.Err()
			}
			tools.Close()
			if e != nil {
				return e
			}
			for i, ref := range refs {
				if budget < 1024 {
					return nil
				}
				text, when, author, e := r.workerMemoryOriginal(ctx, tx, scope.AgentID, ref, floor)
				if e != nil && e != recall.ErrConflict {
					return e
				}
				if e == nil {
					full := len(text)
					n := min(full, min(4096, budget))
					for n > 0 && !utf8.ValidString(text[:n]) {
						n--
					}
					in.Observations = append(in.Observations, memory.Observation{Source: memory.SourceRef{Type: "run", Ref: ref}, Author: author, At: when, Text: text[:n], TotalBytes: int64(full)})
					budget -= n
				}
				in.WorkerThrough, in.WorkerOffset = event.sequence, start+int64(i)+1
			}
			if !more {
				in.WorkerThrough, in.WorkerOffset = event.sequence, 0
			} else {
				return nil
			}
		}
		return nil
	})
}

// Re-open a few original sources of selected older notes. Coverage prevents
// repeated background ingestion; it must not forbid checking an old theory.
func (r *Runs) dreamOriginalSources(ctx context.Context, scope recall.Scope, in *memory.CurationInput, floor int64, budget int) error {
	seen := map[string]bool{}
	for _, o := range in.Observations {
		seen[o.Source.Ref] = true
	}
	return r.evidenceReadTx(ctx, func(tx *sql.Tx) error {
		count := 0
		for _, doc := range in.Documents {
			for _, src := range doc.Sources {
				if count >= 4 || budget < 512 {
					return nil
				}
				if seen[src.Ref] {
					continue
				}
				seen[src.Ref] = true
				var text, author string
				var fullBytes int64
				var at time.Time
				if src.Type == "run" {
					var e error
					text, at, author, e = r.workerMemoryOriginal(ctx, tx, scope.AgentID, src.Ref, floor)
					if e == recall.ErrConflict {
						continue
					}
					if e != nil {
						return e
					}
				} else if src.Type == "conversation" {
					v, e := r.readMemoryTranscript(ctx, tx, scope.AgentID, floor, memory.Mutation{Operation: "read", Layer: memory.Transcripts, MemoryRef: src.Ref, Limit: 4096})
					if e == recall.ErrConflict {
						continue
					}
					if e != nil {
						return e
					}
					raw, _ := json.Marshal(v)
					var page struct {
						Text   string
						Author string
						At     time.Time `json:"occurred_at"`
						Bytes  int64     `json:"total_bytes"`
					}
					if e = json.Unmarshal(raw, &page); e != nil {
						return e
					}
					text, author, at = page.Text, page.Author, page.At
					fullBytes = page.Bytes
				} else {
					continue
				}
				if at.After(in.Observation.At) || memory.HasSecret(text) {
					continue
				}
				if fullBytes == 0 {
					fullBytes = int64(len(text))
				}
				excerpt := recall.Excerpt(recall.Hit{Content: text}, in.Goal, min(2048, budget))
				in.Observations = append(in.Observations, memory.Observation{Source: src, Author: author, At: at, Text: excerpt.Content, Offset: int64(excerpt.Offset), TotalBytes: fullBytes})
				budget -= len(excerpt.Content)
				count++
			}
		}
		return nil
	})
}

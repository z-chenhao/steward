package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"slices"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/z-chenhao/assistant/internal/agent"
	"github.com/z-chenhao/assistant/internal/conversation"
	"github.com/z-chenhao/assistant/internal/llm"
	"github.com/z-chenhao/assistant/internal/memory"
	"github.com/z-chenhao/assistant/internal/recall"
)

// scheduleDream coalesces observations into bounded background work. It uses
// the shared durable journal, not a user Task, timer file or filesystem scan.
func (r *Runs) scheduleDream(ctx context.Context) (bool, error) {
	if r.memoryProvider == nil || r.vault.Ref == "" {
		return false, nil
	}
	if _, ok := r.memory.(memory.Documents); !ok {
		return false, nil
	}
	queued := false
	err := r.write(ctx, func(tx *sql.Tx) error {
		var owner string
		if e := tx.QueryRowContext(ctx, `SELECT agent_id FROM agent_config WHERE singleton=1`).Scan(&owner); e != nil {
			return e
		}
		var count, last, oldest, after, offset int64
		if e := tx.QueryRowContext(ctx, `SELECT coalesce((SELECT through_sequence FROM memory_dream_coverage WHERE agent_id=?),0),coalesce((SELECT next_offset FROM memory_dream_coverage WHERE agent_id=?),0)`, owner, owner).Scan(&after, &offset); e != nil {
			return e
		}
		e := tx.QueryRowContext(ctx, `SELECT count(*),coalesce(max(sequence),0),coalesce(min(CAST((julianday(json_extract(message_json,'$.created_at'))-2440587.5)*86400000000 AS INTEGER)),0) FROM conversation_intent WHERE json_extract(message_json,'$.conversation_id')=? AND (sequence>? OR (sequence=? AND ?>0)) AND CAST((julianday(json_extract(message_json,'$.created_at'))-2440587.5)*86400000000 AS INTEGER)>coalesce((SELECT updated_at FROM memory_privacy WHERE agent_id=?),0)`, owner, after, after, offset, owner).Scan(&count, &last, &oldest)
		if e != nil {
			return e
		}
		var workerAfter, workerOffset, pendingWorkers, workerOldest int64
		if e = tx.QueryRowContext(ctx, `SELECT coalesce((SELECT worker_sequence FROM memory_dream_coverage WHERE agent_id=?),0),coalesce((SELECT worker_offset FROM memory_dream_coverage WHERE agent_id=?),0)`, owner, owner).Scan(&workerAfter, &workerOffset); e != nil {
			return e
		}
		if e = tx.QueryRowContext(ctx, `SELECT count(*),coalesce(min(e.accepted_at),0) FROM memory_mutation e JOIN run r ON r.run_id=e.source_ref AND r.agent_id=e.agent_id WHERE e.agent_id=? AND e.source_type IN('task','run') AND e.operation='observe' AND (e.sequence>? OR (e.sequence=? AND ?>0)) AND r.kind='worker' AND r.status IN('completed','failed','cancelled') AND e.accepted_at>coalesce((SELECT updated_at FROM memory_privacy WHERE agent_id=?),0) AND NOT EXISTS(SELECT 1 FROM memory_mutation prior WHERE prior.agent_id=e.agent_id AND prior.source_ref=e.source_ref AND prior.operation='observe' AND prior.source_type IN('task','run') AND prior.sequence<e.sequence)`, owner, workerAfter, workerAfter, workerOffset, owner).Scan(&pendingWorkers, &workerOldest); e != nil {
			return e
		}
		if oldest == 0 || workerOldest > 0 && workerOldest < oldest {
			oldest = workerOldest
		}
		staleKey := ""
		if count+pendingWorkers == 0 || count+pendingWorkers < 8 && offset == 0 && workerOffset == 0 && time.Now().UnixMicro()-oldest < int64(5*time.Minute/time.Microsecond) {
			reader, ok := r.memory.(interface {
				StaleInjectedKey(context.Context, recall.Scope, int64) (string, error)
			})
			if !ok {
				return nil
			}
			var floor int64
			if e := tx.QueryRowContext(ctx, `SELECT coalesce((SELECT updated_at FROM memory_privacy WHERE agent_id=?),0)`, owner).Scan(&floor); e != nil {
				return e
			}
			stale, e := reader.StaleInjectedKey(ctx, recall.Scope{UserID: r.host.OwnerID(), AgentID: owner}, floor)
			if e != nil {
				return e
			}
			if stale == "" {
				return nil
			}
			staleKey = stale
		}
		ref := fmt.Sprintf("after-%d-%d-%d-%d-workers-%d-%d-%d", last, count, after, offset, workerAfter, workerOffset, pendingWorkers)
		if staleKey != "" {
			ref += "-stale-" + staleKey
		}
		var exists bool
		if e = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM memory_mutation WHERE operation='observe' AND source_type='dream' AND source_ref=?)`, ref).Scan(&exists); e != nil {
			return e
		}
		if exists {
			return nil
		}
		_, e = r.queueMemory(ctx, tx, owner, "observe", "dream", ref, "")
		queued = e == nil
		return e
	})
	return queued, err
}

func (r *Runs) dreamInput(ctx context.Context, scope recall.Scope, event string, at time.Time, floor int64, pass int) (memory.CurationInput, error) {
	in := memory.CurationInput{Version: 3, Mode: "dream", Pass: pass, Observation: memory.Observation{EventID: event, Author: "maintenance", At: at}}
	var goal, key string
	if e := r.host.db.QueryRowContext(ctx, `SELECT payload,source_ref FROM memory_mutation WHERE event_id=? AND agent_id=?`, event, scope.AgentID).Scan(&goal, &key); e != nil {
		return in, agent.ErrStorage
	}
	if goal != "" {
		var e error
		in.Goal, e = r.vault.Open(ctx, goal, "dream-goal:"+key)
		if e != nil {
			return in, e
		}
	}
	var window, output int64
	if e := r.host.db.QueryRowContext(ctx, `SELECT p.context_window,p.max_output_tokens FROM agent_config a JOIN model_profile p ON p.profile_id=a.active_model_profile_id WHERE a.agent_id=? AND a.setup_status='ready' AND p.status='ready'`, scope.AgentID).Scan(&window, &output); e != nil {
		return in, agent.ErrSetup
	}
	totalBudget := min(64<<10, int((window-output)*3/2)-2048-len(in.Goal))
	if totalBudget < 4096 {
		return in, llm.ContextLengthExceeded
	}
	sourceBudget := min(32<<10, totalBudget/3)
	var after, offset int64
	if e := r.host.db.QueryRowContext(ctx, `SELECT coalesce((SELECT through_sequence FROM memory_dream_coverage WHERE agent_id=?),0),coalesce((SELECT next_offset FROM memory_dream_coverage WHERE agent_id=?),0)`, scope.AgentID, scope.AgentID).Scan(&after, &offset); e != nil {
		return in, agent.ErrStorage
	}
	in.Through, in.NextOffset = after, offset
	rows, err := r.host.db.QueryContext(ctx, `SELECT sequence,message_json FROM conversation_intent WHERE json_extract(message_json,'$.conversation_id')=? AND (sequence>? OR (sequence=? AND ?>0)) ORDER BY sequence LIMIT 24`, scope.AgentID, after, after, offset)
	if err != nil {
		return in, agent.ErrStorage
	}
	defer rows.Close()
	bytes := 0
	for rows.Next() {
		var raw string
		var sequence int64
		var m conversation.Message
		if rows.Scan(&sequence, &raw) != nil || json.Unmarshal([]byte(raw), &m) != nil {
			rows.Close()
			return in, agent.ErrStorage
		}
		if m.CreatedAt.After(at) {
			break
		}
		if m.CreatedAt.UnixMicro() <= floor {
			in.Through, in.NextOffset = sequence, 0
			continue
		}
		var text strings.Builder
		for _, part := range m.Content {
			if part.Type == "text" {
				text.WriteString(part.Text)
				text.WriteByte('\n')
			}
		}
		if memory.HasSecret(text.String()) {
			in.Through, in.NextOffset = sequence, 0
			continue
		}
		start := 0
		if sequence == after {
			start = int(offset)
		}
		if start > text.Len() {
			return in, recall.ErrConflict
		}
		available := sourceBudget - bytes
		if available < 1024 {
			break
		}
		body := text.String()
		end := min(len(body), start+available)
		for end > start && end < len(body) && !utf8.RuneStart(body[end]) {
			end--
		}
		if end == start {
			in.Through, in.NextOffset = sequence, 0
			continue
		}
		in.Observations = append(in.Observations, memory.Observation{Source: memory.SourceRef{Type: "conversation", Ref: "conversation:" + m.MessageID}, Author: m.Role, At: m.CreatedAt, Text: body[start:end], Offset: int64(start), TotalBytes: int64(len(body))})
		bytes += end - start
		in.Through, in.NextOffset = sequence, 0
		if end < len(body) {
			in.NextOffset = int64(end)
			break
		}
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return in, agent.ErrStorage
	}
	if err = r.dreamWorkerEvidence(ctx, scope, &in, at, floor, max(0, totalBudget/3)); err != nil {
		return in, err
	}
	bytes = 0
	for _, o := range in.Observations {
		bytes += len(o.Text)
	}
	store := r.memory.(memory.Documents)
	// Choose a bounded relevant working set; recent unrelated documents do not
	// consume maintenance model context merely because they were written last.
	query := in.Goal
	for _, o := range in.Observations {
		query += "\n" + o.Text
	}
	terms := navigationTerms(query)
	docs, err := store.ListDocuments(ctx, scope, floor, memory.Injected, 1)
	if err != nil {
		return in, err
	}
	seen := map[string]bool{}
	for _, d := range docs {
		seen[d.Ref] = true
	}
	if strings.TrimSpace(query) != "" {
		reader, ok := r.memory.(recall.MemoryProvider)
		if !ok {
			return in, recall.ErrUnavailable
		}
		query = strings.Join(terms, " ")
		if len(query) > 4000 {
			query = query[:4000]
			for !utf8.ValidString(query) {
				query = query[:len(query)-1]
			}
		}
		if query != "" {
			start := time.UnixMicro(floor).UTC()
			hits, e := reader.Search(ctx, scope, recall.Query{Query: query, Limit: 48, TimeRange: &recall.TimeRange{Start: &start}})
			if e != nil {
				return in, e
			}
			for _, h := range hits {
				if seen[h.SourceRef] {
					continue
				}
				d, e := store.ReadDocument(ctx, scope, floor, h.SourceRef)
				if e == recall.ErrConflict {
					continue
				}
				if e != nil {
					return in, e
				}
				seen[d.Ref] = true
				docs = append(docs, d)
			}
		}
	}
	// Recency is a small fallback, never the corpus boundary for relevance.
	recent, e := store.ListDocuments(ctx, scope, floor, "", 4)
	if e != nil {
		return in, e
	}
	for _, d := range recent {
		if !seen[d.Ref] {
			seen[d.Ref] = true
			docs = append(docs, d)
		}
	}
	sort.SliceStable(docs, func(i, j int) bool {
		score := func(d memory.Document) float64 {
			v := recall.Relevance(terms, d.Title+"\n"+d.Content)
			if d.Layer == memory.Injected {
				v += 1000
			}
			return v
		}
		return score(docs[i]) > score(docs[j])
	})
	chosen := []memory.Document{}
	for _, doc := range docs {
		if len(chosen) >= 12 {
			break
		}
		if doc.Layer == memory.Injected || recall.Relevance(terms, doc.Title+"\n"+doc.Content) > 0 || len(chosen) < 4 || doc.CreatedAt.Equal(at) {
			chosen = append(chosen, doc)
		}
	}
	docs = chosen
	for _, doc := range docs {
		if bytes+len(doc.Content) > totalBudget {
			if doc.Layer == memory.Injected {
				// Never hide the current head and ask the model to create a
				// conflicting replacement. Preserve it when the window is too small.
				return in, llm.ContextLengthExceeded
			}
			continue
		}
		bytes += len(doc.Content)
		in.Documents = append(in.Documents, doc)
	}
	if err = r.dreamOriginalSources(ctx, scope, &in, floor, max(0, totalBudget-bytes)); err != nil {
		return in, err
	}
	return in, nil
}

func (r *Runs) curateDream(ctx context.Context, scope recall.Scope, event string, at time.Time, floor int64) error {
	store, ok := r.memory.(memory.Documents)
	if !ok {
		return recall.ErrUnavailable
	}
	for pass := 0; pass < 3; pass++ {
		var done, finished bool
		err := r.host.db.QueryRowContext(ctx, `SELECT status='completed',finished FROM memory_dream_step WHERE event_id=? AND pass_index=?`, event, pass).Scan(&done, &finished)
		if err != nil && err != sql.ErrNoRows {
			return agent.ErrStorage
		}
		if done {
			if finished {
				return nil
			}
			continue
		}
		in, err := r.dreamInput(ctx, scope, event, at, floor, pass)
		if err != nil {
			return err
		}
		frozen, p, err := r.deriveMemory(ctx, scope, in)
		if err != nil {
			return err
		}
		order, err := memory.DreamWriteOrder(p.Documents)
		if err != nil {
			return err
		}
		err = r.write(ctx, func(tx *sql.Tx) error {
			var current int64
			if e := tx.QueryRowContext(ctx, `SELECT coalesce((SELECT updated_at FROM memory_privacy WHERE agent_id=?),0)`, scope.AgentID).Scan(&current); e != nil {
				return e
			}
			if current != floor {
				return recall.ErrConflict
			}
			sources := []memory.SourceRef{}
			for _, o := range frozen.Observations {
				sources = append(sources, o.Source)
			}
			if len(sources) == 0 {
				for _, doc := range frozen.Documents {
					sources = append(sources, doc.Sources...)
				}
			}
			replacements := map[string]string{}
			for _, i := range order {
				w := p.Documents[i]
				w.Links = append([]string(nil), w.Links...)
				for j, ref := range w.Links {
					if replacement, ok := replacements[ref]; ok {
						w.Links[j] = replacement
					}
				}
				documentSources := sources
				if frozen.Version >= 2 {
					var e error
					documentSources, w.Evidence, e = memory.DreamEvidence(frozen, p.Documents[i])
					if e != nil {
						return e
					}
				}
				// Identical revisions add no knowledge. Check the live leaf before
				// skipping, so a concurrent correction is never treated as unchanged.
				unchanged := false
				if w.Ref != "" && !w.Retire {
					for _, old := range frozen.Documents {
						if old.Ref == w.Ref && old.Revision == w.Revision && old.Title == w.Title && old.Content == w.Content && slices.Equal(old.Links, w.Links) && slices.Equal(old.Evidence, w.Evidence) {
							live, e := store.ReadDocument(ctx, scope, floor, w.Ref)
							if e != nil {
								return e
							}
							unchanged = live.Revision == w.Revision
							break
						}
					}
				}
				if unchanged {
					if _, e := tx.ExecContext(ctx, `INSERT OR IGNORE INTO memory_dream_change VALUES(?,?,?,?,?,'unchanged')`, event, pass, i, w.Ref, w.Layer); e != nil {
						return e
					}
					continue
				}
				key := fmt.Sprintf("dream:%s:%d:%d", event, pass, i)
				ref, e := store.WriteDocument(ctx, scope, floor, key, digest(key), at, documentSources, w)
				if e != nil {
					return e
				}
				operation := "write"
				if w.Retire {
					operation = "retire"
				}
				if _, e := tx.ExecContext(ctx, `INSERT OR IGNORE INTO memory_dream_change VALUES(?,?,?,?,?,?)`, event, pass, i, ref.Ref, w.Layer, operation); e != nil {
					return e
				}
				if w.Ref != "" && !w.Retire {
					replacements[w.Ref] = ref.Ref
				}
			}
			finished := p.Done || pass == 2
			if finished {
				if _, e := tx.ExecContext(ctx, `INSERT INTO memory_dream_coverage(agent_id,through_sequence,next_offset,worker_sequence,worker_offset) VALUES(?,?,?,?,?) ON CONFLICT(agent_id) DO UPDATE SET through_sequence=excluded.through_sequence,next_offset=excluded.next_offset,worker_sequence=excluded.worker_sequence,worker_offset=excluded.worker_offset`, scope.AgentID, frozen.Through, frozen.NextOffset, frozen.WorkerThrough, frozen.WorkerOffset); e != nil {
					return e
				}
			}
			_, e := tx.ExecContext(ctx, `INSERT INTO memory_dream_step VALUES(?,?,'completed',?) ON CONFLICT(event_id,pass_index) DO NOTHING`, event, pass, finished)
			return e
		})
		if err != nil {
			return err
		}
		if p.Done {
			return nil
		}
	}
	return nil
}

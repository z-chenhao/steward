package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/z-chenhao/assistant/internal/agent"
	"github.com/z-chenhao/assistant/internal/conversation"
	"github.com/z-chenhao/assistant/internal/evidence"
	"github.com/z-chenhao/assistant/internal/llm"
	"github.com/z-chenhao/assistant/internal/memory"
	"github.com/z-chenhao/assistant/internal/recall"
	"github.com/z-chenhao/assistant/internal/tool"
)

func documentOperation(op string) bool {
	return op == "list" || op == "read" || op == "write" || op == "retire" || op == "dream"
}

// The state transaction holds the privacy fence while a separate adapter commits
// its domain receipt. A crash between those commits reuses the same receipt.
func (r *Runs) manageDocument(ctx context.Context, tx *sql.Tx, agentID, runID, key string, m memory.Mutation) (tool.Result, error) {
	store, ok := r.memory.(memory.Documents)
	if !ok || r.vault.Ref == "" {
		return tool.Failure("PROVIDER_UNAVAILABLE"), nil
	}
	var floor int64
	if err := tx.QueryRowContext(ctx, `SELECT coalesce((SELECT updated_at FROM memory_privacy WHERE agent_id=?),0)`, agentID).Scan(&floor); err != nil {
		return tool.Result{}, err
	}
	scope := recall.Scope{UserID: r.host.OwnerID(), AgentID: agentID}
	var value any
	var err error
	switch m.Operation {
	case "list":
		limit := m.Limit
		if limit == 0 {
			limit = 20
		}
		docs, next, e := store.ListDocumentPage(ctx, scope, floor, m.Layer, limit, m.Cursor)
		err = e
		value = map[string]any{"documents": docs, "next_cursor": next}
	case "read":
		if m.Layer == memory.Transcripts {
			value, err = r.readMemoryTranscript(ctx, tx, agentID, floor, m)
			break
		}
		var doc memory.Document
		doc, err = store.ReadDocument(ctx, scope, floor, m.MemoryRef)
		if err == nil {
			page := evidence.Page{}
			err = evidence.Window(&page, doc.Content, evidence.Request{Offset: m.Offset, Limit: m.Limit})
			if err != nil {
				return tool.Failure("INVALID_ARGUMENT"), nil
			}
			doc.Content = page.Text
			if len(doc.Sources) > 20 {
				doc.Sources = doc.Sources[:20]
			}
			value = struct {
				memory.Document
				Next  *int64 `json:"next_offset,omitempty"`
				Bytes int64  `json:"total_bytes"`
			}{doc, page.NextOffset, page.TotalBytes}
		}

	case "dream":
		var prior string
		e := tx.QueryRowContext(ctx, `SELECT payload FROM memory_mutation WHERE operation='observe' AND source_type='dream' AND source_ref=?`, key).Scan(&prior)
		if e == nil {
			plain := ""
			if prior != "" {
				plain, err = r.vault.Open(ctx, prior, "dream-goal:"+key)
				if err != nil {
					return tool.Failure("PROVIDER_UNAVAILABLE"), nil
				}
			}
			if plain != m.Content {
				return tool.Failure("CONFLICT"), nil
			}
		} else if e != sql.ErrNoRows {
			return tool.Result{}, e
		}
		payload := ""
		if m.Content != "" {
			payload, err = r.vault.Seal(ctx, m.Content, "dream-goal:"+key)
			if err != nil {
				return tool.Failure("PROVIDER_UNAVAILABLE"), nil
			}
		}
		var receipt string
		receipt, err = r.queueMemory(ctx, tx, agentID, "observe", "dream", key, payload)
		if err == nil {
			value = map[string]any{"accepted": true, "operation": "dream", "receipt": receipt}
		}

	case "write", "retire":
		sources := []memory.SourceRef{{Type: "feedback", Ref: "feedback:" + key}}
		// Provenance comes from the consumed input, never model-supplied IDs.
		var source string
		if runID != "" {
			e := tx.QueryRowContext(ctx, `SELECT i.message_id FROM main_input i JOIN run r USING(run_id) WHERE i.run_id=? AND i.sequence<=coalesce(json_extract(r.checkpoint_json,'$.input_sequence'),0) ORDER BY i.sequence DESC LIMIT 1`, runID).Scan(&source)
			if e == nil {
				sources = append(sources, memory.SourceRef{Type: "conversation", Ref: "conversation:" + source})
			} else if e != sql.ErrNoRows {
				return tool.Result{}, e
			}
		}
		d, e := store.WriteDocument(ctx, scope, floor, "document:"+key, key+"-document", time.Now().UTC(), sources, m.DocumentWrite())
		err = e
		value = map[string]any{"applied": err == nil, "ref": tool.ObjectID(d.Ref), "layer": d.Layer, "revision": d.Revision}
	}
	if errors.Is(err, recall.ErrConflict) {
		if m.Operation == "read" {
			return tool.Failure("NOT_FOUND"), nil
		}
		return tool.Failure("CONFLICT"), nil
	}
	if errors.Is(err, recall.ErrInvalid) || errors.Is(err, memory.ErrInspectionInvalid) {
		return tool.Failure("INVALID_ARGUMENT"), nil
	}
	if err != nil {
		return tool.Failure("PROVIDER_UNAVAILABLE"), nil
	}
	if m.Operation == "read" && runID != "" {
		ref := m.MemoryRef
		kind := "memory"
		if m.Layer == memory.Transcripts {
			kind = "conversation"
		}
		if !strings.HasPrefix(ref, kind+":") {
			ref = kind + ":" + ref
		}
		if e := recordMemoryAccess(ctx, tx, runID, key, "read", []string{ref}); e != nil {
			return tool.Result{}, e
		}
	}
	text, _ := encoded(value)
	if m.Operation == "read" || m.Operation == "list" {
		binding := "memory-read:" + runID + ":" + key
		plain := tool.ModelObjectIDs(text)
		sealed, e := r.vault.Seal(ctx, plain, binding)
		if e != nil {
			return tool.Failure("PROVIDER_UNAVAILABLE"), nil
		}
		text, _ = encoded(map[string]any{"memory_text": llm.MemoryText{Sealed: sealed, Binding: binding, Ref: tool.ObjectID(m.MemoryRef), EncodedBytes: llm.MemoryEncodedBytes(plain)}})
	}
	return tool.Result{Text: text}, nil
}

func (r *Runs) memoryResolver(runID string) func(context.Context, llm.MemoryText) (string, error) {
	return func(ctx context.Context, v llm.MemoryText) (string, error) {
		if v.CodeMode {
			if v.Binding != "memory-code:"+runID {
				return "", agent.ErrClaimLost
			}
			text, ok, e := llm.ResolveCodeMemory(v.Sealed, func(m llm.MemoryText) (string, error) { return r.memoryResolver(runID)(ctx, m) })
			if e != nil {
				return "", e
			}
			if !ok {
				return "", llm.ModelIncompatible
			}
			return text, nil
		}
		if v.Binding != "memory-injected:"+runID && !strings.HasPrefix(v.Binding, "memory-read:"+runID+":") {
			return "", agent.ErrClaimLost
		}
		return r.vault.Open(ctx, v.Sealed, v.Binding)
	}
}

func memoryReadProjection(messages []llm.Message) []llm.Message {
	for i, m := range messages {
		if m.Role != "tool" {
			continue
		}
		var envelope struct {
			Memory *llm.MemoryText `json:"memory_text"`
		}
		if json.Unmarshal([]byte(m.Text), &envelope) == nil && envelope.Memory != nil {
			messages[i].Memory = envelope.Memory
			messages[i].Text = ""
		}
	}
	return messages
}

// This is the same authorized Conversation corpus as memory_search, with exact
// bounded reads. It does not grant Main's Task/Artifact/GitHub evidence surface.
func (r *Runs) readMemoryTranscript(ctx context.Context, tx *sql.Tx, agentID string, floor int64, q memory.Mutation) (any, error) {
	if strings.HasPrefix(q.MemoryRef, "run:") {
		text, at, author, e := r.workerMemoryOriginal(ctx, tx, agentID, q.MemoryRef, floor)
		if e != nil {
			return nil, e
		}
		page := evidence.Page{}
		if e = evidence.Window(&page, text, evidence.Request{Offset: q.Offset, Limit: q.Limit}); e != nil {
			return nil, recall.ErrInvalid
		}
		return map[string]any{"ref": q.MemoryRef, "layer": memory.Transcripts, "author": author, "occurred_at": at, "text": page.Text, "offset": q.Offset, "total_bytes": page.TotalBytes, "next_offset": page.NextOffset}, nil
	}
	id := strings.TrimPrefix(q.MemoryRef, "conversation:")
	var m conversation.Message
	scope := recall.Scope{UserID: r.host.OwnerID(), AgentID: agentID}
	err := conversation.ErrConflict
	if reader, ok := r.history.(interface {
		GetScoped(context.Context, recall.Scope, string) (conversation.Message, error)
	}); ok {
		m, err = reader.GetScoped(ctx, scope, id)
	}
	if errors.Is(err, conversation.ErrConflict) {
		if reader, ok := r.history.(interface {
			EvidenceSourceVisible(context.Context, recall.Scope, string, string, int64) (bool, error)
		}); ok {
			visible, e := reader.EvidenceSourceVisible(ctx, scope, agentID, id, floor)
			if e == nil && !visible {
				return nil, recall.ErrConflict
			}
			if e != nil && !errors.Is(e, conversation.ErrConflict) {
				return nil, recall.ErrUnavailable
			}
		}
		var raw string
		err = tx.QueryRowContext(ctx, `SELECT message_json FROM conversation_intent WHERE message_id=? AND json_extract(message_json,'$.conversation_id')=?`, id, agentID).Scan(&raw)
		if err == nil {
			err = json.Unmarshal([]byte(raw), &m)
		}
	}
	if err != nil {
		if errors.Is(err, conversation.ErrConflict) || err == sql.ErrNoRows {
			return nil, recall.ErrConflict
		}
		return nil, recall.ErrUnavailable
	}
	if m.CreatedAt.UnixMicro() <= floor {
		return nil, recall.ErrConflict
	}
	parts := []string{}
	for _, p := range m.Content {
		if p.Type == "text" {
			parts = append(parts, p.Text)
		}
	}
	body := strings.Join(parts, "\n")
	if memory.HasSecret(body) {
		return nil, recall.ErrConflict
	}
	page := evidence.Page{}
	if err = evidence.Window(&page, body, evidence.Request{Offset: q.Offset, Limit: q.Limit}); err != nil {
		return nil, recall.ErrInvalid
	}
	return struct {
		Ref    string    `json:"ref"`
		Layer  string    `json:"layer"`
		Author string    `json:"author"`
		At     time.Time `json:"occurred_at"`
		Text   string    `json:"text"`
		Offset int64     `json:"offset"`
		Next   *int64    `json:"next_offset,omitempty"`
		Bytes  int64     `json:"total_bytes"`
	}{q.MemoryRef, memory.Transcripts, m.Role, m.CreatedAt, page.Text, q.Offset, page.NextOffset, page.TotalBytes}, nil
}

func (r *Runs) codeMemoryProjection(ctx context.Context, run agent.Run, messages []llm.Message) ([]llm.Message, error) {
	enabled := false
	for _, s := range run.Tools {
		enabled = enabled || s.Name == memory.ManageName && s.Version >= 4
	}
	if !enabled {
		return messages, nil
	}
	calls := map[string]bool{}
	out := append([]llm.Message(nil), messages...)
	for i, m := range out {
		for _, c := range m.ToolCalls {
			if c.Name == "builtin__codemode_execute" {
				calls[c.ID] = true
			}
		}
		if m.Role != "tool" || m.Memory != nil || !calls[m.ToolCallID] {
			continue
		}
		plain, changed, e := llm.ResolveCodeMemory(m.Text, func(v llm.MemoryText) (string, error) { return r.memoryResolver(run.ID)(ctx, v) })
		if e != nil {
			return nil, e
		}
		if !changed {
			continue
		}
		out[i].Memory = &llm.MemoryText{CodeMode: true, Sealed: m.Text, Binding: "memory-code:" + run.ID, EncodedBytes: llm.MemoryEncodedBytes(plain)}
		out[i].Text = ""
	}
	return out, nil
}

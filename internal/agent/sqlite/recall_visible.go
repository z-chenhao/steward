package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"github.com/z-chenhao/assistant/internal/agent"
	contextbuilder "github.com/z-chenhao/assistant/internal/context"
	"github.com/z-chenhao/assistant/internal/llm"
	"github.com/z-chenhao/assistant/internal/memory"
	"github.com/z-chenhao/assistant/internal/recall"
	"github.com/z-chenhao/assistant/internal/tool"
	"strings"
)

// Only complete results actually present in the current working set can be
// excluded. A child receipt hidden by a failed CodeMode frame is not visible.
func (r *Runs) visibleRecallRefs(ctx context.Context, run agent.Run, messages []llm.Message) ([]string, error) {
	visible := map[string]map[string]bool{}
	for _, m := range messages {
		if m.Role != "tool" {
			continue
		}
		signatures := map[string]bool{}
		for _, h := range recallHitsInMessage(m.Text) {
			if completeRecallHit(h) {
				signatures[recallHitSignature(h)] = true
			}
		}
		if len(signatures) > 0 {
			visible[m.ToolCallID] = signatures
		}
	}
	if len(visible) == 0 {
		return nil, nil
	}
	rows, err := r.host.db.QueryContext(ctx, `SELECT coalesce(t.provider_call_id,''),coalesce(p.provider_call_id,''),t.result_json FROM tool_invocation t LEFT JOIN tool_invocation p ON p.invocation_id=t.parent_invocation_id WHERE t.run_id=? AND t.tool_name=? AND t.status='succeeded'`, run.ID, recall.ToolName)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []string{}
	seen := map[string]bool{}
	for rows.Next() {
		var call, parent, raw string
		if err = rows.Scan(&call, &parent, &raw); err != nil {
			return nil, err
		}
		var receipt tool.Result
		if json.Unmarshal([]byte(raw), &receipt) != nil {
			continue
		}
		for _, h := range recallHitsInMessage(receipt.Text) {
			signature := recallHitSignature(h)
			if completeRecallHit(h) && (visible[call][signature] || visible[parent][signature]) && !seen[h.SourceRef] {
				seen[h.SourceRef] = true
				out = append(out, h.SourceRef)
			}
		}
	}
	return out, rows.Err()
}
func completeRecallHit(h recall.Hit) bool {
	return h.SourceRef != "" && (h.SourceType == "memory" || h.SourceType == "conversation") && h.Content != "" && h.Offset == 0 && (h.TotalBytes == 0 || h.TotalBytes == len(h.Content))
}
func recallHitSignature(h recall.Hit) string {
	h.SourceRef = tool.ObjectID(h.SourceRef)
	raw, _ := encoded(struct {
		Ref, Kind, Text string
		Offset, Bytes   int
	}{h.SourceRef, h.SourceType, h.Content, h.Offset, h.TotalBytes})
	return digest(raw)
}
func recallHitsInMessage(raw string) []recall.Hit {
	var hits []recall.Hit
	if json.Unmarshal([]byte(raw), &hits) == nil {
		for _, h := range hits {
			if h.SourceRef != "" && (h.SourceType == "memory" || h.SourceType == "conversation") {
				return hits
			}
		}
		if len(hits) == 0 {
			return nil
		}
	}
	var blocks []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}
	if json.Unmarshal([]byte(raw), &blocks) != nil {
		return nil
	}
	for _, b := range blocks {
		if b.Type != "text" {
			continue
		}
		var part []recall.Hit
		if json.Unmarshal([]byte(b.Text), &part) == nil {
			hits = append(hits, part...)
		}
	}
	return hits
}

// One visibility predicate is shared by automatic recall and explicit search.
// This is not a cross-Run cache: revocation and compaction remove eligibility.
func (r *Runs) evidenceReuse(ctx context.Context, runID string) (bool, error) {
	var raw string
	if e := r.host.db.QueryRowContext(ctx, `SELECT policy_json FROM run_context_policy WHERE run_id=?`, runID).Scan(&raw); e != nil {
		return false, e
	}
	var p contextbuilder.Policy
	if e := json.Unmarshal([]byte(raw), &p); e != nil {
		return false, e
	}
	return p.EvidenceReuse, nil
}

func (r *Runs) visibleEvidenceRefs(ctx context.Context, run agent.Run, messages []llm.Message) ([]string, error) {
	visible := map[string]map[string]bool{}
	resolve := r.memoryResolver(run.ID)
	for _, m := range messages {
		if m.Role != "tool" {
			continue
		}
		raw := m.Text
		if m.Memory != nil {
			var e error
			raw, e = resolve(ctx, *m.Memory)
			if e != nil {
				return nil, e
			}
		}
		hits, e := evidenceHits(raw, func(v llm.MemoryText) (string, error) { return resolve(ctx, v) })
		if e != nil {
			return nil, e
		}
		for _, h := range hits {
			if completeRecallHit(h) {
				if visible[m.ToolCallID] == nil {
					visible[m.ToolCallID] = map[string]bool{}
				}
				visible[m.ToolCallID][recallHitSignature(h)] = true
			}
		}
	}
	out := []string{}
	seen := map[string]bool{}
	add := func(h recall.Hit) {
		ref := h.SourceRef
		if !strings.HasPrefix(ref, h.SourceType+":") {
			ref = h.SourceType + ":" + ref
		}
		if !seen[ref] {
			seen[ref] = true
			out = append(out, ref)
		}
	}
	if len(visible) > 0 {
		calls := []string{}
		for call := range visible {
			calls = append(calls, call)
		}
		callJSON, _ := json.Marshal(calls)
		rows, e := r.host.db.QueryContext(ctx, `SELECT coalesce(t.provider_call_id,''),coalesce(p.provider_call_id,''),t.result_json FROM tool_invocation t LEFT JOIN tool_invocation p ON p.invocation_id=t.parent_invocation_id WHERE t.run_id=? AND t.tool_name IN (?,?) AND t.status='succeeded' AND (t.provider_call_id IN (SELECT value FROM json_each(?)) OR p.provider_call_id IN (SELECT value FROM json_each(?)))`, run.ID, recall.ToolName, memory.ManageName, string(callJSON), string(callJSON))
		if e != nil {
			return nil, e
		}
		receipts := []struct{ call, parent, raw string }{}
		for rows.Next() {
			var x struct{ call, parent, raw string }
			if e = rows.Scan(&x.call, &x.parent, &x.raw); e != nil {
				rows.Close()
				return nil, e
			}
			receipts = append(receipts, x)
		}
		e = rows.Err()
		rows.Close()
		if e != nil {
			return nil, e
		}
		for _, x := range receipts {
			var receipt tool.Result
			if json.Unmarshal([]byte(x.raw), &receipt) != nil {
				continue
			}
			hits, e := evidenceHits(receipt.Text, func(v llm.MemoryText) (string, error) { return resolve(ctx, v) })
			if e != nil {
				return nil, e
			}
			for _, h := range hits {
				sig := recallHitSignature(h)
				if completeRecallHit(h) && (visible[x.call][sig] || visible[x.parent][sig]) {
					add(h)
				}
			}
		}
	}
	// Automatic evidence has no public source keys. Its exact Host-rendered block
	// and frozen result must both be present; a merely historical row is not seen.
	// Prepared requests contain inline automatic evidence that checkpoints do
	// not. Consult only the latest working projection for this logical input.
	automaticMessages := append([]llm.Message(nil), messages...)
	if input := recallInputRef(messages); input != "" {
		var prepared string
		e := r.host.db.QueryRowContext(ctx, `SELECT messages_json FROM context_compaction WHERE run_id=? AND step_index<=? AND json_extract(provenance_json,'$.recall_input_ref')=? ORDER BY step_index DESC LIMIT 1`, run.ID, run.Checkpoint.Step, input).Scan(&prepared)
		if e == nil {
			var latest []llm.Message
			if json.Unmarshal([]byte(prepared), &latest) != nil {
				return nil, llm.ModelIncompatible
			}
			automaticMessages = append(automaticMessages, latest...)
		} else if e != sql.ErrNoRows {
			return nil, e
		}
	}
	rows, e := r.host.db.QueryContext(ctx, `SELECT hits_json FROM run_recall WHERE run_id=?`, run.ID)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	for rows.Next() {
		var raw string
		if e = rows.Scan(&raw); e != nil {
			return nil, e
		}
		var hits []recall.Hit
		if json.Unmarshal([]byte(raw), &hits) != nil {
			continue
		}
		if len(hits) == 0 {
			continue
		}
		block := renderInputRecall(hits)
		present := false
		for _, m := range automaticMessages {
			present = present || strings.Contains(m.Text, block) || strings.Contains(m.Text, renderSeparateRecall(hits))
		}
		if present {
			for _, h := range hits {
				if completeRecallHit(h) {
					add(h)
				}
			}
		}
	}
	return out, rows.Err()
}

func evidenceHits(raw string, resolve func(llm.MemoryText) (string, error)) ([]recall.Hit, error) {
	return evidenceHitsDepth(raw, resolve, 0)
}

// Reusing an exact read requires its WHOLE metadata-bearing page, not only a
// matching search excerpt. In particular, search has no CAS revision or links.
func (r *Runs) visibleDocumentReadRefs(ctx context.Context, run agent.Run, messages []llm.Message) ([]string, error) {
	resolve := r.memoryResolver(run.ID)
	visible := map[string][]string{}
	for _, m := range messages {
		if m.Role != "tool" {
			continue
		}
		raw := m.Text
		if m.Memory != nil {
			var e error
			raw, e = resolve(ctx, *m.Memory)
			if e != nil {
				return nil, e
			}
		}
		// Exact native envelopes and known CodeMode blocks only. Comparison below
		// is with the accepted sealed page, including revision and all metadata.
		var envelope struct {
			Memory *llm.MemoryText `json:"memory_text"`
		}
		if json.Unmarshal([]byte(raw), &envelope) == nil && envelope.Memory != nil {
			var e error
			raw, e = resolve(ctx, *envelope.Memory)
			if e != nil {
				return nil, e
			}
		}
		visible[m.ToolCallID] = append(visible[m.ToolCallID], raw)
		expanded, _, e := llm.ResolveCodeMemory(raw, func(v llm.MemoryText) (string, error) { return resolve(ctx, v) })
		if e != nil {
			return nil, e
		}
		var blocks []struct{ Type, Text string }
		if json.Unmarshal([]byte(expanded), &blocks) == nil {
			for _, b := range blocks {
				if b.Type == "text" {
					visible[m.ToolCallID] = append(visible[m.ToolCallID], b.Text)
				}
			}
		}
	}
	if len(visible) == 0 {
		return nil, nil
	}
	calls := []string{}
	for call := range visible {
		calls = append(calls, call)
	}
	keys, _ := json.Marshal(calls)
	rows, e := r.host.db.QueryContext(ctx, `SELECT coalesce(t.provider_call_id,''),coalesce(p.provider_call_id,''),t.arguments_json,t.result_json FROM tool_invocation t LEFT JOIN tool_invocation p ON p.invocation_id=t.parent_invocation_id WHERE t.run_id=? AND t.tool_name=? AND t.status='succeeded' AND (t.provider_call_id IN(SELECT value FROM json_each(?)) OR p.provider_call_id IN(SELECT value FROM json_each(?)))`, run.ID, memory.ManageName, string(keys), string(keys))
	if e != nil {
		return nil, e
	}
	type receipt struct{ call, parent, args, result string }
	receipts := []receipt{}
	for rows.Next() {
		var x receipt
		if e = rows.Scan(&x.call, &x.parent, &x.args, &x.result); e != nil {
			rows.Close()
			return nil, e
		}
		receipts = append(receipts, x)
	}
	e = rows.Err()
	rows.Close()
	if e != nil {
		return nil, e
	}
	out := []string{}
	seen := map[string]bool{}
	for _, x := range receipts {
		m, ok := memory.Parse(x.args)
		if !ok || m.Operation != "read" || m.Layer == memory.Transcripts || m.Offset != 0 {
			continue
		}
		var result tool.Result
		if json.Unmarshal([]byte(x.result), &result) != nil {
			continue
		}
		var envelope struct {
			Memory *llm.MemoryText `json:"memory_text"`
		}
		if json.Unmarshal([]byte(result.Text), &envelope) != nil || envelope.Memory == nil {
			continue
		}
		plain, e := resolve(ctx, *envelope.Memory)
		if e != nil {
			return nil, e
		}
		var page struct {
			Content  string
			Revision int64
			Bytes    int  `json:"total_bytes"`
			Next     *int `json:"next_offset"`
		}
		if json.Unmarshal([]byte(plain), &page) != nil || page.Revision < 1 || page.Content == "" || page.Bytes != len(page.Content) || page.Next != nil {
			continue
		}
		for _, segment := range append(visible[x.call], visible[x.parent]...) {
			if strings.Contains(segment, plain) {
				ref := m.MemoryRef
				if !strings.HasPrefix(ref, "memory:") {
					ref = "memory:" + ref
				}
				if !seen[ref] {
					seen[ref] = true
					out = append(out, ref)
				}
				break
			}
		}
	}
	return out, nil
}
func evidenceHitsDepth(raw string, resolve func(llm.MemoryText) (string, error), depth int) ([]recall.Hit, error) {
	if depth > 8 {
		return nil, nil
	}
	var envelope struct {
		Memory *llm.MemoryText `json:"memory_text"`
	}
	if json.Unmarshal([]byte(raw), &envelope) == nil && envelope.Memory != nil {
		plain, e := resolve(*envelope.Memory)
		if e != nil {
			return nil, e
		}
		if llm.MemoryEncodedBytes(plain) != envelope.Memory.EncodedBytes {
			return nil, llm.ModelIncompatible
		}
		return evidenceHitsDepth(plain, resolve, depth+1)
	}
	// Accept exact-read pages, not document listings or arbitrary nested objects.
	var page struct {
		Ref, Layer, Content, Text string
		Offset                    int
		Bytes                     *int `json:"total_bytes"`
		Next                      *int `json:"next_offset"`
	}
	if json.Unmarshal([]byte(raw), &page) == nil && page.Ref != "" && page.Bytes != nil && page.Next == nil {
		kind := "memory"
		content := page.Content
		if page.Layer == memory.Transcripts {
			kind = "conversation"
			content = page.Text
		}
		return []recall.Hit{{SourceRef: page.Ref, SourceType: kind, Content: content, Offset: page.Offset, TotalBytes: *page.Bytes}}, nil
	}
	var items []json.RawMessage
	if json.Unmarshal([]byte(raw), &items) != nil {
		return nil, nil
	}
	out := []recall.Hit{}
	for _, item := range items {
		var hit recall.Hit
		if json.Unmarshal(item, &hit) == nil && hit.SourceRef != "" && (hit.SourceType == "memory" || hit.SourceType == "conversation") {
			out = append(out, hit)
			continue
		}
		var block struct{ Type, Text string }
		body := string(item)
		if json.Unmarshal(item, &block) == nil && block.Type == "text" {
			body = block.Text
		}
		hits, e := evidenceHitsDepth(body, resolve, depth+1)
		if e != nil {
			return nil, e
		}
		out = append(out, hits...)
	}
	return out, nil
}

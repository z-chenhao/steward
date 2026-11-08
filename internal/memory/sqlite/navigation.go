package sqlite

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/z-chenhao/assistant/internal/memory"
	"github.com/z-chenhao/assistant/internal/recall"
)

// Both bounded queries select metadata, never m.content. Close each SQL view
// before accessing SecretStore, then reject a concurrent visibility change.
func (s *Store) ReadNavigation(ctx context.Context, scope recall.Scope, floor int64, refs []string) ([]memory.NavigationEntry, error) {
	if len(refs) > 32 || scope.UserID == "" || scope.AgentID == "" {
		return nil, recall.ErrInvalid
	}
	ids := []string{}
	seenIDs := map[string]bool{}
	for _, ref := range refs {
		if !memory.ValidInspectionRef(ref) {
			return nil, recall.ErrInvalid
		}
		id := strings.TrimPrefix(ref, "memory:")
		if !seenIDs[id] {
			seenIDs[id] = true
			ids = append(ids, id)
		}
	}
	raw, _ := json.Marshal(ids)
	query := `SELECT m.memory_id,coalesce((SELECT metadata FROM memory_document x WHERE x.memory_id=m.memory_id),'')` + currentMemoryRows + ` AND m.created_at>? AND m.memory_id IN (SELECT value FROM json_each(?)) ORDER BY m.memory_id`
	load := func() (map[string]string, error) {
		rows, err := s.db.QueryContext(ctx, query, scope.UserID, scope.UserID, scope.AgentID, floor, string(raw))
		if err != nil {
			return nil, recall.ErrUnavailable
		}
		defer rows.Close()
		out := map[string]string{}
		for rows.Next() {
			var id, meta string
			if rows.Scan(&id, &meta) != nil {
				return nil, recall.ErrUnavailable
			}
			out[id] = meta
		}
		if rows.Err() != nil {
			return nil, recall.ErrUnavailable
		}
		return out, nil
	}
	before, err := load()
	if err != nil {
		return nil, err
	}
	entries := map[string]memory.NavigationEntry{}
	for id, sealed := range before {
		meta := documentMetadata{Title: "Memory note"}
		if sealed != "" {
			plain, e := s.vault.Open(ctx, sealed, "document:"+id)
			if e != nil {
				return nil, e
			}
			if json.Unmarshal([]byte(plain), &meta) != nil {
				return nil, recall.ErrUnavailable
			}
		}
		terms := meta.NavigationTerms
		if len(terms) == 0 { // Existing immutable metadata remains untouched.
			text := meta.Title
			for _, e := range meta.Evidence {
				text += "\n" + e.Quote
			}
			terms = navigationTerms(text)
		}
		entries[id] = memory.NavigationEntry{Ref: "memory:" + id, Title: meta.Title, Terms: terms}
	}
	after, err := load()
	if err != nil {
		return nil, err
	}
	out := []memory.NavigationEntry{}
	for _, id := range ids {
		meta, exists := before[id]
		if current, ok := after[id]; exists && ok && current == meta {
			out = append(out, entries[id])
		}
	}
	return out, nil
}

func navigationTerms(text string) []string {
	// Index only a bounded vocabulary, not another copy of the document.
	runes := []rune(text)
	if len(runes) > 4096 {
		runes = runes[:4096]
	}
	seen := map[string]bool{}
	bytes := 0
	out := []string{}
	for len(runes) > 0 && len(out) < 96 {
		n := min(200, len(runes))
		for _, term := range recall.Terms(string(runes[:n])) {
			if !seen[term] && len(out) < 96 && bytes+len(term) <= 1024 {
				bytes += len(term)
				seen[term] = true
				out = append(out, term)
			}
		}
		runes = runes[n:]
	}
	return out
}

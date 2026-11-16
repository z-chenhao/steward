package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"unicode/utf8"

	"github.com/z-chenhao/assistant/internal/agent"
	"github.com/z-chenhao/assistant/internal/plugin/tooldiscovery/contract"
	"github.com/z-chenhao/assistant/internal/rsi"
	"github.com/z-chenhao/assistant/internal/tool"
	"github.com/z-chenhao/assistant/pkg/rsiplugin"
)

// The candidate only receives already authorized, frozen, unactivated metadata.
// Exposure and CodeModeOnly remain Host authority, never plugin output fields.
func toolDiscoveryPool(specs []tool.Spec, loaded []string) ([]tool.Spec, []contract.Candidate) {
	active := map[string]bool{}
	for _, name := range loaded {
		active[name] = true
	}
	pool := []tool.Spec{}
	facts := []contract.Candidate{}
	for _, s := range specs {
		if !s.Searchable() || active[s.Name] {
			continue
		}
		pool = append(pool, s)
		facts = append(facts, contract.Candidate{Name: s.Name, Description: s.Description, Schema: s.InputSchema, Namespace: s.Provider, NamespaceDescription: s.NamespaceDescription})
	}
	return pool, facts
}

func materializeToolDiscovery(pool []tool.Spec, plan contract.Plan, limit int) (tool.SearchResult, error) {
	out := tool.SearchResult{Loaded: []string{}, Tools: []tool.ToolMatch{}}
	if limit < 1 || limit > tool.MaxSearchLimit || len(plan.Entries) > limit {
		return out, rsi.ErrInvalid
	}
	seen := map[int]bool{}
	for _, entry := range plan.Entries {
		if entry.Index < 0 || entry.Index >= len(pool) || seen[entry.Index] {
			return out, rsi.ErrInvalid
		}
		seen[entry.Index] = true
		s := pool[entry.Index]
		if entry.Offset < 0 || entry.Offset > len(s.Description) || entry.Length < 0 || entry.Length > len(s.Description)-entry.Offset {
			return out, rsi.ErrInvalid
		}
		text := s.Description[entry.Offset : entry.Offset+entry.Length]
		if !utf8.ValidString(text) {
			return out, rsi.ErrInvalid
		}
		if entry.Ellipsis {
			text += "…"
		}
		out.Tools = append(out.Tools, tool.ToolMatch{Name: s.Name, Description: text})
		if !s.CodeModeOnly {
			out.Loaded = append(out.Loaded, s.Name)
		}
	}
	encoded, err := json.Marshal(out)
	if err != nil || len(encoded) > 1<<20 {
		return tool.SearchResult{}, rsi.ErrInvalid
	}
	return out, nil
}

// The revision is the original Run binding. Stream only this exact metadata,
// with the original claim/invocation or VM fence on every callback and result.
func (t *tools) managedToolDiscovery(ctx context.Context, specs []tool.Spec, loaded []string, query string, limit int, domainFence func(context.Context, *sql.Tx) error) (tool.SearchResult, error) {
	if t.runs.plugins == nil {
		return tool.SearchResult{}, rsi.ErrNotFound
	}
	pool, candidates := toolDiscoveryPool(specs, loaded)
	h := t.runs.pluginHost(t.claim)
	store := t.runs.host.Plugins()
	store.Fence = domainFence
	h.Store = store
	revision, err := h.Store.Binding(ctx, t.claim.RunID())
	if err != nil {
		return tool.SearchResult{}, err
	}
	if revision == "" {
		return tool.SearchResult{}, rsi.ErrNotFound
	}
	input, _ := json.Marshal(contract.Request{Count: len(candidates), Limit: limit})
	raw, err := h.ReadServiceStream(ctx, revision, contract.Contract, input, rsi.InputData{ID: contract.InputID, Data: contract.Encode(contract.Facts{Query: query, Candidates: candidates})})
	if err != nil {
		return tool.SearchResult{}, err
	}
	var plan contract.Plan
	if rsiplugin.DecodeResult(raw, &plan) != nil {
		return tool.SearchResult{}, rsi.ErrInvalid
	}
	out, err := materializeToolDiscovery(pool, plan, limit)
	if err != nil {
		return tool.SearchResult{}, err
	}
	if err := t.runs.evidenceReadTx(ctx, func(tx *sql.Tx) error { return domainFence(ctx, tx) }); err != nil {
		return tool.SearchResult{}, err
	}
	return out, nil
}
func toolDiscoveryUnavailable(err error) bool {
	return err != nil && !errors.Is(err, agent.ErrClaimLost) && !errors.Is(err, agent.ErrStorage) && !errors.Is(err, context.Canceled) && !errors.Is(err, context.DeadlineExceeded)
}

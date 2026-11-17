package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"unicode/utf8"

	"github.com/z-chenhao/assistant/internal/artifact"
	"github.com/z-chenhao/assistant/internal/evidence"
	"github.com/z-chenhao/assistant/internal/plugin/evidencereader/contract"
	"github.com/z-chenhao/assistant/internal/rsi"
	"github.com/z-chenhao/assistant/pkg/rsiplugin"
)

func (r *Runs) evidencePagePolicy(ctx context.Context, a evidenceAccess, kind string, input any) (json.RawMessage, error) {
	if r.plugins == nil || a.pageGraph == "" {
		return nil, evidence.ErrUnavailable
	}
	h := *r.plugins
	store := r.host.Plugins()
	store.Fence = func(ctx context.Context, tx *sql.Tx) error { return r.evidenceFence(ctx, tx, a) }
	h.Store = store
	raw, err := json.Marshal(input)
	if err != nil {
		return nil, err
	}
	// Pagination is a pure read. The accepted Tool receipt freezes the page;
	// an additional RSI write would make admitted reads compete for WAL writers.
	out, err := h.ReadService(ctx, a.pageGraph, kind, raw, nil)
	if errors.Is(err, rsi.ErrNotFound) || errors.Is(err, rsi.ErrRevoked) {
		return nil, evidence.ErrUnavailable
	}
	return out, err
}

// Host alone slices original bytes and constructs progress/cursors. The policy
// sees only relative UTF-8 and paragraph boundaries, never source content/IDs.
func (r *Runs) evidenceWindow(ctx context.Context, a evidenceAccess, p *evidence.Page, text string, q evidence.Request) error {
	if !a.pagePlugin {
		return evidence.Window(p, text, q)
	}
	if !utf8.ValidString(text) || q.Offset < 0 || q.Offset > int64(len(text)) || q.Offset < int64(len(text)) && !utf8.RuneStart(text[q.Offset]) {
		return evidence.ErrInvalid
	}
	limit := q.Limit
	if limit == 0 {
		limit = 4096
	}
	end := min(int64(len(text)), q.Offset+int64(limit))
	for end > q.Offset && end < int64(len(text)) && !utf8.RuneStart(text[end]) {
		end--
	}
	return r.evidenceTextChunk(ctx, a, p, text[q.Offset:end], int64(len(text)), q)
}

func (r *Runs) evidenceTextChunk(ctx context.Context, a evidenceAccess, p *evidence.Page, chunk string, total int64, q evidence.Request) error {
	f := contract.TextFacts{Bytes: len(chunk), Boundaries: []int{0}, Breaks: []int{}}
	for i, c := range chunk {
		end := i + utf8.RuneLen(c)
		f.Boundaries = append(f.Boundaries, end)
		if c == '\n' {
			f.Breaks = append(f.Breaks, end)
		}
	}
	raw, err := r.evidencePagePolicy(ctx, a, contract.TextContract, f)
	if err != nil {
		return err
	}
	var plan contract.TextPlan
	if err = rsiplugin.DecodeResult(raw, &plan); err != nil {
		return err
	}
	if err = validateEvidenceTextPlan(chunk, total, q, plan); err != nil {
		return err
	}
	p.Text = chunk[:plan.Bytes]
	p.Bytes = plan.Bytes
	p.Offset = q.Offset
	p.TotalBytes = total
	p.NextOffset = nil
	next := q.Offset + int64(plan.Bytes)
	if next < total {
		p.NextOffset = &next
	}
	p.PageRevision = a.pageGraph
	return nil
}

func validateEvidenceTextPlan(chunk string, total int64, q evidence.Request, p contract.TextPlan) error {
	limit := q.Limit
	if limit == 0 {
		limit = 4096
	}
	if !utf8.ValidString(chunk) || p.Bytes < 0 || p.Bytes > len(chunk) || p.Bytes > limit || q.Offset < 0 || q.Offset > total || int64(len(chunk)) > total-q.Offset || p.Bytes < len(chunk) && !utf8.RuneStart(chunk[p.Bytes]) || p.Bytes == 0 && q.Offset < total {
		return rsi.ErrInvalid
	}
	return nil
}

func (r *Runs) evidenceLinkWindow(ctx context.Context, a evidenceAccess, q evidence.Request, count int) (int, *int, error) {
	take := min(count, 20)
	if a.pagePlugin {
		raw, err := r.evidencePagePolicy(ctx, a, contract.LinksContract, contract.LinkFacts{Candidates: count})
		if err != nil {
			return 0, nil, err
		}
		var p contract.LinkPlan
		if err = rsiplugin.DecodeResult(raw, &p); err != nil {
			return 0, nil, err
		}
		if p.Take < 0 || p.Take > 20 || p.Take > count || p.Take == 0 && count > 0 {
			return 0, nil, rsi.ErrInvalid
		}
		take = p.Take
	}
	if count > take {
		next := q.LinksOffset + take
		if next > 1000000 {
			return 0, nil, artifact.ErrLimit
		}
		return take, &next, nil
	}
	return take, nil, nil
}

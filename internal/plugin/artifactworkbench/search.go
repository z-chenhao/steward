package artifactworkbench

import (
	"context"
	"io"
	"strings"

	"github.com/z-chenhao/assistant/internal/plugin/artifactworkbench/contract"
)

// Matching and ranking run in the versioned producer. Only top-K metadata is
// retained, so a large catalogue does not multiply the process's memory use.
func Select(ctx context.Context, request contract.SearchRequest, input io.Reader) (contract.Selection, error) {
	limit, valid := searchLimit(request)
	if !valid {
		return contract.Selection{Indices: []int{}, ErrorCode: "INVALID_ARGUMENT"}, nil
	}
	query := strings.ToLower(strings.TrimSpace(request.Query))
	h := selectionHeap{newest: true}
	for index := 0; index < request.Count; index++ {
		if err := ctx.Err(); err != nil {
			return contract.Selection{}, err
		}
		row, err := contract.ReadSearchRow(input)
		if err != nil {
			return contract.Selection{}, err
		}
		if query == "" || strings.Contains(strings.ToLower(row.Name), query) || strings.Contains(strings.ToLower(row.MediaType), query) {
			h.add(ranked{index: index, stamp: row.CreatedAt}, limit)
		}
	}
	if err := contract.End(input); err != nil {
		return contract.Selection{}, err
	}
	return h.plan(limit), nil
}
func SelectCleanup(ctx context.Context, request contract.CleanupRequest, input io.Reader) (contract.Selection, error) {
	batch, valid := cleanupLimit(request)
	if !valid {
		return contract.Selection{Indices: []int{}, ErrorCode: "INVALID_ARGUMENT"}, nil
	}
	h := selectionHeap{}
	for index := 0; index < request.Count; index++ {
		if err := ctx.Err(); err != nil {
			return contract.Selection{}, err
		}
		row, err := contract.ReadCleanupRow(input)
		if err != nil {
			return contract.Selection{}, err
		}
		h.add(ranked{index: index, stamp: row.ExpiresAt}, batch)
	}
	if err := contract.End(input); err != nil {
		return contract.Selection{}, err
	}
	return h.plan(batch), nil
}

func searchLimit(request contract.SearchRequest) (int, bool) {
	limit := request.Limit
	if limit == 0 {
		limit = 50
	}
	return limit, request.Count >= 0 && limit >= 1 && limit <= 50 && len(request.Query) <= 4096
}
func cleanupLimit(request contract.CleanupRequest) (int, bool) {
	batch := request.Batch
	if batch == 0 {
		batch = 100
	}
	return batch, request.Count >= 0 && batch >= 1 && batch <= 10000
}

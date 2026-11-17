package connectedapps

import (
	"context"
	"github.com/z-chenhao/assistant/internal/connector/composio"
	"github.com/z-chenhao/assistant/internal/plugin/appsdirectory/contract"
)

// Order consumes only public catalogue rows. Account records never enter this
// port. Complete output pages must form one original-index permutation before
// any order is returned; errors never select the native interpreter.
func (s DirectorySelection) Order(ctx context.Context, items []composio.Toolkit) ([]int, error) {
	if !validLabelPolicy(s.Labels) {
		return nil, ErrUnavailable
	}
	if s.mode == directoryLegacy {
		return s.orderLegacyV1(items)
	}
	if s.mode != directoryManaged || s.methods == nil || s.methods.Order == nil {
		return nil, ErrUnavailable
	}
	facts := make([]contract.OrderFact, len(items))
	for i, item := range items {
		facts[i] = contract.OrderFact{Name: item.Name, Slug: item.Slug}
	}
	data, err := contract.EncodeOrderFacts(facts)
	if err != nil {
		return nil, ErrUnavailable
	}
	labels := *s.Labels
	indices := make([]int, 0, len(items))
	seen := make([]bool, len(items))
	for offset := 0; ; {
		request := contract.OrderRequest{Count: len(items), Offset: offset, Labels: labels}
		page, err := s.methods.Order(ctx, request, data)
		if err != nil {
			return nil, ErrUnavailable
		}
		expected := min(contract.OrderPageSize, len(items)-offset)
		if len(page.Indices) != expected {
			return nil, ErrUnavailable
		}
		for _, index := range page.Indices {
			if index < 0 || index >= len(items) || seen[index] {
				return nil, ErrUnavailable
			}
			seen[index] = true
			indices = append(indices, index)
		}
		offset += expected
		if offset == len(items) {
			return indices, nil
		}
	}
}

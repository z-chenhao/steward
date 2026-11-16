package connectedapps

import (
	"context"

	"github.com/z-chenhao/assistant/internal/plugin/appsdirectory/contract"
)

// InventoryOrder binds already-projected original private display fields to
// the request's metadata graph and original account-state revision. All output
// windows must cover one exact original permutation before anything is shown.
func (s DirectorySelection) InventoryOrder(ctx context.Context, revision int64, facts []contract.InventoryOrderFact, connected bool) ([]int, error) {
	if s.mode == directoryLegacy {
		return AccountInventoryOrderFacts(facts, connected), nil
	}
	if s.mode != directoryManaged || s.methods == nil || s.methods.InventoryOrder == nil {
		return nil, ErrUnavailable
	}
	data := contract.EncodeInventoryOrderFacts(facts)
	indices := make([]int, 0, len(facts))
	seen := make([]bool, len(facts))
	for offset := 0; ; {
		page, err := s.methods.InventoryOrder(ctx, revision, contract.InventoryOrderRequest{Count: len(facts), Offset: offset, Connected: connected}, data)
		if err != nil {
			return nil, ErrUnavailable
		}
		expected := min(contract.OrderPageSize, len(facts)-offset)
		if len(page.Indices) != expected {
			return nil, ErrUnavailable
		}
		for _, index := range page.Indices {
			if index < 0 || index >= len(facts) || seen[index] {
				return nil, ErrUnavailable
			}
			seen[index] = true
			indices = append(indices, index)
		}
		offset += expected
		if offset == len(facts) {
			return indices, nil
		}
	}
}

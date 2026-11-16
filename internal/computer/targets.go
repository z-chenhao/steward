package computer

import "context"

// Only original authorized inventory positions cross the private policy seam.
// Driver identities and handles remain owned by ComputerUseRuntime.
type InventoryOrderer func(context.Context, string, []Target) ([]int, error)
type TargetMatcher func(context.Context, string, string, []Target) ([]int, error)

// TargetReceipt commits inventory disclosure or a newly minted binding under
// its original Computer lease. Token is empty for inventory-only results.
// This process-local receipt is never serialized into the Tool result.
type TargetReceipt struct {
	Lease    string
	Instance Instance
	Token    string
}

func validTargetIndices(indices []int, count int, complete bool) bool {
	if complete && len(indices) != count {
		return false
	}
	seen := make([]bool, count)
	for _, i := range indices {
		if i < 0 || i >= count || seen[i] {
			return false
		}
		seen[i] = true
	}
	return true
}
func (r UseRuntime) publicInventory(ctx context.Context, kind string, values []Target) ([]any, error) {
	pub := make([]any, len(values))
	for i, v := range values {
		pub[i] = v.Public()
	}
	// Preserve the existing public-output budget before releasing labels. This
	// also bounds ordering input without imposing a new inventory-count limit.
	if r.OrderInventory == nil || useResult(pub).Error != nil {
		return pub, nil
	}
	indices, err := r.OrderInventory(ctx, kind, values)
	if err != nil {
		return nil, err
	}
	if !validTargetIndices(indices, len(values), true) {
		return nil, ErrInvalid
	}
	for i, index := range indices {
		pub[i] = values[index].Public()
	}
	return pub, nil
}
func (r UseRuntime) matchTarget(ctx context.Context, kind, selector string, values []Target) (*Target, error) {
	if r.MatchTargets == nil {
		return nativeTargetMatchV1(kind, selector, values), nil
	}
	indices, err := r.MatchTargets(ctx, kind, selector, values)
	if err != nil {
		return nil, err
	}
	if !validTargetIndices(indices, len(values), false) {
		return nil, ErrInvalid
	}
	if len(indices) != 1 {
		return nil, nil
	}
	// An explicit known identity can never be redirected by label policy. A
	// duplicated identity is ambiguous even if the candidate picks one of them.
	identities := 0
	for _, v := range values {
		if v.ID == selector {
			identities++
		}
	}
	if identities > 1 || identities == 1 && values[indices[0]].ID != selector {
		return nil, ErrInvalid
	}
	target := values[indices[0]]
	return &target, nil
}

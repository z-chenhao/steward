package connectedapps

import "context"

// Inventory returns proof-backed original row references, never account models
// to a candidate. One grouped app card has one original Toolkit identity. Host
// grouping is the reference boundary; eligibility and representative choice run
// in the managed card-state selector. Source-order projection preserves original
// insertion before the owning managed private-name ordering service.
func (s DirectorySelection) Inventory(ctx context.Context, state State) (InventoryProjection, error) {
	if s.mode == directoryLegacy {
		var toolkits []string
		seen := map[string]bool{}
		for _, row := range state.Connections {
			if !seen[row.Toolkit] {
				seen[row.Toolkit] = true
				toolkits = append(toolkits, row.Toolkit)
			}
		}
		values := AccountCardFacts(state, toolkits)
		states := map[string]CardState{}
		for i, toolkit := range toolkits {
			states[toolkit] = values[i]
		}
		return InventoryProjection{Rows: AccountInventoryFacts(state), states: states}, nil
	}
	selected, err := s.accountSelections(ctx, state)
	if err != nil {
		return InventoryProjection{}, err
	}
	keep := map[int]bool{}
	states := map[string]CardState{}
	for toolkit, selection := range selected {
		states[toolkit] = selection.State
		if selection.Row >= 0 {
			keep[selection.Row] = true
		}
	}
	var rows []int
	for i := range state.Connections {
		if keep[i] {
			rows = append(rows, i)
		}
	}
	return InventoryProjection{Rows: rows, states: states}, nil
}

// InventoryProjection is a request-local Host value, never candidate JSON. One
// accepted selection supplies both inventory references and proof-backed flags.
type InventoryProjection struct {
	Rows   []int
	states map[string]CardState
}

func (p InventoryProjection) States(toolkits []string) []CardState {
	out := make([]CardState, len(toolkits))
	for i, toolkit := range toolkits {
		out[i] = p.states[toolkit]
	}
	return out
}

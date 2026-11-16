package connectedapps

import (
	"context"

	"github.com/z-chenhao/assistant/internal/plugin/appsdirectory/contract"
)

type CardState struct{ Managed, Connected, PublicActive, Pending bool }

type accountSelection struct {
	State CardState
	Row   int
}

// accountSelections projects proof-backed original rows. Exact Toolkit equality
// defines one app identity; candidate input has no account identity or name.
func (s DirectorySelection) accountSelections(ctx context.Context, state State) (map[string]accountSelection, error) {
	if s.mode != directoryManaged || s.methods == nil || s.methods.CardStates == nil {
		return nil, ErrUnavailable
	}
	groups := map[string][]int{}
	var toolkits []string
	for i, row := range state.Connections {
		if _, exists := groups[row.Toolkit]; !exists {
			toolkits = append(toolkits, row.Toolkit)
		}
		groups[row.Toolkit] = append(groups[row.Toolkit], i)
	}
	results := map[string]accountSelection{}
	read := func(rows []int) (accountSelection, error) {
		prior := contract.CardStatePlan{}
		for offset := 0; ; {
			end := min(offset+contract.CardStateWindow, len(rows))
			data := make([]byte, end-offset)
			for i, index := range rows[offset:end] {
				row := state.Connections[index]
				var bits byte
				if row.Status == "active" {
					bits |= contract.FactActive
				}
				if row.Status == "pending" {
					bits |= contract.FactPending
				}
				if row.Status == "disconnected" {
					bits |= contract.FactDisconnected
				}
				if row.RevocationConfirmed {
					bits |= contract.FactRevocationConfirmed
				}
				if row.Public {
					bits |= contract.FactPublic
				}
				data[i] = bits
			}
			plan, err := s.methods.CardStates(ctx, state.Revision, contract.CardStateRequest{Count: len(rows), Offset: offset, Prior: prior}, data)
			if err != nil {
				return accountSelection{}, ErrUnavailable
			}
			for _, ref := range []*int{plan.Managed, plan.Active, plan.Pending} {
				if ref != nil && (*ref < 0 || *ref >= end) {
					return accountSelection{}, ErrUnavailable
				}
			}
			if plan.Active != nil && state.Connections[rows[*plan.Active]].Status != "active" {
				return accountSelection{}, ErrUnavailable
			}
			if plan.Pending != nil && state.Connections[rows[*plan.Pending]].Status != "pending" {
				return accountSelection{}, ErrUnavailable
			}
			prior = plan
			offset = end
			if offset == len(rows) {
				result := CardState{Managed: plan.Managed != nil, Connected: plan.Active != nil, Pending: plan.Pending != nil}
				if plan.Active != nil {
					result.PublicActive = state.Connections[rows[*plan.Active]].Public
				}
				row := -1
				if plan.Managed != nil {
					row = rows[*plan.Managed]
				}
				return accountSelection{State: result, Row: row}, nil
			}
		}
	}
	if len(groups) == 0 {
		if _, err := read(nil); err != nil {
			return nil, err
		}
		return map[string]accountSelection{}, nil
	}
	for _, toolkit := range toolkits {
		value, err := read(groups[toolkit])
		if err != nil {
			return nil, err
		}
		results[toolkit] = value
	}
	return results, nil
}

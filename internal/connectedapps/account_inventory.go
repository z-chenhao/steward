package connectedapps

import (
	"context"
	"github.com/z-chenhao/assistant/internal/plugin/appsaccounts/contract"
	directory "github.com/z-chenhao/assistant/internal/plugin/appsdirectory/contract"
)

type AccountInventoryReader func(context.Context) (AccountInventoryPolicy, error)
type AccountInventoryPolicy struct {
	mode  accountPolicyMode
	check func(context.Context, int64) error
	read  func(context.Context, int64, contract.InventoryRequest, []byte) (contract.InventoryPlan, error)
}

func NativeAccountInventory() AccountInventoryPolicy {
	return AccountInventoryPolicy{mode: accountPolicyNative}
}

// HistoricalAccountInventory interprets only a named original package profile.
// Its exact graph and current authoritative state are checked before release.
func HistoricalAccountInventory(check func(context.Context, int64) error) AccountInventoryPolicy {
	if check == nil {
		return AccountInventoryPolicy{}
	}
	return AccountInventoryPolicy{mode: accountPolicyNative, check: check}
}
func ManagedAccountInventory(read func(context.Context, int64, contract.InventoryRequest, []byte) (contract.InventoryPlan, error)) AccountInventoryPolicy {
	return AccountInventoryPolicy{mode: accountPolicyManaged, read: read}
}
func (s *Service) AccountInventory(ctx context.Context) (AccountInventoryPolicy, error) {
	if s.ReadInventory == nil {
		return NativeAccountInventory(), nil
	}
	return s.ReadInventory(ctx)
}

// Project binds every reference to this original state snapshot. A managed error
// never enters the named historical interpreter. The caller freezes this policy
// before an account effect so its accepted response cannot choose a later graph.
func (p AccountInventoryPolicy) Project(ctx context.Context, state State, connected bool) (InventoryProjection, error) {
	if p.mode == accountPolicyNative {
		if p.check != nil {
			if err := p.check(ctx, state.Revision); err != nil {
				return InventoryProjection{}, err
			}
		}
		rows := AccountInventoryFacts(state)
		facts := make([]directory.InventoryOrderFact, len(rows))
		slugs := make([]string, len(rows))
		for i, n := range rows {
			r := state.Connections[n]
			facts[i] = directory.InventoryOrderFact{Name: r.Name, Slug: r.Toolkit}
			slugs[i] = r.Toolkit
		}
		flags := AccountCardFacts(state, slugs)
		states := map[string]CardState{}
		for i, slug := range slugs {
			states[slug] = flags[i]
		}
		order := AccountInventoryOrderFacts(facts, connected)
		var indices []int
		for _, n := range order {
			indices = append(indices, rows[n])
		}
		if p.check != nil {
			if err := p.check(ctx, state.Revision); err != nil {
				return InventoryProjection{}, err
			}
		}
		return InventoryProjection{Rows: indices, states: states}, nil
	}
	if p.mode != accountPolicyManaged || p.read == nil {
		return InventoryProjection{}, ErrUnavailable
	}
	rows := make([]contract.InventoryRow, len(state.Connections))
	for i, r := range state.Connections {
		var bits byte
		switch r.Status {
		case "active":
			bits |= contract.Active
		case "pending":
			bits |= contract.Pending
		case "disconnected":
			bits |= contract.Disconnected
		}
		if r.Public {
			bits |= contract.Public
		}
		if r.RevocationConfirmed {
			bits |= contract.RevocationConfirmed
		}
		rows[i] = contract.InventoryRow{Slug: r.Toolkit, Name: r.Name, Facts: bits}
	}
	data := contract.EncodeInventoryRows(rows)
	out := InventoryProjection{states: map[string]CardState{}}
	total := -1
	for offset := 0; ; {
		plan, err := p.read(ctx, state.Revision, contract.InventoryRequest{Count: len(rows), Offset: offset, Connected: connected}, data)
		if err != nil {
			return InventoryProjection{}, err
		}
		if plan.Total < 0 || plan.Total > len(rows) || total >= 0 && plan.Total != total || offset > plan.Total {
			return InventoryProjection{}, ErrUnavailable
		}
		total = plan.Total
		expected := min(contract.InventoryPageSize, total-offset)
		if len(plan.Items) != expected {
			return InventoryProjection{}, ErrUnavailable
		}
		for _, item := range plan.Items {
			if item.Row < 0 || item.Row >= len(rows) {
				return InventoryProjection{}, ErrUnavailable
			}
			r := state.Connections[item.Row]
			if _, duplicate := out.states[r.Toolkit]; duplicate || r.Status == "disconnected" && r.RevocationConfirmed {
				return InventoryProjection{}, ErrUnavailable
			}
			status := CardState{Managed: true}
			if item.Active != nil {
				n := *item.Active
				if n < 0 || n >= len(rows) || state.Connections[n].Toolkit != r.Toolkit || state.Connections[n].Status != "active" {
					return InventoryProjection{}, ErrUnavailable
				}
				status.Connected = true
				status.PublicActive = state.Connections[n].Public
			}
			if item.Pending != nil {
				n := *item.Pending
				if n < 0 || n >= len(rows) || state.Connections[n].Toolkit != r.Toolkit || state.Connections[n].Status != "pending" {
					return InventoryProjection{}, ErrUnavailable
				}
				status.Pending = true
			}
			out.Rows = append(out.Rows, item.Row)
			out.states[r.Toolkit] = status
		}
		offset += expected
		if offset == total {
			return out, nil
		}
	}
}

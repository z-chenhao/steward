package connectedapps

import (
	"context"
	"errors"

	"github.com/z-chenhao/assistant/internal/connector/composio"
	"github.com/z-chenhao/assistant/internal/plugin/appsaccounts/contract"
)

type connectionIntent struct {
	Alias     string
	PendingID *string
}

func (s *Service) connectionIntent(ctx context.Context, state State, toolkit, alias string) (connectionIntent, error) {
	var rows []contract.ConnectRow
	var original []int
	for i, row := range state.Connections {
		if row.Toolkit != toolkit {
			continue
		}
		var facts byte
		switch row.Status {
		case "active":
			facts |= contract.Active
		case "pending":
			facts |= contract.Pending
		case "disconnected":
			facts |= contract.Disconnected
		}
		if row.Public {
			facts |= contract.Public
		}
		rows = append(rows, contract.ConnectRow{Alias: row.Alias, Facts: facts})
		original = append(original, i)
	}
	selection, err := s.connectIntentSelection(ctx)
	if err != nil {
		if errors.Is(err, ErrConflict) {
			return connectionIntent{}, ErrConflict
		}
		return connectionIntent{}, ErrUnavailable
	}
	plan, err := selection.readConnectIntent(ctx, state.Revision, contract.ConnectRequest{Count: len(rows), Alias: alias}, rows)
	if err != nil {
		if errors.Is(err, ErrConflict) {
			return connectionIntent{}, ErrConflict
		}
		return connectionIntent{}, ErrUnavailable
	}
	switch plan.Kind {
	case "alias_required", "conflict":
		if plan.Row != nil || plan.Alias != "" {
			return connectionIntent{}, ErrUnavailable
		}
		if plan.Kind == "alias_required" {
			return connectionIntent{}, ErrAliasRequired
		}
		return connectionIntent{}, ErrConflict
	case "pending":
		if plan.Alias != "" || plan.Row == nil || *plan.Row < 0 || *plan.Row >= len(original) {
			return connectionIntent{}, ErrUnavailable
		}
		row := state.Connections[original[*plan.Row]]
		if row.Status != "pending" || alias != "" && row.Alias != alias {
			return connectionIntent{}, ErrUnavailable
		}
		return connectionIntent{PendingID: &row.ID}, nil
	case "new":
		if plan.Row != nil || !composio.ValidID(plan.Alias) || len(plan.Alias) > 64 || alias != "" && plan.Alias != alias {
			return connectionIntent{}, ErrUnavailable
		}
		// Alias uniqueness is an existing connection identity/namespace invariant,
		// separate from the candidate's default-label and reuse policy.
		for _, i := range original {
			row := state.Connections[i]
			if row.Alias == plan.Alias && row.Status != "disconnected" {
				return connectionIntent{}, ErrUnavailable
			}
		}
		return connectionIntent{Alias: plan.Alias}, nil
	default:
		return connectionIntent{}, ErrUnavailable
	}
}

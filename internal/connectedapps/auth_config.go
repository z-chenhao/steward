package connectedapps

import (
	"context"
	"errors"

	"github.com/z-chenhao/assistant/internal/connector/composio"
	"github.com/z-chenhao/assistant/internal/plugin/appsaccounts/contract"
)

type AuthConfigPolicy struct {
	mode accountPolicyMode
	read func(context.Context, int64, contract.AuthRequest, []contract.AuthRow) (contract.AuthPlan, error)
}

func NativeAuthConfig() AuthConfigPolicy { return AuthConfigPolicy{mode: accountPolicyNative} }
func ManagedAuthConfig(read func(context.Context, int64, contract.AuthRequest, []contract.AuthRow) (contract.AuthPlan, error)) AuthConfigPolicy {
	return AuthConfigPolicy{mode: accountPolicyManaged, read: read}
}
func authFact(row composio.AuthConfigMetadata) contract.AuthRow {
	var bits byte
	if row.ToolkitMatch {
		bits |= contract.AuthToolkit
	}
	if row.Enabled {
		bits |= contract.AuthEnabled
	}
	if row.Router {
		bits |= contract.AuthRouter
	}
	if row.Unmanaged {
		bits |= contract.AuthUnmanaged
	}
	if row.ValidID {
		bits |= contract.AuthValidID
	}
	if row.ExplicitMatch {
		bits |= contract.AuthExplicitMatch
	}
	return contract.AuthRow{ID: row.ID, Created: row.Created, Facts: bits}
}
func eligibleAuth(row composio.AuthConfigMetadata, override bool) bool {
	return row.ToolkitMatch && row.Enabled && row.Router && row.ValidID && (override && row.ExplicitMatch || !override && row.Unmanaged)
}

// find interprets the closed source commands for one frozen owner intent. The
// candidate owns source choice, filtering, ordering, continuation and fallback.
// Host keeps exact source parameters/references, source budgets and non-replay.
func (p AuthConfigPolicy) find(ctx context.Context, state State, client composio.Client, toolkit, explicit string, noHosted bool) (composio.AuthConfigSelection, error) {
	if p.mode == accountPolicyNative {
		return findAuthConfigLegacyV1(ctx, client, toolkit, explicit, state.AuthConfigs[toolkit], noHosted)
	}
	if p.mode != accountPolicyManaged || p.read == nil {
		return composio.AuthConfigSelection{}, ErrUnavailable
	}
	request := contract.AuthRequest{Step: "begin", Explicit: explicit != "", Saved: state.AuthConfigs[toolkit] != "", NoHosted: noHosted}
	var rows []composio.AuthConfigMetadata
	var prior *composio.AuthConfigMetadata
	var failure error
	sourceID := ""
	source := ""
	cursor := ""
	next := ""
	pages := 0
	seen := map[string]bool{}
	reject := func() error {
		if failure != nil {
			return failure
		}
		if request.Override {
			return &composio.Error{Status: 400}
		}
		return &composio.Error{}
	}
	for range 22 {
		facts := make([]contract.AuthRow, len(rows))
		for i, row := range rows {
			facts[i] = authFact(row)
		}
		request.Count = len(facts)
		plan, err := p.read(ctx, state.Revision, request, facts)
		if err != nil {
			return composio.AuthConfigSelection{}, err
		}
		choose := func() (*composio.AuthConfigMetadata, error) {
			if plan.Row == nil {
				return nil, nil
			}
			i := *plan.Row
			if i < 0 || i >= len(rows) || !eligibleAuth(rows[i], request.Override) {
				return nil, ErrUnavailable
			}
			value := rows[i]
			return &value, nil
		}
		switch plan.Kind {
		case "fetch":
			if request.Step != "begin" || plan.Row != nil {
				return composio.AuthConfigSelection{}, ErrUnavailable
			}
			if explicit != "" && plan.Source != "explicit" {
				return composio.AuthConfigSelection{}, ErrUnavailable
			}
			source = plan.Source
			switch source {
			case "explicit":
				if explicit == "" || plan.Limit != 0 {
					return composio.AuthConfigSelection{}, ErrUnavailable
				}
				sourceID = explicit
			case "saved":
				if explicit != "" || state.AuthConfigs[toolkit] == "" || plan.Limit != 0 {
					return composio.AuthConfigSelection{}, ErrUnavailable
				}
				sourceID = state.AuthConfigs[toolkit]
			case "auto":
				if explicit != "" || plan.Limit < 1 || plan.Limit > 200 {
					return composio.AuthConfigSelection{}, ErrUnavailable
				}
			default:
				return composio.AuthConfigSelection{}, ErrUnavailable
			}
		case "next":
			if request.Step != "result" || source != "auto" || !request.HasNext || request.CursorSeen || pages >= 20 || plan.Source != "" || plan.Limit < 1 || plan.Limit > 200 {
				return composio.AuthConfigSelection{}, ErrUnavailable
			}
			prior, err = choose()
			if err != nil {
				return composio.AuthConfigSelection{}, err
			}
			cursor = next
		case "done":
			if request.Step != "result" || request.CursorSeen || plan.Source != "" || plan.Limit != 0 {
				return composio.AuthConfigSelection{}, ErrUnavailable
			}
			selected, err := choose()
			if err != nil {
				return composio.AuthConfigSelection{}, err
			}
			if selected == nil && request.Override {
				return composio.AuthConfigSelection{}, ErrUnavailable
			}
			id := ""
			if selected != nil {
				id = selected.ID
			}
			return composio.AuthConfigSelection{ID: id, Readable: true}, nil
		case "unreadable":
			if request.Step != "error" || source != "auto" || (request.Status != 401 && request.Status != 403) || plan.Row != nil || plan.Source != "" || plan.Limit != 0 {
				return composio.AuthConfigSelection{}, ErrUnavailable
			}
			return composio.AuthConfigSelection{}, nil
		case "requires_config":
			if request.Step != "result" || request.Override || request.HasNext || request.CursorSeen || !noHosted || plan.Row != nil || plan.Source != "" || plan.Limit != 0 {
				return composio.AuthConfigSelection{}, ErrUnavailable
			}
			return composio.AuthConfigSelection{}, ErrAuthConfigRequired
		case "reject":
			if plan.Row != nil || plan.Source != "" || plan.Limit != 0 {
				return composio.AuthConfigSelection{}, ErrUnavailable
			}
			return composio.AuthConfigSelection{}, reject()
		default:
			return composio.AuthConfigSelection{}, ErrUnavailable
		}
		request.Step = "result"
		request.Override = source != "auto"
		request.HasNext = false
		request.CursorSeen = false
		request.Status = 0
		rows = nil
		failure = nil
		if source != "auto" {
			value, err := client.ReadAuthConfig(ctx, toolkit, sourceID)
			if err != nil {
				failure = err
			} else {
				rows = []composio.AuthConfigMetadata{value}
			}
		} else {
			if pages >= 20 || pages > 0 && seen[cursor] {
				return composio.AuthConfigSelection{}, ErrUnavailable
			}
			seen[cursor] = true
			pages++
			request.Page = pages
			page, err := client.ReadAuthConfigPage(ctx, toolkit, cursor, plan.Limit)
			if err != nil {
				failure = err
			} else {
				if prior != nil {
					rows = append(rows, *prior)
				}
				rows = append(rows, page.Items...)
				next = page.Next
				request.HasNext = next != ""
				request.CursorSeen = next != "" && seen[next]
			}
		}
		if failure != nil {
			request.Step = "error"
			var remote *composio.Error
			if errors.As(failure, &remote) {
				request.Status = remote.Status
			}
		}
	}
	return composio.AuthConfigSelection{}, ErrUnavailable
}

func (s *Service) selectAuthConfig(ctx context.Context, state State, client composio.Client, toolkit, explicit string, noHosted bool) (composio.AuthConfigSelection, error) {
	selection, err := s.accountPolicies(ctx)
	if err != nil {
		return composio.AuthConfigSelection{}, err
	}
	return selection.auth.find(ctx, state, client, toolkit, explicit, noHosted)
}

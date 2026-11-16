package connectedapps

import (
	"context"
	"errors"

	"github.com/z-chenhao/assistant/internal/connector/composio"
	"github.com/z-chenhao/assistant/internal/plugin/appsaccounts/contract"
)

type ReconnectPolicy struct {
	mode accountPolicyMode
	read func(context.Context, int64, contract.ReconnectRequest) (contract.ReconnectPlan, error)
}

func NativeReconnect() ReconnectPolicy { return ReconnectPolicy{mode: accountPolicyNative} }
func ManagedReconnect(read func(context.Context, int64, contract.ReconnectRequest) (contract.ReconnectPlan, error)) ReconnectPolicy {
	return ReconnectPolicy{mode: accountPolicyManaged, read: read}
}
func (p ReconnectPolicy) available() bool {
	return p.mode == accountPolicyNative || p.mode == accountPolicyManaged && p.read != nil
}
func (p ReconnectPolicy) apply(ctx context.Context, s *Service, state State, c Connection) (composio.Link, error) {
	if p.mode == accountPolicyNative {
		return s.reconnectLegacyV1(ctx, c)
	}
	if !p.available() {
		return composio.Link{}, ErrUnavailable
	}
	request := contract.ReconnectRequest{Step: "begin", Pending: c.Status == "pending"}
	originalHash := BindingHash(state, c)
	closed := false
	for range 2 {
		plan, err := p.read(ctx, state.Revision, request)
		if err != nil {
			if errors.Is(err, ErrConflict) {
				return composio.Link{}, ErrConflict
			}
			return composio.Link{}, ErrUnavailable
		}
		switch plan.Kind {
		case "deny":
			return composio.Link{}, ErrDenied
		case "close":
			if closed {
				return composio.Link{}, ErrUnavailable
			}
			closed = true
			if err := s.disconnect(ctx, c.ID); err != nil {
				return composio.Link{}, err
			}
			state, err = s.State(ctx)
			if err != nil {
				return composio.Link{}, err
			}
			original := find(&state, c.ID)
			if original == nil || original.Status != "disconnected" || BindingHash(state, *original) != originalHash {
				return composio.Link{}, ErrConflict
			}
			request.Step = "closed"
		case "connect":
			if !closed {
				return composio.Link{}, ErrUnavailable
			}
			// The accepted Link returns directly. A later candidate round may neither
			// hide a newly persisted connection nor ask to repeat its provider effects.
			return s.connectFromState(ctx, state, c.Toolkit, c.Alias, "")
		default:
			return composio.Link{}, ErrUnavailable
		}
	}
	return composio.Link{}, ErrUnavailable
}

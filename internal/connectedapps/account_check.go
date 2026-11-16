package connectedapps

import (
	"context"
	"errors"
	"github.com/z-chenhao/assistant/internal/plugin/appsaccounts/contract"
)

type AccountCheckPolicy struct {
	mode accountPolicyMode
	read func(context.Context, int64, contract.AccountCheckRequest) (contract.AccountCheckPlan, error)
}

func NativeAccountCheck() AccountCheckPolicy { return AccountCheckPolicy{mode: accountPolicyNative} }
func ManagedAccountCheck(read func(context.Context, int64, contract.AccountCheckRequest) (contract.AccountCheckPlan, error)) AccountCheckPolicy {
	return AccountCheckPolicy{mode: accountPolicyManaged, read: read}
}
func (p AccountCheckPolicy) available() bool {
	return p.mode == accountPolicyNative || p.mode == accountPolicyManaged && p.read != nil
}
func (p AccountCheckPolicy) decide(ctx context.Context, revision int64, request contract.AccountCheckRequest) (string, error) {
	var plan contract.AccountCheckPlan
	var err error
	if p.mode == accountPolicyNative {
		plan = accountCheckLegacyV1(request)
	} else {
		if !p.available() {
			return "", ErrUnavailable
		}
		plan, err = p.read(ctx, revision, request)
		if err != nil {
			if errors.Is(err, ErrConflict) {
				return "", ErrConflict
			}
			return "", ErrUnavailable
		}
	}
	// Candidate can expire a source state conservatively; it cannot invent an
	// active/pending authority or turn a public proof into a private binding.
	if request.Public {
		if plan.Status != "refresh" {
			return "", ErrUnavailable
		}
		return plan.Status, nil
	}
	switch plan.Status {
	case "active":
		if request.Disabled || !request.Active {
			return "", ErrUnavailable
		}
	case "pending":
		if request.Disabled || !request.Initializing && !request.Initiated {
			return "", ErrUnavailable
		}
	case "expired":
	default:
		return "", ErrUnavailable
	}
	return plan.Status, nil
}

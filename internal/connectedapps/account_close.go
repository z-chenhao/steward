package connectedapps

import (
	"context"

	"github.com/z-chenhao/assistant/internal/connector/composio"
	"github.com/z-chenhao/assistant/internal/plugin/appsaccounts/contract"
)

// AccountCloseReader is independent owner control. Unrelated business graph
// failures cannot suppress native remote cleanup before this service's opt-in;
// after opt-in unavailable cleanup never blocks independently durable closure.
type AccountCloseReader func(context.Context) (AccountClosePolicy, error)
type AccountClosePolicy struct {
	mode accountPolicyMode
	read func(context.Context, int64, contract.AccountCloseRequest) (contract.AccountClosePlan, error)
}

func NativeAccountClose() AccountClosePolicy { return AccountClosePolicy{mode: accountPolicyNative} }
func ManagedAccountClose(read func(context.Context, int64, contract.AccountCloseRequest) (contract.AccountClosePlan, error)) AccountClosePolicy {
	return AccountClosePolicy{mode: accountPolicyManaged, read: read}
}
func (p AccountClosePolicy) available() bool {
	return p.mode == accountPolicyNative || p.mode == accountPolicyManaged && p.read != nil
}
func (s *Service) accountClosePolicy(ctx context.Context) AccountClosePolicy {
	if policies, ok := ctx.Value(accountPoliciesKey{}).(AccountPolicies); ok {
		return policies.close
	}
	if s.ReadAccountClose == nil {
		return NativeAccountClose()
	}
	policy, err := s.ReadAccountClose(ctx)
	if err != nil {
		return AccountClosePolicy{}
	}
	return policy
}

// cleanup returns only the original source's proof. Candidate failure cannot
// invent or erase an already observed authorized revoke. The caller must still
// persist under the original owner/privacy fence and CAS, never replay effects.
func (p AccountClosePolicy) cleanup(ctx context.Context, state State, client composio.Client, c Connection) bool {
	if p.mode == accountPolicyNative {
		return accountCloseLegacyV1(ctx, state, client, c)
	}
	if !p.available() {
		return false
	}
	request := contract.AccountCloseRequest{Step: "closed"}
	accountRead, revokeSent, deleteSent := false, false, false
	proved := false
	for range 4 {
		plan, err := p.read(ctx, state.Revision, request)
		if err != nil {
			return proved
		}
		switch plan.Kind {
		case "account":
			if accountRead || revokeSent || deleteSent {
				return proved
			}
			accountRead = true
			account, err := client.Account(ctx, c.AccountID)
			request.Step = "account"
			request.Failed = err != nil
			request.Matched = err == nil && accountIdentity(account, state, c)
		case "revoke":
			if !accountRead || !request.Matched || request.Failed || revokeSent || deleteSent {
				return proved
			}
			revokeSent = true
			proved = client.RevokeAccount(ctx, c.AccountID)
			request.Step = "revoked"
			request.Revoked = proved
			request.Failed = !proved
		case "delete":
			if !accountRead || !request.Matched || !revokeSent || deleteSent {
				return proved
			}
			deleteSent = true
			err := client.DeleteAccount(ctx, c.AccountID)
			request.Step = "deleted"
			request.Failed = err != nil
		default:
			return proved
		}
	}
	return proved
}

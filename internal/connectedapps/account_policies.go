package connectedapps

import (
	"context"

	"github.com/z-chenhao/assistant/internal/plugin/appsaccounts/contract"
	"github.com/z-chenhao/assistant/internal/plugin/appsaccounts/v1"
)

// AccountPolicyReader captures an owner-side graph before a logical operation.
// Connect/configuration/Check and reconnect each retain one concrete selection
// across original source I/O; independently missing services fail separately.
// A zero/missing managed selection is unavailable, never a native fallback.
type AccountPolicyReader func(context.Context) (AccountPolicies, error)
type accountPolicyMode uint8

const (
	accountPolicyUnavailable accountPolicyMode = iota
	accountPolicyNative
	accountPolicyManaged
)

type AccountPolicies struct {
	intentMode accountPolicyMode
	auth       AuthConfigPolicy
	session    SessionFlowPolicy
	check      AccountCheckPolicy
	close      AccountClosePolicy
	reconnect  ReconnectPolicy
	intentRead func(context.Context, int64, contract.ConnectRequest, []contract.ConnectRow) (contract.ConnectPlan, error)
}

func NativeAccountPolicies() AccountPolicies {
	return AccountPolicies{intentMode: accountPolicyNative, auth: NativeAuthConfig(), session: NativeSessionFlow(), check: NativeAccountCheck(), close: NativeAccountClose(), reconnect: NativeReconnect()}
}
func ManagedConnectIntent(read func(context.Context, int64, contract.ConnectRequest, []contract.ConnectRow) (contract.ConnectPlan, error)) AccountPolicies {
	return AccountPolicies{intentMode: accountPolicyManaged, intentRead: read}
}
func (s AccountPolicies) readConnectIntent(ctx context.Context, revision int64, request contract.ConnectRequest, rows []contract.ConnectRow) (contract.ConnectPlan, error) {
	if s.intentMode == accountPolicyNative {
		return v1.Connect(rows, request.Alias), nil
	}
	if s.intentMode != accountPolicyManaged || s.intentRead == nil {
		return contract.ConnectPlan{}, ErrUnavailable
	}
	return s.intentRead(ctx, revision, request, rows)
}

func (s AccountPolicies) WithAuthConfig(auth AuthConfigPolicy) AccountPolicies {
	s.auth = auth
	return s
}

type accountPoliciesKey struct{}

func (s *Service) accountPolicies(ctx context.Context) (AccountPolicies, error) {
	if cached, ok := ctx.Value(accountPoliciesKey{}).(AccountPolicies); ok {
		return cached, nil
	}
	if s.ReadAccountPolicies == nil {
		return NativeAccountPolicies(), nil
	}
	return s.ReadAccountPolicies(ctx)
}
func (s *Service) connectIntentSelection(ctx context.Context) (AccountPolicies, error) {
	selection, err := s.accountPolicies(ctx)
	if err != nil {
		return AccountPolicies{}, err
	}
	if selection.intentMode != accountPolicyNative && (selection.intentMode != accountPolicyManaged || selection.intentRead == nil) {
		return AccountPolicies{}, ErrUnavailable
	}
	return selection, nil
}

// WithSessionFlow attaches the independently opted-in workflow from the same graph.
func (s AccountPolicies) WithSessionFlow(flow SessionFlowPolicy) AccountPolicies {
	s.session = flow
	return s
}

func (s AccountPolicies) WithAccountCheck(check AccountCheckPolicy) AccountPolicies {
	s.check = check
	return s
}

func (p AccountPolicies) WithAccountClose(close AccountClosePolicy) AccountPolicies {
	p.close = close
	return p
}

func (p AccountPolicies) WithReconnect(reconnect ReconnectPolicy) AccountPolicies {
	p.reconnect = reconnect
	return p
}

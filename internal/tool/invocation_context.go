package tool

import "context"

type acceptedInvocationKey struct{}

// AcceptedInvocationID is correlation, never an authorization token. Host
// handlers must still validate the invocation and its execution claim.
func AcceptedInvocationID(ctx context.Context) string {
	id, _ := ctx.Value(acceptedInvocationKey{}).(string)
	return id
}

// InvocationContextStore binds private adapter drivers after Start has accepted
// the exact invocation. It grants no model/plugin capability and is not a hook
// API. MCP needs the actual Run/CodeMode owner for both Execute and Invoke.
type InvocationContextStore interface {
	InvocationContext(context.Context, Invocation) (context.Context, error)
}

package sqlite

import (
	"context"
	"database/sql"
	"errors"

	"github.com/z-chenhao/assistant/internal/rsi"
	"github.com/z-chenhao/assistant/internal/tool"
)

type connectorResourceCheck func(context.Context, *sql.Tx, string) error

func (t *tools) connectorInvocationFence(inv tool.Invocation, name, arguments string, resource connectorResourceCheck) func(context.Context) error {
	return func(ctx context.Context) error {
		return t.runs.evidenceReadTx(ctx, func(tx *sql.Tx) error {
			_, acceptedName, acceptedArgs, err := t.acceptedBuiltin(ctx, tx, inv)
			if err != nil {
				return err
			}
			if acceptedName != name || acceptedArgs != arguments {
				return tool.ErrApprovalDenied
			}
			return t.connectorAuthority(ctx, tx, inv.ID, resource)
		})
	}
}

func (t *tools) connectorAuthority(ctx context.Context, tx *sql.Tx, id string, resource connectorResourceCheck) error {
	if err := resource(ctx, tx, id); err != nil {
		return err
	}
	return t.runs.pluginDispatchFence(ctx, tx, t.claim.RunID())
}

func (t *tools) commitConnectorResult(ctx context.Context, tx *sql.Tx, id string, result tool.Result, resource connectorResourceCheck) (tool.Result, error) {
	err := t.connectorAuthority(ctx, tx, id, resource)
	if err == nil {
		result.HostFence = nil
		return result, nil
	}
	if !errors.Is(err, rsi.ErrRevoked) {
		return tool.Result{}, err
	}
	_, policy, err := t.load(ctx, tx, id)
	if err != nil {
		return tool.Result{}, err
	}
	if policy.Replay == "never" {
		return tool.Failure("UNKNOWN_SIDE_EFFECT"), nil
	}
	return tool.Failure("PROVIDER_UNAVAILABLE"), nil
}

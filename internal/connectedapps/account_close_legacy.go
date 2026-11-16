package connectedapps

import (
	"context"

	"github.com/z-chenhao/assistant/internal/connector/composio"
)

// Exact pre-close/default23/direct and already captured native cleanup. Never
// used after managed failure; delete when these real consumers no longer exist.
func accountCloseLegacyV1(ctx context.Context, state State, client composio.Client, c Connection) bool {
	account, err := client.Account(ctx, c.AccountID)
	return err == nil && accountIdentity(account, state, c) && client.DisconnectLegacyV1(ctx, c.AccountID)
}

package connectedapps

import (
	"context"

	"github.com/z-chenhao/assistant/internal/connector/composio"
)

// Original pre-auth-service/default23/direct and frozen pre-opt-in behavior.
// Never called after managed auth failures; delete after named consumers migrate.
func findAuthConfigLegacyV1(ctx context.Context, client composio.Client, toolkit, explicit, saved string, noHosted bool) (composio.AuthConfigSelection, error) {
	if explicit == "" {
		explicit = saved
	}
	value, err := client.FindAuthConfigLegacyV1(ctx, toolkit, explicit)
	if err == nil && value.Readable && value.ID == "" && noHosted {
		return composio.AuthConfigSelection{}, ErrAuthConfigRequired
	}
	return value, err
}

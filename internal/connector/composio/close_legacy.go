package composio

import "context"

// DisconnectLegacyV1 serves explicit pre-close/default23/direct and frozen old
// interpretations only, never managed errors. Delete when those consumers end.
func (c Client) DisconnectLegacyV1(ctx context.Context, id string) bool {
	if !ValidID(id) {
		return false
	}
	revoked := c.RevokeAccount(ctx, id)
	_ = c.DeleteAccount(ctx, id)
	return revoked
}

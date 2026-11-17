package composio

import "context"

// ConnectLegacyV1 retains the pre-session-service/default23/direct protocol
// interpreter and already frozen older requests. No managed error uses it.
// Delete once those explicit consumers have migrated.
func (c Client) ConnectLegacyV1(ctx context.Context, user, toolkit, alias, authConfig string) (Link, error) {
	session, err := c.CreateSession(ctx, user, toolkit, alias, authConfig)
	if err != nil {
		return Link{}, err
	}
	return c.CreateSessionLink(ctx, session, toolkit, alias)
}

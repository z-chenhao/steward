package connectedapps

import (
	"context"
	"errors"
	"github.com/z-chenhao/assistant/internal/connector/composio"
)

// connectNewLegacyV1 serves default23/direct/pre-service graphs and captured
// in-flight selections only. Managed failures never select it. Delete when
// those real consumers no longer require the original workflow.
func (s *Service) connectNewLegacyV1(ctx context.Context, state State, toolkit, alias, authConfig string) (composio.Link, error) {
	client := s.client(state)
	app, err := client.Toolkit(ctx, toolkit)
	if err != nil || app.Slug != toolkit {
		return composio.Link{}, ErrUnavailable
	}
	if app.NoAuth {
		return composio.Link{}, s.commitPublicConnection(ctx, state, toolkit, alias, app)
	}
	selected, err := s.selectAuthConfig(ctx, state, client, toolkit, authConfig, app.ManagedAuth != nil && len(app.ManagedAuth) == 0)
	if err != nil {
		if errors.Is(err, ErrAuthConfigRequired) || errors.Is(err, ErrConflict) {
			return composio.Link{}, err
		}
		return composio.Link{}, ErrUnavailable
	}
	link, err := client.ConnectLegacyV1(ctx, state.UserID, toolkit, alias, selected.ID)
	if err != nil {
		return composio.Link{}, ErrUnavailable
	}
	return s.commitPendingConnection(ctx, state, toolkit, alias, app, link, AuthorizationTTL)
}

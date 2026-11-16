package connectedapps

import (
	"context"
	"github.com/z-chenhao/assistant/internal/connector/composio"
	"github.com/z-chenhao/assistant/internal/plugin/appsdirectory/contract"
)

// detailLegacyV1 preserves live-first/editorial-before-connected precedence
// only for pre-directory and explicit direct/unbundled compositions. Remove
// after those consumers migrate; it cannot handle a managed contribution error.
func (s *Service) detailLegacyV1(ctx context.Context, slug string, state State, editorial contract.Catalogue) (composio.Toolkit, error) {
	var fallback composio.Toolkit
	for _, app := range EditorialToolkits(editorial) {
		if app.Slug == slug {
			fallback = app
			break
		}
	}
	for _, c := range state.Connections {
		if c.Toolkit == slug && fallback.Slug == "" {
			fallback.Slug = slug
			fallback.Name = c.Name
			fallback.NoAuth = c.Public
		}
	}
	if state.KeyRef != "" {
		app, e := s.client(state).Toolkit(ctx, slug)
		if e == nil && app.Slug == slug {
			return app, nil
		}
		if fallback.Slug == "" {
			return composio.Toolkit{}, ErrUnavailable
		}
	}
	if fallback.Slug == "" {
		return fallback, ErrInvalid
	}
	return fallback, nil
}

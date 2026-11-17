package connectedapps

import (
	"context"
	"github.com/z-chenhao/assistant/internal/connector/composio"
)

// connectedToolkit returns an original local fact, never authority to connect,
// use a Tool or bypass live account verification.
func connectedToolkit(state State, slug string) composio.Toolkit {
	for _, c := range state.Connections {
		if c.Toolkit == slug {
			return composio.Toolkit{Slug: slug, Name: c.Name, NoAuth: c.Public}
		}
	}
	return composio.Toolkit{}
}

func (s *Service) Detail(ctx context.Context, slug string) (composio.Toolkit, error) {
	if !composio.ValidID(slug) {
		return composio.Toolkit{}, ErrInvalid
	}
	state, err := s.State(ctx)
	if err != nil {
		return composio.Toolkit{}, err
	}
	selection, err := s.detailSelection(ctx, state.KeyRef != "")
	if err != nil {
		// Directory removal does not hide independent original account controls.
		// No live lookup or old editorial data may replace the failed contribution.
		if fact := connectedToolkit(state, slug); fact.Slug != "" {
			return fact, nil
		}
		return composio.Toolkit{}, err
	}
	if selection.mode == directoryLegacy {
		return s.detailLegacyV1(ctx, slug, state, selection.Catalogue)
	}
	failed := false
	if selection.Lookup.PreferLive {
		item, err := s.client(state).Toolkit(ctx, slug)
		if err == nil && item.Slug == slug {
			return item, nil
		}
		failed = true
		if selection.Lookup.OnLiveError == "unavailable" {
			return composio.Toolkit{}, ErrUnavailable
		}
	}
	var editorial composio.Toolkit
	for _, item := range EditorialToolkits(selection.Catalogue) {
		if item.Slug == slug {
			editorial = item
			break
		}
	}
	connection := connectedToolkit(state, slug)
	first, second := editorial, connection
	if selection.Lookup.LocalFirst == "connection" {
		first, second = connection, editorial
	}
	if first.Slug != "" {
		return first, nil
	}
	if second.Slug != "" {
		return second, nil
	}
	if failed {
		return composio.Toolkit{}, ErrUnavailable
	}
	return composio.Toolkit{}, ErrInvalid
}

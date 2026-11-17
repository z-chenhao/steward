package composio

import (
	"context"
	"errors"
	"net/url"
)

// Exact pre-service/direct interpretation. Current native consumers use this
// only before auth-policy opt-in; never after managed failures. Delete when
// current/direct/inflight pre-service consumers no longer require it.
func (c Client) FindAuthConfigLegacyV1(ctx context.Context, toolkit, override string) (AuthConfigSelection, error) {
	if !ValidID(toolkit) || override != "" && !ValidID(override) {
		return AuthConfigSelection{}, &Error{Status: 400}
	}
	if override != "" {
		var config authConfig
		if err := c.request(ctx, "GET", "/auth_configs/"+override, nil, nil, &config); err != nil {
			return AuthConfigSelection{}, err
		}
		if config.ID != override || config.Toolkit.Slug != toolkit || config.Status != "ENABLED" || !config.Router {
			return AuthConfigSelection{}, &Error{Status: 400}
		}
		return AuthConfigSelection{ID: override, Readable: true}, nil
	}
	var chosen authConfig
	cursor := ""
	seen := map[string]bool{}
	for range 20 {
		var page struct {
			Items []authConfig `json:"items"`
			Next  string       `json:"next_cursor"`
		}
		err := c.request(ctx, "GET", "/auth_configs", url.Values{"toolkit_slug": {toolkit}, "is_composio_managed": {"false"}, "show_disabled": {"false"}, "limit": {"200"}, "cursor": {cursor}}, nil, &page)
		if err != nil {
			// Scoped keys without auth-config read permission can still use
			// Composio-managed sign-in. The link endpoint decides availability.
			var failure *Error
			if errors.As(err, &failure) && (failure.Status == 401 || failure.Status == 403) {
				return AuthConfigSelection{}, nil
			}
			return AuthConfigSelection{}, err
		}
		for _, config := range page.Items {
			if config.Toolkit.Slug == toolkit && !config.Managed && config.Status == "ENABLED" && config.Router && ValidID(config.ID) {
				if chosen.ID == "" || config.Created > chosen.Created || config.Created == chosen.Created && config.ID > chosen.ID {
					chosen = config
				}
			}
		}
		if page.Next == "" {
			return AuthConfigSelection{ID: chosen.ID, Readable: true}, nil
		}
		if seen[page.Next] {
			return AuthConfigSelection{}, &Error{}
		}
		seen[page.Next] = true
		cursor = page.Next
	}
	return AuthConfigSelection{}, &Error{}
}

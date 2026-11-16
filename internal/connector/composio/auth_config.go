package composio

import (
	"context"
	"net/url"
	"strconv"
)

// Only metadata is decoded. Auth config responses can also contain private
// client credentials; those fields must never leave this adapter.
type authConfig struct {
	ID      string `json:"id"`
	Managed bool   `json:"is_composio_managed"`
	Status  string `json:"status"`
	Router  bool   `json:"is_enabled_for_tool_router"`
	Created string `json:"created_at"`
	Toolkit struct {
		Slug string `json:"slug"`
	} `json:"toolkit"`
}

// AuthConfigSelection distinguishes an absent project configuration from a
// scoped key that cannot read it. Neither case contains OAuth credentials.
type AuthConfigSelection struct {
	ID       string
	Readable bool
}

// AuthConfigMetadata is a closed source metadata view. Credentials, raw bodies
// and private OAuth fields never leave the original decoder.
type AuthConfigMetadata struct {
	ID, Created                                                      string
	ToolkitMatch, Enabled, Router, Unmanaged, ValidID, ExplicitMatch bool
}
type AuthConfigPage struct {
	Items []AuthConfigMetadata
	Next  string
}

func authMetadata(value authConfig, toolkit, override string) AuthConfigMetadata {
	return AuthConfigMetadata{ID: value.ID, Created: value.Created, ToolkitMatch: value.Toolkit.Slug == toolkit, Enabled: value.Status == "ENABLED", Router: value.Router, Unmanaged: !value.Managed, ValidID: ValidID(value.ID), ExplicitMatch: value.ID == override}
}
func (c Client) ReadAuthConfig(ctx context.Context, toolkit, id string) (AuthConfigMetadata, error) {
	if !ValidID(toolkit) || !ValidID(id) {
		return AuthConfigMetadata{}, &Error{Status: 400}
	}
	var value authConfig
	if err := c.request(ctx, "GET", "/auth_configs/"+id, nil, nil, &value); err != nil {
		return AuthConfigMetadata{}, err
	}
	return authMetadata(value, toolkit, id), nil
}
func (c Client) ReadAuthConfigPage(ctx context.Context, toolkit, cursor string, limit int) (AuthConfigPage, error) {
	if !ValidID(toolkit) || limit < 1 || limit > 200 {
		return AuthConfigPage{}, &Error{Status: 400}
	}
	var page struct {
		Items []authConfig `json:"items"`
		Next  string       `json:"next_cursor"`
	}
	if err := c.request(ctx, "GET", "/auth_configs", url.Values{"toolkit_slug": {toolkit}, "is_composio_managed": {"false"}, "show_disabled": {"false"}, "limit": {strconv.Itoa(limit)}, "cursor": {cursor}}, nil, &page); err != nil {
		return AuthConfigPage{}, err
	}
	out := AuthConfigPage{Next: page.Next, Items: make([]AuthConfigMetadata, len(page.Items))}
	for i, row := range page.Items {
		out.Items[i] = authMetadata(row, toolkit, "")
	}
	return out, nil
}

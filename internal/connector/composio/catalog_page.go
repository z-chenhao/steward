package composio

import (
	"context"
	"net/url"
	"strconv"
)

// CatalogPage performs one authorized native/non-deprecated catalogue read.
// v3.1 bounds limit at1000 and the opaque cursor already carries its page size.
// https://docs.composio.dev/reference/api-reference/toolkits/getToolkits
// The caller fixes original search/category/cursor and project authority.
func (c Client) CatalogPage(ctx context.Context, search, category, cursor string, limit int) (Catalog, error) {
	if limit < 0 || limit > 1000 || cursor == "" && limit == 0 || cursor != "" && limit != 0 {
		return Catalog{}, &Error{Status: 400}
	}
	query := url.Values{"type": {"native"}, "include_deprecated": {"false"}, "sort_by": {"alphabetically"}, "search": {search}, "category": {category}, "cursor": {cursor}}
	if limit > 0 {
		query.Set("limit", strconv.Itoa(limit))
	}
	var value Catalog
	err := c.request(ctx, "GET", "/toolkits", query, nil, &value)
	return value, err
}

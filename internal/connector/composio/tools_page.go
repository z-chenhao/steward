package composio

import (
	"context"
	"net/url"
	"strconv"
)

// SearchToolsPage performs one original toolkit/query/cursor read. Page size
// is a method recipe, bounded by the v3.1 protocol's documented maximum1000.
// An existing opaque cursor carries its original page size; do not replace it.
// https://docs.composio.dev/reference/api-reference/tools/getTools
func (c Client) SearchToolsPage(ctx context.Context, toolkit, query, cursor string, limit int) (Tools, error) {
	if limit < 0 || limit > 1000 || cursor == "" && limit == 0 || cursor != "" && limit != 0 {
		return Tools{}, &Error{Status: 400}
	}
	count := ""
	if limit > 0 {
		count = strconv.Itoa(limit)
	}
	return c.toolsPage(ctx, toolkit, query, cursor, count)
}

func (c Client) toolsPage(ctx context.Context, toolkit, query, cursor, limit string) (Tools, error) {
	var page Tools
	if !ValidID(toolkit) {
		return page, &Error{Status: 400}
	}
	err := c.request(ctx, "GET", "/tools", url.Values{"toolkit_slug": {toolkit}, "query": {query}, "cursor": {cursor}, "include_deprecated": {"false"}, "toolkit_versions": {"latest"}, "limit": {limit}}, nil, &page)
	return page, err
}

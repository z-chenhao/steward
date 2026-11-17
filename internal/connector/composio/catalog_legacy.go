package composio

import (
	"context"
	"net/url"
)

// CatalogLegacyV1 preserves the original40-item request, including a limit on
// continuation, for pre-directory compositions and the unmigrated owner project
// probe. Managed directory reads never call it or use it as an error fallback.
// Delete after both named consumers migrate; retain the fixed HTTP authority.
func (c Client) CatalogLegacyV1(ctx context.Context, search, category, cursor string) (Catalog, error) {
	var v Catalog
	err := c.request(ctx, "GET", "/toolkits", url.Values{"type": {"native"}, "include_deprecated": {"false"}, "sort_by": {"alphabetically"}, "limit": {"40"}, "search": {search}, "category": {category}, "cursor": {cursor}}, nil, &v)
	return v, err
}

package composio

import "context"

// Exact original recipe for pre-method Runs, explicit prior compositions and
// direct/unbundled Client consumers. Managed methods select their own initial
// count and retain an existing opaque cursor's page size through SearchToolsPage.
// Never a managed-error fallback. Remove after these consumers are inventoried
// and no longer require the original eight-item request interpretation.
func (c Client) SearchTools(ctx context.Context, toolkit, query, cursor string) (Tools, error) {
	return c.toolsPage(ctx, toolkit, query, cursor, "8")
}

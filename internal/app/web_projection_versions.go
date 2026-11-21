package app

import "context"

func (a *App) StaticWebProjectionVersions(ctx context.Context, ids []string) ([]string, bool, error) {
	return a.store.StaticWebProjectionVersions(ctx, ids)
}

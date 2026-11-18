package app

import (
	"context"
	"io"

	agentsqlite "github.com/z-chenhao/assistant/internal/agent/sqlite"
	"github.com/z-chenhao/assistant/internal/rsi"
)

func (a *App) EnableTaskMail(ctx context.Context, source string) (rsi.Status, error) {
	if a.prepareArtifactPlugins != nil {
		if err := a.prepareArtifactPlugins(ctx); err != nil {
			return rsi.Status{}, err
		}
	}
	return a.store.EnableTaskMail(ctx, a.Plugins(), source)
}
func (a *App) BindTaskMailbox(ctx context.Context, ref, domain string) (agentsqlite.TaskMailbox, error) {
	return a.store.BindTaskMailbox(ctx, ref, domain)
}
func (a *App) CloseTaskMailbox(ctx context.Context, address string) error {
	return a.store.CloseTaskMailbox(ctx, address)
}
func (a *App) ReceiveTaskMail(ctx context.Context, address, id string, r io.Reader) (agentsqlite.TaskMailReceipt, error) {
	return a.store.ReceiveTaskMail(ctx, address, id, r)
}

func (a *App) RecentTaskMail(ctx context.Context, ref string, before int64) ([]agentsqlite.TaskMailItem, error) {
	return a.store.RecentTaskMail(ctx, ref, before)
}

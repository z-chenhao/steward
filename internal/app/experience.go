package app

import (
	"context"

	"github.com/z-chenhao/assistant/internal/agent"
	"github.com/z-chenhao/assistant/internal/skill"
)

func (a *App) ReviewExperience(ctx context.Context, q skill.ReviewRequest) (map[string]any, error) {
	if a.skillConfig == nil {
		return nil, agent.ErrNotFound
	}
	runs := a.store.Runs(a.history, nil)
	runs.ConfigurePlugins(a.Plugins().Catalog)
	return runs.ReviewExperience(ctx, *a.skillConfig, q)
}

package app

import (
	"context"

	"github.com/z-chenhao/assistant/internal/agent"
	"github.com/z-chenhao/assistant/internal/artifact"
	"github.com/z-chenhao/assistant/internal/coding"
)

func (a *App) CodeReport(ctx context.Context, ref string, withDiff bool) (coding.Report, error) {
	return a.store.CodeReport(ctx, ref, withDiff)
}

func (a *App) SaveCodeReport(ctx context.Context, ref string) (artifact.Descriptor, error) {
	if a.prepareArtifactPlugins != nil {
		if err := a.prepareArtifactPlugins(ctx); err != nil {
			return artifact.Descriptor{}, err
		}
	}
	return a.store.SaveCodeReport(ctx, ref)
}

func (a *App) SaveCodePRReport(ctx context.Context, ref, head string) (artifact.Descriptor, error) {
	if a.github == nil {
		return artifact.Descriptor{}, agent.ErrConflict
	}
	if a.prepareArtifactPlugins != nil {
		if err := a.prepareArtifactPlugins(ctx); err != nil {
			return artifact.Descriptor{}, err
		}
	}
	return a.store.SaveCodePRReport(ctx, ref, a.github.Config, head)
}

func (a *App) SaveCodePRExactReport(ctx context.Context, ref, head, source string) (artifact.Descriptor, error) {
	if a.github == nil {
		return artifact.Descriptor{}, agent.ErrConflict
	}
	if a.prepareArtifactPlugins != nil {
		if err := a.prepareArtifactPlugins(ctx); err != nil {
			return artifact.Descriptor{}, err
		}
	}
	return a.store.SaveCodePRExactReport(ctx, ref, a.github.Config, head, source)
}

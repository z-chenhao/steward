package app

import (
	"context"
	"github.com/z-chenhao/assistant/internal/observer/github"

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

// A coding report identifies the Task. Multiple configured PRs must resolve
// to one active association before reading its frozen head.
func (a *App) codeReportWatch(ctx context.Context, ref string) (*github.Service, error) {
	if len(a.githubWatches) == 1 {
		return a.githubWatch(a.defaultGitHubScope())
	}
	var selected *github.Service
	for _, watch := range a.githubWatches {
		binding, err := a.store.GitHubTaskBinding(ctx, watch.Config)
		if err != nil {
			return nil, err
		}
		if binding.State == "bound" && binding.TaskRef == ref {
			if selected != nil {
				return nil, agent.ErrConflict
			}
			selected = watch
		}
	}
	if selected == nil {
		return nil, agent.ErrConflict
	}
	return selected, nil
}
func (a *App) SaveCodePRReport(ctx context.Context, ref, head string) (artifact.Descriptor, error) {
	watch, err := a.codeReportWatch(ctx, ref)
	if err != nil {
		return artifact.Descriptor{}, err
	}
	if a.prepareArtifactPlugins != nil {
		if err := a.prepareArtifactPlugins(ctx); err != nil {
			return artifact.Descriptor{}, err
		}
	}
	return a.store.SaveCodePRReport(ctx, ref, watch.Config, head)
}

func (a *App) SaveCodePRExactReport(ctx context.Context, ref, head, source string) (artifact.Descriptor, error) {
	watch, err := a.codeReportWatch(ctx, ref)
	if err != nil {
		return artifact.Descriptor{}, err
	}
	if a.prepareArtifactPlugins != nil {
		if err := a.prepareArtifactPlugins(ctx); err != nil {
			return artifact.Descriptor{}, err
		}
	}
	return a.store.SaveCodePRExactReport(ctx, ref, watch.Config, head, source)
}

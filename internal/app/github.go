package app

import (
	"context"
	"errors"

	"github.com/z-chenhao/assistant/internal/observer/github"
)

func (a *App) GitHubStatus(ctx context.Context) (github.Status, error) {
	if a.github == nil {
		return github.Status{State: "not_configured"}, nil
	}
	return a.store.GitHubStatus(ctx, a.github.Config)
}

func (a *App) PollGitHub(ctx context.Context) error {
	if a.github == nil {
		return errors.New("github configuration is required")
	}
	if a.prepareGitHubPlugins != nil {
		if err := a.prepareGitHubPlugins(ctx); err != nil {
			return err
		}
	}
	return a.github.Poll(ctx)
}

func (a *App) SetGitHubWatch(ctx context.Context, enabled bool) error {
	if a.github == nil {
		return errors.New("github configuration is required")
	}
	return a.store.SetGitHubWatch(ctx, a.github.Config, enabled)
}

func (a *App) GitHubObservations(ctx context.Context, before int64) ([]github.Observation, error) {
	if a.github == nil {
		return nil, errors.New("github configuration is required")
	}
	return a.store.GitHubObservations(ctx, a.github.Config, before)
}

func (a *App) BindGitHubTask(ctx context.Context, q github.BindingRequest, enabled bool) (github.TaskBinding, error) {
	if a.github == nil {
		return github.TaskBinding{}, errors.New("github configuration is required")
	}
	if err := a.store.BindGitHubTask(ctx, a.github.Config, q, enabled); err != nil {
		return github.TaskBinding{}, err
	}
	return a.store.GitHubTaskBinding(ctx, a.github.Config)
}

func (a *App) GitHubTaskBinding(ctx context.Context) (github.TaskBinding, error) {
	if a.github == nil {
		return github.TaskBinding{State: "not_configured", Mode: "off"}, nil
	}
	return a.store.GitHubTaskBinding(ctx, a.github.Config)
}

func (a *App) GitHubRuleDecisions(ctx context.Context, before int64) ([]github.RuleDecision, error) {
	if a.github == nil {
		return nil, errors.New("github configuration is required")
	}
	return a.store.GitHubRuleDecisions(ctx, a.github.Config, before)
}

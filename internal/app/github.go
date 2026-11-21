package app

import (
	"context"
	"errors"
	"maps"
	"slices"

	"github.com/z-chenhao/assistant/internal/observer/github"
)

var errGitHubScope = errors.New("select a configured exact GitHub PR")

func (a *App) GitHubScopes() []string {
	return slices.Sorted(maps.Keys(a.githubWatches))
}

func (a *App) githubWatch(scope string) (*github.Service, error) {
	watch := a.githubWatches[scope]
	if watch == nil {
		return nil, errGitHubScope
	}
	return watch, nil
}

func (a *App) configuredGitHubWatches() []github.Config {
	watches := make([]github.Config, 0, len(a.githubWatches))
	for _, watch := range a.githubWatches {
		watches = append(watches, watch.Config)
	}
	return watches
}

func (a *App) defaultGitHubScope() string {
	scopes := a.GitHubScopes()
	if len(scopes) == 1 {
		return scopes[0]
	}
	return ""
}
func (a *App) GitHubStatus(ctx context.Context) (github.Status, error) {
	if len(a.GitHubScopes()) == 0 {
		return github.Status{State: "not_configured"}, nil
	}
	return a.GitHubStatusFor(ctx, a.defaultGitHubScope())
}
func (a *App) GitHubStatusFor(ctx context.Context, scope string) (github.Status, error) {
	w, err := a.githubWatch(scope)
	if err != nil {
		return github.Status{}, err
	}
	return a.store.GitHubStatus(ctx, w.Config)
}
func (a *App) PollGitHub(ctx context.Context) error {
	return a.PollGitHubFor(ctx, a.defaultGitHubScope())
}
func (a *App) PollGitHubFor(ctx context.Context, scope string) error {
	w, err := a.githubWatch(scope)
	if err != nil {
		return err
	}
	if a.prepareGitHubPlugins != nil {
		if err = a.prepareGitHubPlugins(ctx); err != nil {
			return err
		}
	}
	return w.Poll(ctx)
}
func (a *App) SetGitHubWatch(ctx context.Context, enabled bool) error {
	return a.SetGitHubWatchFor(ctx, a.defaultGitHubScope(), enabled)
}
func (a *App) SetGitHubWatchFor(ctx context.Context, scope string, enabled bool) error {
	w, err := a.githubWatch(scope)
	if err != nil {
		return err
	}
	return a.store.SetGitHubWatch(ctx, w.Config, enabled)
}
func (a *App) GitHubObservations(ctx context.Context, before int64) ([]github.Observation, error) {
	return a.GitHubObservationsFor(ctx, a.defaultGitHubScope(), before)
}
func (a *App) GitHubObservationsFor(ctx context.Context, scope string, before int64) ([]github.Observation, error) {
	w, err := a.githubWatch(scope)
	if err != nil {
		return nil, err
	}
	return a.store.GitHubObservations(ctx, w.Config, before)
}
func (a *App) BindGitHubTask(ctx context.Context, q github.BindingRequest, enabled bool) (github.TaskBinding, error) {
	return a.BindGitHubTaskFor(ctx, a.defaultGitHubScope(), q, enabled)
}
func (a *App) BindGitHubTaskFor(ctx context.Context, scope string, q github.BindingRequest, enabled bool) (github.TaskBinding, error) {
	w, err := a.githubWatch(scope)
	if err != nil {
		return github.TaskBinding{}, err
	}
	if err = a.store.BindGitHubTaskIn(ctx, w.Config, a.configuredGitHubWatches(), q, enabled); err != nil {
		return github.TaskBinding{}, err
	}
	return a.store.GitHubTaskBinding(ctx, w.Config)
}
func (a *App) GitHubTaskBinding(ctx context.Context) (github.TaskBinding, error) {
	if len(a.GitHubScopes()) == 0 {
		return github.TaskBinding{State: "not_configured", Mode: "off"}, nil
	}
	return a.GitHubTaskBindingFor(ctx, a.defaultGitHubScope())
}
func (a *App) GitHubTaskBindingFor(ctx context.Context, scope string) (github.TaskBinding, error) {
	w, err := a.githubWatch(scope)
	if err != nil {
		return github.TaskBinding{}, err
	}
	return a.store.GitHubTaskBinding(ctx, w.Config)
}
func (a *App) GitHubRuleDecisions(ctx context.Context, before int64) ([]github.RuleDecision, error) {
	return a.GitHubRuleDecisionsFor(ctx, a.defaultGitHubScope(), before)
}
func (a *App) GitHubRuleDecisionsFor(ctx context.Context, scope string, before int64) ([]github.RuleDecision, error) {
	w, err := a.githubWatch(scope)
	if err != nil {
		return nil, err
	}
	return a.store.GitHubRuleDecisions(ctx, w.Config, before)
}

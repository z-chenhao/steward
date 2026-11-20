package web

import (
	"context"
	"net/http"
	"net/url"
	"slices"
	"strconv"

	"github.com/z-chenhao/assistant/internal/agent"
	"github.com/z-chenhao/assistant/internal/observer/github"
	"github.com/z-chenhao/assistant/internal/task"
)

type githubRuleControl interface {
	GitHubScopes() []string
	GitHubStatusFor(context.Context, string) (github.Status, error)
	GitHubTaskBindingFor(context.Context, string) (github.TaskBinding, error)
	GitHubRuleDecisionsFor(context.Context, string, int64) ([]github.RuleDecision, error)
	BindGitHubTaskFor(context.Context, string, github.BindingRequest, bool) (github.TaskBinding, error)
}

type githubScopeLink struct{ Scope, URL string }
type githubRuleResult struct{ Event, Reason string }
type githubRuleView struct {
	Links                                                                      []githubScopeLink
	Scope, Mode, Event                                                         string
	Version                                                                    int64
	Ready, Unavailable, OtherTask, CanStart, CanEnable, CanStop, IssueComments bool
	Decisions                                                                  []githubRuleResult
}

func githubRuleURL(ref, scope string) string {
	return taskURL(ref) + "?" + url.Values{"github_scope": {scope}}.Encode() + "#github-rule"
}

// Read local, authorized facts for one PR. Opening a task has no external effect.
func (s *Server) loadGitHubRule(ctx context.Context, p *page, selected string) error {
	if p.Path == "" || p.Workbench == nil || !p.Workbench.Presentation.Policy.GitHubRules {
		return nil
	}
	core, ok := s.Core.(githubRuleControl)
	if !ok {
		return nil
	}
	scopes := core.GitHubScopes()
	if selected != "" && !slices.Contains(scopes, selected) {
		return task.ErrInvalid
	}
	if len(scopes) == 0 {
		return nil
	}
	d := p.Tasks.Detail
	v := &githubRuleView{}
	p.Tasks.GitHubRule = v
	for _, scope := range scopes {
		v.Links = append(v.Links, githubScopeLink{scope, githubRuleURL(d.TaskRef, scope)})
	}
	if selected == "" {
		if len(scopes) != 1 {
			return nil
		}
		selected = scopes[0]
	}
	v.Scope = selected
	status, err := core.GitHubStatusFor(ctx, selected)
	if err != nil {
		v.Unavailable = true
		return nil
	}
	b, err := core.GitHubTaskBindingFor(ctx, selected)
	if err != nil {
		v.Unavailable = true
		return nil
	}
	v.Ready = status.Enabled && status.RepositoryID != nil && status.FailureCode == "" && status.State != "privacy_stopped"
	v.OtherTask = (b.State == "bound" || b.State == "stale") && b.TaskRef != d.TaskRef
	if v.OtherTask {
		return nil
	}
	v.Version = b.Version
	v.CanStop = b.TaskRef == d.TaskRef && b.State == "bound" && b.TaskEpoch == d.Epoch
	v.CanStart = v.Ready && !v.CanStop && (d.Status == "active" || d.Status == "completed")
	v.IssueComments = (github.BindingRequest{Mode: "observe", EventKind: "issue_comment"}).ValidRule()
	if v.CanStop {
		v.Mode, v.Event = b.Mode, b.EventKind
		if b.CheckConclusion == "failure" {
			v.Event = "failed_check"
		}
		v.CanEnable = v.Ready && b.Mode == "observe" && b.EventKind != ""
	}
	if b.TaskRef != d.TaskRef {
		return nil
	}
	decisions, err := core.GitHubRuleDecisionsFor(ctx, selected, 0)
	if err != nil {
		v.Unavailable = true
		return nil
	}
	for _, decision := range decisions {
		if decision.TaskRef == d.TaskRef && decision.Generation == b.Generation {
			v.Decisions = append(v.Decisions, githubRuleResult{decision.Kind, decision.Reason})
			if len(v.Decisions) == 5 {
				break
			}
		}
	}
	return nil
}

func (s *Server) changeGitHubRule(w http.ResponseWriter, r *http.Request) error {
	ctx := r.Context()
	core, ok := s.Core.(githubRuleControl)
	if !ok || s.WebWorkbench == nil || s.Tasks == nil {
		return errWorkbenchUnavailable
	}
	f := r.PostForm
	for key, vals := range f {
		if len(vals) != 1 || !slices.Contains([]string{"scope", "task_ref", "epoch", "version", "workbench_revision", "action", "event"}, key) {
			return task.ErrInvalid
		}
	}
	revision, ref, scope := f.Get("workbench_revision"), f.Get("task_ref"), f.Get("scope")
	if !task.ValidRef(ref) || revision == "" || !validModelReader(revision) || !slices.Contains(core.GitHubScopes(), scope) {
		return task.ErrInvalid
	}
	out, current, managed, err := s.WebWorkbench(ctx, "")
	if err != nil || !managed || current != revision || !out.Policy.GitHubRules {
		return agent.ErrConflict
	}
	epoch, err := strconv.ParseInt(f.Get("epoch"), 10, 64)
	if err != nil || epoch <= 0 {
		return task.ErrInvalid
	}
	version, err := strconv.ParseInt(f.Get("version"), 10, 64)
	if err != nil || version < 0 {
		return task.ErrInvalid
	}
	d, err := s.Tasks.DetailPage(ctx, ref, "", 0)
	if err != nil {
		return err
	}
	if d.TaskRef != ref || d.Epoch != epoch {
		return agent.ErrConflict
	}
	b, err := core.GitHubTaskBindingFor(ctx, scope)
	if err != nil {
		return err
	}
	if b.Version != version || (b.State == "bound" || b.State == "stale") && b.TaskRef != ref {
		return agent.ErrConflict
	}
	q := github.BindingRequest{TaskRef: ref, TaskEpoch: epoch, Version: version}
	action := f.Get("action")
	switch action {
	case "observe":
		if b.State == "bound" {
			return agent.ErrConflict
		}
		q.Mode, q.EventKind = "observe", f.Get("event")
		if q.EventKind == "failed_check" {
			q.EventKind, q.CheckConclusion = "check_run", "failure"
		}
		if !q.ValidRule() {
			return task.ErrInvalid
		}
	case "enable", "stop":
		if b.TaskRef != ref || b.TaskEpoch != epoch || b.State != "bound" {
			return agent.ErrConflict
		}
		if action == "enable" {
			if b.Mode != "observe" || b.EventKind == "" {
				return agent.ErrConflict
			}
			q.Mode, q.EventKind, q.CheckConclusion = "automatic", b.EventKind, b.CheckConclusion
		}
	default:
		return task.ErrInvalid
	}
	if _, err = core.BindGitHubTaskFor(ctx, scope, q, action != "stop"); err != nil {
		return err
	}
	http.Redirect(w, r, githubRuleURL(ref, scope), http.StatusSeeOther)
	return nil
}

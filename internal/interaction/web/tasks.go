package web

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/z-chenhao/assistant/internal/task"
)

type TaskReader interface {
	List(context.Context, task.ListQuery) (task.List, error)
	DetailPage(context.Context, string, string, int64) (task.Detail, error)
}
type executionReader interface {
	Executions(context.Context, task.ListQuery) (task.ExecutionList, error)
	Execution(context.Context, string) (task.ExecutionDetail, error)
}
type shownArtifact struct {
	Ref, Name   string
	Previewable bool
}
type scheduledTaskReader interface {
	ScheduledPage(context.Context, string, string) ([]task.ScheduledWork, string, error)
}
type taskView struct {
	GitHubRule           *githubRuleView
	Configurations       bool
	Executions           task.ExecutionList
	Execution            *task.ExecutionDetail
	Query                task.ListQuery
	List                 task.List
	Detail               *task.Detail
	Messages             []shownMessage
	Approvals            []approvalView
	Artifacts            []shownArtifact
	Scheduled            []task.ScheduledWork
	ScheduledNext        string
	SchedulesUnavailable bool
}

func executionURL(id string) string { return "/tasks/executions/" + url.PathEscape(id) }
func taskURL(ref string) string     { return "/tasks/" + url.PathEscape(ref) }
func conversationURL(sequence int64) string {
	if sequence <= 0 {
		return "/"
	}
	return "/?before=" + strconv.FormatInt(sequence+1, 10) + "#message-" + strconv.FormatInt(sequence, 10)
}
func tasksQuery(r *http.Request) (task.ListQuery, error) {
	q := task.ListQuery{View: "all"}
	for key, values := range r.URL.Query() {
		if len(values) != 1 {
			return q, task.ErrInvalid
		}
		switch key {
		case "view":
			q.View = values[0]
		case "q":
			q.Query = values[0]
		case "needs_me":
			if values[0] != "1" {
				return q, task.ErrInvalid
			}
			q.NeedsMe = true
		case "cursor":
			q.Cursor = values[0]
		case "schedules_after":
			if r.URL.Path != "/tasks/configurations" || len(values[0]) > 256 {
				return q, task.ErrInvalid
			}
		case "feedback_before":
			if len(values[0]) > 256 {
				return q, task.ErrInvalid
			}
		default:
			return q, task.ErrInvalid
		}
	}
	if q.View == "" {
		q.View = "all"
	}
	if r.URL.Query().Get("schedules_after") != "" && q.Cursor != "" {
		return q, task.ErrInvalid
	}
	return q, q.Validate()
}
func taskListURL(v *taskView, field, value string) string {
	q := url.Values{}
	if v != nil {
		q.Set("view", v.Query.View)
		q.Set("q", v.Query.Query)
		if v.Query.NeedsMe {
			q.Set("needs_me", "1")
		}
	}
	q.Set(field, value)
	if field != "cursor" {
		q.Del("cursor")
	}
	path := "/tasks"
	if v != nil && v.Configurations {
		path = "/tasks/configurations"
	}
	return path + "?" + q.Encode()
}
func (s *Server) loadTasks(ctx context.Context, r *http.Request, p *page) error {
	if s.Tasks == nil {
		return errors.New("task workspace unavailable")
	}
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	v := &taskView{}
	p.Tasks = v
	if r.URL.Path == "/tasks/attention" {
		s.loadAttention(ctx, p, "", r.URL.Query().Get("feedback_before"), nil)
		return nil
	}
	if strings.HasPrefix(r.URL.Path, "/tasks/executions/") {
		reader, ok := s.Tasks.(executionReader)
		if !ok {
			return errors.New("execution view unavailable")
		}
		if len(r.URL.Query()) > 0 {
			return task.ErrInvalid
		}
		detail, err := reader.Execution(ctx, strings.TrimPrefix(r.URL.Path, "/tasks/executions/"))
		if err != nil {
			return err
		}
		v.Execution = &detail
		for _, m := range detail.Messages {
			shown, e := s.showMessage(ctx, m, nil)
			if e != nil {
				return e
			}
			v.Messages = append(v.Messages, shown)
		}
		return nil
	}
	if r.URL.Path == "/tasks" || r.URL.Path == "/progress" || r.URL.Path == "/tasks/configurations" {
		q, err := tasksQuery(r)
		if err != nil {
			return err
		}
		v.Query = q
		v.Configurations = r.URL.Path == "/tasks/configurations"
		if v.Configurations {
			q.View = "all"
			q.NeedsMe = false
			v.Query.NeedsMe = false
			v.Query.View = "all"
			after := r.URL.Query().Get("schedules_after")
			if after == "" {
				v.List, err = s.Tasks.List(ctx, q)
				if err != nil {
					return err
				}
			}
			if reader, ok := s.Tasks.(scheduledTaskReader); ok && q.Cursor == "" {
				v.Scheduled, v.ScheduledNext, err = reader.ScheduledPage(ctx, q.Query, after)
				if err != nil {
					v.SchedulesUnavailable = true
					v.Scheduled = nil
					err = nil
				}
			}
		} else {
			reader, ok := s.Tasks.(executionReader)
			if !ok {
				return errors.New("execution list unavailable")
			}
			v.Executions, err = reader.Executions(ctx, q)
		}

		return err
	}
	ref := strings.TrimPrefix(r.URL.Path, "/tasks/")
	for key, values := range r.URL.Query() {
		if (key != "history" && key != "delivery_before" && key != "github_scope") || len(values) != 1 {
			return task.ErrInvalid
		}
	}
	var before int64
	if raw := r.URL.Query().Get("delivery_before"); raw != "" {
		var err error
		before, err = strconv.ParseInt(raw, 10, 64)
		if err != nil || before <= 0 {
			return task.ErrInvalid
		}
	}
	d, err := s.Tasks.DetailPage(ctx, ref, r.URL.Query().Get("history"), before)
	if err != nil {
		return err
	}
	v.Detail = &d
	if err := s.loadGitHubRule(ctx, p, r.URL.Query().Get("github_scope")); err != nil {
		return err
	}
	for _, ref := range d.Artifacts {
		descriptor, e := s.Core.DescribeArtifact(ctx, ref)
		if e == nil {
			v.Artifacts = append(v.Artifacts, shownArtifact{Ref: ref, Name: descriptor.Name, Previewable: artifactPreviewType(descriptor) != ""})
		}
	}
	if len(d.ApprovalIDs) > 0 {
		approvals, e := s.pageApprovals(ctx, d.ApprovalIDs, false)
		if e != nil {
			return e
		}
		wanted := map[string]bool{}
		for _, id := range d.ApprovalIDs {
			wanted[id] = true
		}
		for _, a := range approvals {
			if wanted[a.ID] && a.Status == "pending" {
				card := approvalFor(a)
				card.ReturnTo = taskURL(d.TaskRef)
				v.Approvals = append(v.Approvals, card)
			}
		}
	}
	for _, m := range d.Messages {
		shown, err := s.showMessage(ctx, m, nil)
		if err != nil {
			return err
		}
		v.Messages = append(v.Messages, shown)
	}
	s.loadAttention(ctx, p, ref, "", nil)
	return nil
}

package web

import (
	"errors"
	"net/http"
	"net/url"
	"strings"

	"github.com/z-chenhao/assistant/internal/memory"
	"github.com/z-chenhao/assistant/internal/observability"
	"github.com/z-chenhao/assistant/internal/task"
)

func pageRoute(path string) (tab, view, title string) {
	switch {
	case path == "/settings/meeting-preparation":
		return "/settings", "meeting-preparation", "Meeting preparation"
	case path == "/settings/apps/service":
		return "/settings", "app-service", "App connection service"
	case path == "/settings/apps":
		return "/settings", "apps", "Apps"
	case path == "/":
		return "/", "conversation", "Conversation"
	case path == "/tasks/attention":
		return "/tasks", "attention", "Needs your attention"
	case path == "/tasks/configurations":
		return "/tasks", "tasks", "Configurations"
	case path == "/tasks" || path == "/progress":
		return "/tasks", "tasks", "Execution records"
	case strings.HasPrefix(path, "/tasks/executions/") && pageLeaf(strings.TrimPrefix(path, "/tasks/executions/")):
		return "/tasks", "execution", "Execution record"
	case path == "/tasks/schedules/new" || strings.HasPrefix(path, "/tasks/schedules/automation:") && pageLeaf(strings.TrimPrefix(path, "/tasks/schedules/")):
		return "/tasks", "schedule", "Schedule settings"
	case strings.HasPrefix(path, "/tasks/") && task.ValidRef(strings.TrimPrefix(path, "/tasks/")):
		return "/tasks", "task", "Task"
	case path == "/settings/capabilities":
		return "/settings", "capabilities", "Capabilities"
	case strings.HasPrefix(path, "/settings/capabilities/") && validCapability(strings.TrimPrefix(path, "/settings/capabilities/")):
		return "/settings", "capabilities", "Capability settings"
	case path == "/settings/preferences":
		return "/settings", "settings", "Preferences"
	case path == "/settings/setup" || path == "/settings" || path == "/settings/models":
		return "/settings", "settings", "Models"
	case path == "/settings/notifications":
		return "/settings", "settings", "Notifications"
	case path == "/settings/models/new":
		return "/settings", "settings", "Connect with an API key"
	case strings.HasPrefix(path, "/settings/models/") && pageLeaf(strings.TrimPrefix(path, "/settings/models/")):
		return "/settings", "settings", "Model details"
	case path == "/developer/observability":
		return "/developer/observability", "traces", "Traces"
	case path == "/developer/memory":
		return "/developer/memory", "memory", "Memory"
	case strings.HasPrefix(path, "/developer/observability/") && pageLeaf(strings.TrimPrefix(path, "/developer/observability/")) || path == "/developer/context":
		return "/developer/observability", "trace", "Trace details"
	}
	return "", "", ""
}
func safePageReturn(raw string) string {
	u, err := url.Parse(raw)
	if err != nil || u.IsAbs() || u.Host != "" || !strings.HasPrefix(u.Path, "/") || strings.HasPrefix(u.Path, "//") || strings.ContainsAny(raw, "\r\n\\") {
		return "/"
	}
	if _, view, _ := pageRoute(u.Path); view == "" {
		return "/"
	}
	return u.String()
}
func (s *Server) pageError(w http.ResponseWriter, r *http.Request, err error) {
	code := 503
	message := "This page is temporarily unavailable. Your work has not been changed."
	if errors.Is(err, errPageNotFound) || errors.Is(err, task.ErrNotFound) || errors.Is(err, observability.ErrNotFound) || errors.Is(err, memory.ErrInspectionNotFound) {
		code = 404
		message = "This item could not be found in your workspace."
	}
	if errors.Is(err, task.ErrInvalid) || errors.Is(err, observability.ErrInvalid) || errors.Is(err, memory.ErrInspectionInvalid) {
		code = 400
		message = "This page link is invalid. Return to the list and choose an item."
	}
	tab, _, title := pageRoute(r.URL.Path)
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(code)
	s.render(w, r, page{Tab: tab, View: "error", Title: title, Path: r.URL.Path, Error: message})
}

// Old public bookmarks redirect to the single canonical view; no parallel UI.
func legacyPageRedirect(w http.ResponseWriter, r *http.Request) bool {
	target := ""
	switch r.URL.Path {
	case "/settings", "/settings/setup":
		target = "/settings/models"
	case "/settings/services/apps":
		target = "/settings/apps/service"
	case "/progress":
		target = "/tasks"
	case "/developer/context":
		if _, err := contextQuery(r); err != nil {
			return false
		}
		q := r.URL.Query()
		run := q.Get("run")
		q.Del("run")
		q.Set("tab", "context")
		q.Set("ctx", "1")
		target = tracePath(run) + "?" + q.Encode()
	case "/developer/observability":
		if _, err := traceQuery(r); err != nil {
			return false
		}
		if run := r.URL.Query().Get("run"); run != "" {
			q := r.URL.Query()
			q.Del("run")
			target = tracePath(run) + "?" + q.Encode()
		}
	}
	if target == "" {
		return false
	}
	if (r.URL.Path == "/settings/setup" || r.URL.Path == "/settings/services/apps" || r.URL.Path == "/settings") && r.URL.RawQuery != "" {
		target += "?" + r.URL.RawQuery
	}
	http.Redirect(w, r, target, http.StatusSeeOther)
	return true
}

func pageLeaf(value string) bool {
	return value != "" && len(value) <= 256 && !strings.ContainsAny(value, "/\\\r\n\x00")
}

// Task definitions and schedule editors belong to configuration; dispatched
// rounds and the attention inbox retain the execution category.
func taskConfigurationPage(path string) bool {
	return path == "/tasks/configurations" || strings.HasPrefix(path, "/tasks/schedules/") || strings.HasPrefix(path, "/tasks/") && task.ValidRef(strings.TrimPrefix(path, "/tasks/"))
}

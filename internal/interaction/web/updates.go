package web

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	modelsettings "github.com/z-chenhao/assistant/internal/plugin/modelconnections/contract"
	"html/template"
	"net/http"
	"net/url"
	"strings"
)

// updates is a view of committed product data, not an event log or a second
// execution channel. Authentication and binding revocation run before every
// request, including conditional requests. No composer/form identity is returned.
func (s *Server) updates(w http.ResponseWriter, r *http.Request) {
	p := page{Tab: r.URL.Query().Get("view")}
	fragments := map[string]string{"workspace-status": "status"}
	switch {
	case p.Tab == "/":
		fragments["conversation-messages"], fragments["conversation-approvals"] = "conversation", "approvals"
		fragments["conversation-attention"] = "attention-summary"
	case p.Tab == "/tasks/attention":
		fragments["tasks-attention"] = "tasks-attention"
	case p.Tab == "/tasks" || p.Tab == "/tasks/configurations":
		fragments["ongoing-work"] = "tasks-list"
	case strings.HasPrefix(p.Tab, "/tasks/executions/"):
		fragments["execution-header"], fragments["execution-progress"], fragments["task-messages"] = "execution-header", "execution-progress", "execution-result"
		fragments["execution-side"] = "execution-side"
	case strings.HasPrefix(p.Tab, "/tasks/"):
		fragments["task-header"] = "task-header"
		fragments["task-feedback"] = "task-feedback"
		fragments["task-progress"] = "task-progress"
		fragments["task-decisions"] = "task-decisions"
		fragments["task-messages"] = "task-messages"
		fragments["task-milestones"] = "task-milestones"
	case strings.HasPrefix(p.Tab, "/settings/"):
		fragments["signin-status"] = "signin"
	default:
		fail(w, 400)
		return
	}

	// Versioned clients pin the presentation before any private facts are loaded.
	// Headerless requests retain the published pre-workbench updates protocol.
	if r.Header.Get("X-Steward-Workbench-Reader") != "" {
		switch {
		case p.Tab == "/":
			p.View = "conversation"
		case p.Tab == "/tasks/attention":
			p.View = "attention"
		case p.Tab == "/tasks" || p.Tab == "/tasks/configurations":
			p.View = "tasks"
		case strings.HasPrefix(p.Tab, "/tasks/executions/"):
			p.View = "execution"
		case strings.HasPrefix(p.Tab, "/tasks/"):
			p.View = "task"
		case strings.HasPrefix(p.Tab, "/settings/"):
			p.View = "settings"
		}
		if err := s.loadWorkbench(r.Context(), r, &p); err != nil || p.Workbench == nil {
			fail(w, 503)
			return
		}
	}

	var chat conditionalChat
	var err error
	w.Header().Set("Vary", "Cookie, Authorization, X-Steward-Workbench-Reader")
	if p.Workbench != nil {
		w.Header().Set("X-Steward-Workbench-Reader", p.Workbench.ReaderRevision)
	}
	if p.Cursor, err = conversationCursor(r); err != nil || p.Cursor != 0 && p.Tab != "/" {
		fail(w, 400)
		return
	}
	if p.Status, err = s.Core.WebStatus(r.Context()); err != nil {
		fail(w, 503)
		return
	}
	switch {
	case p.Tab == "/":
		var unchanged bool
		unchanged, err = s.loadConditionalChat(r, &p, &chat)
		if unchanged {
			w.Header().Set("ETag", r.Header.Get("If-None-Match"))
			w.WriteHeader(http.StatusNotModified)
			return
		}
	case p.Tab == "/tasks" || strings.HasPrefix(p.Tab, "/tasks/"):
		copy := r.Clone(r.Context())
		u := *r.URL
		u.Path = p.Tab
		q := url.Values{}
		for _, key := range []string{"q", "cursor", "history", "delivery_before", "schedules_after", "feedback_before", "needs_me"} {
			if value := r.URL.Query().Get(key); value != "" {
				q.Set(key, value)
			}
		}
		if value := r.URL.Query().Get("task_view"); value != "" {
			q.Set("view", value)
		}
		u.RawQuery = q.Encode()
		copy.URL = &u
		err = s.loadTasks(r.Context(), copy, &p)
	default:
		s.loadSignIn(&p, p.Tab)
		if p.Tab == "/settings/models" || strings.HasPrefix(p.Tab, "/settings/models/") {
			var out modelsettings.SettingsProjection
			out, err = s.projectModelSettings(r.Context(), modelsettings.SettingsFacts{Rows: []modelsettings.SettingsRow{}, WithView: true})
			p.Settings = &settingsView{View: out.View, Copy: out.Copy}
		}
	}

	if err != nil {
		fail(w, 503)
		return
	}
	layout := requestLayout(r)
	if p.Workbench != nil {
		layout, err = workbenchPageLayout(p, r)
		if err != nil {
			fail(w, 503)
			return
		}
		kind := p.View
		if p.Tab == "/tasks/configurations" {
			kind = "configurations"
		}
		selected := map[string]string{}
		for _, entry := range p.Workbench.Presentation.Policy.Live {
			if entry.Kind == kind {
				for _, id := range entry.Slots {
					if name, ok := fragments[id]; ok {
						selected[id] = name
					}
				}
			}
		}
		fragments = selected

	}
	regions := map[string]string{}
	for id, name := range fragments {
		// Other Settings categories do not own an authentication region. Avoid
		// invoking the historical Models interpreter for their empty placeholder.
		if name == "signin" && p.Tab != "/settings/models" && !strings.HasPrefix(p.Tab, "/settings/models/") {
			regions[id] = ""
			continue
		}
		var html bytes.Buffer
		if name == "signin" && p.Settings != nil && p.Settings.View != nil {
			var fragment template.HTML
			fragment, err = renderModelView(p, r, name)
			if err == nil {
				html.WriteString(string(fragment))
			}
		} else {
			err = layout.ExecuteTemplate(&html, name, p)
		}
		if err != nil {
			fail(w, 500)
			return
		}
		regions[id] = html.String()
	}
	body, err := json.Marshal(struct {
		View    string            `json:"view"`
		Before  int64             `json:"before"`
		Latest  int64             `json:"latest"`
		Regions map[string]string `json:"regions"`
	}{p.Tab, p.Cursor, p.Latest, regions})
	if err != nil {
		fail(w, 500)
		return
	}
	etag := fmt.Sprintf(`"%x"`, sha256.Sum256(body))
	w.Header().Set("ETag", etag)
	s.rememberChat(r.Context(), chat, etag)
	if r.Header.Get("If-None-Match") == etag {
		w.WriteHeader(http.StatusNotModified)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write(body)
}

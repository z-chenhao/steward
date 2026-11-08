package web

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"fmt"
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

	var err error
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
		err = s.loadConversation(r.Context(), &p)
	case p.Tab == "/tasks" || strings.HasPrefix(p.Tab, "/tasks/"):
		copy := r.Clone(r.Context())
		u := *r.URL
		u.Path = p.Tab
		q := url.Values{}
		for _, key := range []string{"q", "cursor", "history", "feedback_before", "needs_me"} {
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
	}

	if err != nil {
		fail(w, 503)
		return
	}
	regions := map[string]string{}
	for id, name := range fragments {
		var html bytes.Buffer
		if err = requestLayout(r).ExecuteTemplate(&html, name, p); err != nil {
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
	w.Header().Set("Vary", "Cookie, Authorization")
	if r.Header.Get("If-None-Match") == etag {
		w.WriteHeader(http.StatusNotModified)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write(body)
}

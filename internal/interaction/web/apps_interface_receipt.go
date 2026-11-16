package web

import (
	"net/http"
	"net/url"
	"strconv"

	"github.com/z-chenhao/assistant/internal/connector/composio"
)

// This is the minimum trusted acceptance receipt, not a native interface
// fallback. It preserves an accepted link/identity while business presentation
// is unavailable. No candidate runs after the effect and no effect is retried.
func (s *Server) acceptedAppReceipt(w http.ResponseWriter, r *http.Request, link composio.Link) error {
	reply := map[string]string{"presentation_unavailable": "true", "authorization_url": link.URL, "connection_id": link.ConnectionID, "authorization_expires": strconv.FormatInt(link.ExpiresAt.UnixMilli(), 10)}
	if state, err := s.Apps.State(r.Context()); err == nil {
		reply["revision"] = strconv.FormatInt(state.Revision, 10)
	}
	if r.Header.Get("Accept") == "application/json" {
		jsonResult(w, reply)
		return nil
	}
	// Native anchors receive only the same proved browser authorization origin.
	safe := ""
	if u, err := url.Parse(link.URL); err == nil && u.Scheme == "https" && u.User == nil && u.Port() == "" && (u.Hostname() == "app.composio.dev" || u.Hostname() == "connect.composio.dev") {
		safe = link.URL
	}
	s.render(w, r, page{Tab: "/settings", View: "app-accepted", Title: "Apps", Path: "/settings/apps", Settings: &settingsView{Section: "apps"}, Apps: &appsView{ConnectURL: safe}})
	return nil
}

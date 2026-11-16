package composio

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestClientSelectsProjectAuthConfigWithoutExposingCredentials(t *testing.T) {
	peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/auth_configs":
			if r.URL.Query().Get("toolkit_slug") != "gmail" || r.URL.Query().Get("is_composio_managed") != "false" {
				t.Error(r.URL)
			}
			_, _ = w.Write([]byte(`{"items":[{"id":"ac_old","toolkit":{"slug":"gmail"},"status":"ENABLED","is_enabled_for_tool_router":true,"created_at":"2026-01-01","credentials":{"secret":"private"}},{"id":"ac_new","toolkit":{"slug":"gmail"},"status":"ENABLED","is_enabled_for_tool_router":true,"created_at":"2026-02-01"},{"id":"ac_wrong","toolkit":{"slug":"slack"},"status":"ENABLED","is_enabled_for_tool_router":true,"created_at":"2026-03-01"},{"id":"ac_disabled","toolkit":{"slug":"gmail"},"status":"DISABLED","is_enabled_for_tool_router":true,"created_at":"2026-04-01"}]}`))
		case "/auth_configs/ac_override":
			_, _ = w.Write([]byte(`{"id":"ac_override","toolkit":{"slug":"gmail"},"status":"ENABLED","is_enabled_for_tool_router":true}`))
		case "/auth_configs/ac_wrong":
			_, _ = w.Write([]byte(`{"id":"ac_wrong","toolkit":{"slug":"slack"},"status":"ENABLED","is_enabled_for_tool_router":true}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer peer.Close()
	c := Client{Secrets: secrets{}, KeyRef: "env:TEST", Endpoint: peer.URL}
	if selection, err := c.FindAuthConfigLegacyV1(context.Background(), "gmail", ""); err != nil || selection.ID != "ac_new" {
		t.Fatal(selection.ID, err)
	}
	if selection, err := c.FindAuthConfigLegacyV1(context.Background(), "gmail", "ac_override"); err != nil || selection.ID != "ac_override" {
		t.Fatal(selection.ID, err)
	}
	if _, err := c.FindAuthConfigLegacyV1(context.Background(), "gmail", "ac_wrong"); err == nil {
		t.Fatal("wrong toolkit auth config accepted")
	}
}
func TestClientAuthConfigPaginationAndScopedKeyFallback(t *testing.T) {
	peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("toolkit_slug") == "slack" {
			w.WriteHeader(http.StatusForbidden)
			return
		}
		if r.URL.Query().Get("cursor") == "" {
			_, _ = w.Write([]byte(`{"items":[],"next_cursor":"next"}`))
			return
		}
		_, _ = w.Write([]byte(`{"items":[{"id":"ac_page2","toolkit":{"slug":"gmail"},"status":"ENABLED","is_enabled_for_tool_router":true}]}`))
	}))
	defer peer.Close()
	c := Client{Secrets: secrets{}, KeyRef: "env:TEST", Endpoint: peer.URL}
	if selection, err := c.FindAuthConfigLegacyV1(context.Background(), "gmail", ""); err != nil || selection.ID != "ac_page2" {
		t.Fatal(selection.ID, err)
	}
	if selection, err := c.FindAuthConfigLegacyV1(context.Background(), "slack", ""); err != nil || selection.ID != "" {
		t.Fatal("scoped key lost managed sign-in", selection.ID, err)
	}
}

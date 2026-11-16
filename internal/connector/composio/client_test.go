package composio

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

type secrets struct{}

func (secrets) Put(context.Context, string, []byte) error { return nil }
func (secrets) Delete(context.Context, string) error      { return nil }

func (secrets) Get(context.Context, string) ([]byte, error) {
	return []byte("project-key-private"), nil
}

func TestClientCataloguePaginationAndHostedLinkProtocol(t *testing.T) {
	var count atomic.Int32
	peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		count.Add(1)
		if r.Header.Get("x-api-key") != "project-key-private" {
			t.Error("missing auth")
		}
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/toolkits":
			if r.Method != "GET" || r.URL.Query().Get("search") != "calendar" || r.URL.Query().Get("cursor") != "page-2" || r.URL.Query().Get("category") != "productivity" {
				t.Error(r.URL)
			}
			_, _ = w.Write([]byte(`{"items":[{"slug":"googlecalendar","name":"Google Calendar","meta":{"logo":"https://logos.composio.dev/api/googlecalendar"}}],"next_cursor":"page-3","total_items":1700}`))
		case "/tool_router/session":
			var body map[string]any
			if json.NewDecoder(r.Body).Decode(&body) != nil {
				t.Error("bad body")
			}
			if body["instant"] != false || body["user_id"] != "owner" || body["workbench"].(map[string]any)["enable"] != false || body["manage_connections"].(map[string]any)["enabled"] != false || body["multi_account"].(map[string]any)["require_explicit_selection"] != true || body["auth_configs"].(map[string]any)["gmail"] != "ac_custom" {
				t.Error(body)
			}
			_, _ = w.Write([]byte(`{"session_id":"trs_test"}`))
		case "/tool_router/session/trs_test/link":
			var body map[string]string
			_ = json.NewDecoder(r.Body).Decode(&body)
			if body["alias"] != "work" || body["toolkit"] != "gmail" {
				t.Error(body)
			}
			_, _ = w.Write([]byte(`{"redirect_url":"https://app.composio.dev/link/secret-link","connected_account_id":"ca_work","link_token":"never-returned"}`))
		default:
			t.Error("unexpected request", r.URL)
			http.NotFound(w, r)
		}
	}))
	defer peer.Close()
	c := Client{Endpoint: peer.URL, KeyRef: "env:TEST", Secrets: secrets{}}
	result, err := c.CatalogLegacyV1(context.Background(), "calendar", "productivity", "page-2")
	if err != nil || result.NextCursor != "page-3" || result.Total != 1700 || len(result.Items) != 1 || result.Items[0].Meta.Logo != "https://logos.composio.dev/api/googlecalendar" {
		t.Fatal(result, err)
	}
	link, err := c.ConnectLegacyV1(context.Background(), "owner", "gmail", "work", "ac_custom")
	if err != nil || link.AccountID != "ca_work" || count.Load() != 3 {
		t.Fatal(link, err, count.Load())
	}
}

func TestClientRejectsRedirectsAndInvalidLinksWithoutLeakingCredentials(t *testing.T) {
	var leaked atomic.Int32
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { leaked.Add(1) }))
	defer target.Close()
	peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, target.URL, http.StatusTemporaryRedirect)
	}))
	defer peer.Close()
	c := Client{Endpoint: peer.URL, Secrets: secrets{}, KeyRef: "env:TEST"}
	_, err := c.CatalogLegacyV1(context.Background(), "", "", "")
	if err == nil || strings.Contains(err.Error(), "project-key-private") || leaked.Load() != 0 {
		t.Fatal(err, leaked.Load())
	}
	for _, link := range []string{"http://app.composio.dev/link/x", "https://app.composio.dev.evil.test/link/x", "https://user@app.composio.dev/link/x", "https://app.composio.dev:123/link/x", "javascript:alert(1)"} {
		t.Run(link, func(t *testing.T) {
			p := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/tool_router/session" {
					_, _ = w.Write([]byte(`{"session_id":"trs_test"}`))
					return
				}
				_ = json.NewEncoder(w).Encode(map[string]string{"redirect_url": link, "connected_account_id": "ca_test"})
			}))
			defer p.Close()
			c.Endpoint = p.URL
			if _, err := c.ConnectLegacyV1(context.Background(), "owner", "gmail", "work", ""); err == nil {
				t.Fatal("unsafe authorization link")
			}
		})
	}
}
func TestClientUnknownMutationsAreNeverRetried(t *testing.T) {
	for _, status := range []int{400, 401, 403, 408, 429, 500, 502, 200} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			var count atomic.Int32
			peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				count.Add(1)
				w.WriteHeader(status)
				_, _ = w.Write([]byte(`provider error project-key-private`))
			}))
			defer peer.Close()
			c := Client{Endpoint: peer.URL, Secrets: secrets{}, KeyRef: "env:TEST"}
			_, err := c.Execute(context.Background(), "owner", "ca_test", "GMAIL_SEND_EMAIL", "20261101_00", json.RawMessage(`{}`))
			want := status == 408 || status >= 500 || status == 200
			if err == nil || Uncertain(err) != want || count.Load() != 1 || strings.Contains(err.Error(), "private") {
				t.Fatal(err, count.Load())
			}
		})
	}
	if Uncertain(errors.New("other")) {
		t.Fatal("unrelated error")
	}
}
func TestClientProjectsAccountsAndScrubsProjectKey(t *testing.T) {
	peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/connected_accounts/") {
			_, _ = w.Write([]byte(`{"id":"ca_test","user_id":"owner","status":"ACTIVE","toolkit":{"slug":"gmail"},"data":{"access_token":"credential-private"}}`))
			return
		}
		_, _ = w.Write([]byte(`{"data":{"echo":"project-key-private"},"successful":true,"session_info":{"token":"credential-private"}}`))
	}))
	defer peer.Close()
	c := Client{Endpoint: peer.URL, Secrets: secrets{}, KeyRef: "env:TEST"}
	a, err := c.Account(context.Background(), "ca_test")
	if err != nil {
		t.Fatal(err)
	}
	b, _ := json.Marshal(a)
	if strings.Contains(string(b), "credential-private") {
		t.Fatal(string(b))
	}
	v, err := c.Execute(context.Background(), "owner", "ca_test", "GMAIL_SEND_EMAIL", "20261101_00", json.RawMessage(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	b, _ = json.Marshal(v)
	if strings.Contains(string(b), "private") || !strings.Contains(string(b), "redacted") {
		t.Fatal(string(b))
	}
}

func TestClientPublicExecutionAndCredentialFieldRedaction(t *testing.T) {
	peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		if json.NewDecoder(r.Body).Decode(&body) != nil {
			t.Error("invalid body")
		}
		if _, ok := body["connected_account_id"]; ok {
			t.Error("public operation selected a personal account")
		}
		_, _ = w.Write([]byte(`{"successful":true,"data":{"weather":"sunny","record_id":9007199254740993,"nested":[{"access_token":"private","api_key":"private","authorization":"private"}]}}`))
	}))
	defer peer.Close()
	c := Client{Endpoint: peer.URL, Secrets: secrets{}, KeyRef: "env:TEST"}
	v, err := c.Execute(context.Background(), "owner", "", "WEATHER_LOOKUP", "20261101_00", json.RawMessage(`{}`))
	b, _ := json.Marshal(v)
	if err != nil || strings.Contains(string(b), "private") || !strings.Contains(string(b), "sunny") || !strings.Contains(string(b), "9007199254740993") {
		t.Fatal(string(b), err)
	}
}

func TestClientBoundsResponsesAndCancellation(t *testing.T) {
	peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(strings.Repeat("x", maxResponse+1)))
	}))
	defer peer.Close()
	c := Client{Endpoint: peer.URL, Secrets: secrets{}, KeyRef: "env:TEST"}
	if _, err := c.Execute(context.Background(), "owner", "ca_test", "GMAIL_SEND_EMAIL", "20261101_00", json.RawMessage(`{}`)); !Uncertain(err) {
		t.Fatal("oversized response was not ambiguous", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := c.CatalogLegacyV1(ctx, "", "", ""); err == nil {
		t.Fatal("ignored cancellation")
	}
}

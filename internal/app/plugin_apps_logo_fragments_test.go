package app

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"testing"

	"github.com/z-chenhao/assistant/internal/config"
	"github.com/z-chenhao/assistant/internal/connectedapps"
	"github.com/z-chenhao/assistant/internal/interaction/web"
	"github.com/z-chenhao/assistant/internal/llm/memory"
)

type appsLogoHTTPTransport func(*http.Request) (*http.Response, error)

func (f appsLogoHTTPTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestAppsLogoActualAccountFragmentsKeepOriginalVersionAndAcceptedAction(t *testing.T) {
	ctx := context.Background()
	carrier := filepath.Join(t.TempDir(), "steward")
	buildTestBinary(t, "../../cmd/steward", carrier)
	for _, format := range []string{"json", "native", "json-quarantine"} {
		t.Run(format, func(t *testing.T) {
			root := t.TempDir()
			cfg, err := config.Load("", func(key string) (string, bool) { return root, key == "STEWARD_DATA_ROOT" })
			if err != nil {
				t.Fatal(err)
			}
			secrets := memory.New()
			const token = "synthetic-logo-fragment-owner-atleast24"
			if err := secrets.Put(ctx, "credential:logo-owner", []byte(token)); err != nil {
				t.Fatal(err)
			}
			a, err := OpenRuntimeWithSecrets(ctx, cfg, secrets)
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = a.Close() }()
			a.eventsRequired.Store(false)
			if _, err := a.BindChannel(ctx, "web", "browser", "local", true, 0); err != nil {
				t.Fatal(err)
			}
			host := a.Plugins()
			host.Catalog.Launcher = carrier
			first, policy := prepareAppsLogo(t, ctx, host, "1.0.0")
			activateAppsLogo(t, ctx, host, "", first, policy, "fragments-first")
			second, secondPolicy := prepareAppsLogo(t, ctx, host, "1.1.0")
			state, err := a.store.LoadApps(ctx)
			if err != nil {
				t.Fatal(err)
			}
			state.UserID = "owner"
			state.Connections = []connectedapps.Connection{
				{ID: "keep_one", Toolkit: "gmail", Name: "Gmail", Alias: "work", AccountID: "ca_keep", Status: "active"},
				{ID: "close_one", Toolkit: "gmail", Name: "Gmail", Alias: "public", Public: true, Status: "active"},
				{ID: "keep_dynamic", Toolkit: "new_app", Name: "Original app", Alias: "personal", AccountID: "ca_new", Status: "active"},
			}
			if err := a.store.SaveApps(ctx, state, state.Revision); err != nil {
				t.Fatal(err)
			}
			spy := &accountCloseStore{Store: a.store, afterSave: func(n int) {
				if n == 1 {
					if format == "json-quarantine" {
						if err := host.Store.Quarantine(ctx, first); err != nil {
							t.Fatal(err)
						}
					} else {
						activateAppsLogo(t, ctx, host, first, second, secondPolicy, "fragments-second")
					}
				}
			}}
			a.connectedApps.Store = spy
			a.connectedApps.Changed = nil
			reads := 0
			server := &web.Server{Config: web.Config{TokenRef: "credential:logo-owner"}, Core: a, Secrets: secrets, Apps: a.connectedApps, Logos: func(c context.Context, reference string) (web.AppLogoPolicy, error) {
				reads++
				return a.appsLogosWithHost(c, host, reference)
			}}
			values := url.Values{"action": {"disconnect"}, "connection": {"close_one"}}
			r := httptest.NewRequest("POST", "http://127.0.0.1:8080/apps/configure", strings.NewReader(values.Encode()))
			r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
			r.Header.Set("Authorization", "Bearer "+token)
			if strings.HasPrefix(format, "json") {
				r.Header.Set("Accept", "application/json")
			}
			w := httptest.NewRecorder()
			server.ServeHTTP(w, r)
			if w.Code != 200 {
				t.Fatal("accepted public close hidden", w.Code, w.Body.String())
			}
			body := w.Body.String()
			if strings.HasPrefix(format, "json") {
				var reply map[string]string
				if err := json.Unmarshal(w.Body.Bytes(), &reply); err != nil {
					t.Fatal(err)
				}
				body = reply["sidebar_html"]
				if !strings.Contains(reply["accounts_html"], `data-app-account="keep_one"`) || strings.Contains(reply["accounts_html"], `data-app-account="close_one"`) {
					t.Fatal("accepted original account rows changed")
				}
			}
			if !strings.Contains(body, "/apps/logo/gmail?revision="+url.QueryEscape(first)) || strings.Contains(body, "/apps/logo/gmail?revision="+url.QueryEscape(second)) {
				t.Fatal("response lost original image version", body)
			}
			if reads != 1 {
				t.Fatal("logo graph was not captured exactly once before accepted write", reads)
			}
			after, err := a.store.LoadApps(ctx)
			if err != nil || spy.saves != 2 || after.Connections[1].Status != "disconnected" || !after.Connections[1].RevocationConfirmed {
				t.Fatal("accepted no-grant closure changed", err, spy.saves, after.Connections)
			}
			originalTransport := http.DefaultTransport
			calls := 0
			http.DefaultTransport = appsLogoHTTPTransport(func(request *http.Request) (*http.Response, error) {
				calls++
				if request.URL.String() != "https://logos.composio.dev/api/new_app" || request.Header.Get("Authorization") != "" || request.Header.Get("Cookie") != "" || request.Header.Get("x-api-key") != "" {
					return nil, fmt.Errorf("changed original anonymous source")
				}
				return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(`<svg><path fill="#2468ac" d="M0 0h2v2z"/></svg>`))}, nil
			})
			defer func() { http.DefaultTransport = originalTransport }()
			imageGet := func(reference string) *httptest.ResponseRecorder {
				r := httptest.NewRequest("GET", "http://127.0.0.1:8080/apps/logo/new_app"+reference, nil)
				r.Header.Set("Authorization", "Bearer "+token)
				w := httptest.NewRecorder()
				server.ServeHTTP(w, r)
				return w
			}
			oldImage := imageGet("?revision=" + url.QueryEscape(first))
			if format == "json-quarantine" {
				if oldImage.Code != 404 || calls != 0 {
					t.Fatal("accepted response bypassed quarantined original resource", oldImage.Code, calls)
				}
			} else {
				if oldImage.Code != 200 || !strings.Contains(oldImage.Body.String(), "#2468ac") || calls != 1 {
					t.Fatal("fragment omitted original dynamic source registration", oldImage.Code, calls)
				}
				if w := imageGet(""); w.Code != 404 || calls != 1 {
					t.Fatal("new graph borrowed original source registration", w.Code, calls)
				}
			}

		})
	}
}

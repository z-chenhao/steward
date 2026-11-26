package app

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/z-chenhao/assistant/internal/config"
	"github.com/z-chenhao/assistant/internal/connectedapps"
	"github.com/z-chenhao/assistant/internal/connector/composio"
	"github.com/z-chenhao/assistant/internal/interaction/web"
	"github.com/z-chenhao/assistant/internal/llm/memory"
)

func TestAppsInterfaceActualManagedBrowserLifecycle(t *testing.T) {
	runAppsInterfaceBrowsers(t, []string{"1.0.0", "1.1.0"}, "apps_connections_browser.mjs", "STEWARD_APP_INTERFACE_BROWSER_EVIDENCE")
}
func TestAppsLocalizationActualBrowserOwnedFeedbackAndNative(t *testing.T) {
	runAppsInterfaceBrowsers(t, []string{"1.3.0"}, "apps_localization_browser.mjs", "STEWARD_APP_LOCALIZATION_BROWSER_EVIDENCE")
}

func TestAppsCardViewActualManagedResourceBrowser(t *testing.T) {
	runAppsInterfaceBrowsers(t, []string{"1.4.0"}, "apps_card_view_browser.mjs", "STEWARD_APP_CARD_VIEW_BROWSER_EVIDENCE")
}

func TestAppsSidebarViewActualBrowserAndAcceptedLink(t *testing.T) {
	runAppsInterfaceBrowsers(t, []string{"1.5.0", "3.0.0"}, "apps_sidebar_view_browser.mjs", "STEWARD_APP_SIDEBAR_VIEW_BROWSER_EVIDENCE")
}

func TestAppsMarkupActualBrowserAndRecoveryLink(t *testing.T) {
	runAppsInterfaceBrowsers(t, []string{"1.6.0", "3.2.0"}, "apps_markup_browser.mjs", "STEWARD_APP_MARKUP_BROWSER_EVIDENCE")
}

func TestAppsGraphicsActualBrowserAndRecoveryLink(t *testing.T) {
	runAppsInterfaceBrowsers(t, []string{"1.8.0", "4.0.0"}, "apps_graphics_browser.mjs", "STEWARD_APP_GRAPHICS_BROWSER_EVIDENCE")
}

func TestAppsAccountMarkupActualBrowserAndRecoveryLink(t *testing.T) {
	runAppsInterfaceBrowsers(t, []string{"1.9.0", "1.10.0", "4.3.0", "1.11.0", "1.12.0"}, "apps_account_markup_browser.mjs", "STEWARD_APP_ACCOUNT_MARKUP_BROWSER_EVIDENCE")
}

func TestAppsConnectViewActualBrowserAndRecoveryLink(t *testing.T) {
	runAppsInterfaceBrowsers(t, []string{"1.13.0", "1.14.0", "4.5.0"}, "apps_account_markup_browser.mjs", "STEWARD_APP_CONNECT_VIEW_BROWSER_EVIDENCE")
}

func TestAppsDirectoryControlsActualBrowserNavigationFilterAndPagination(t *testing.T) {
	runAppsInterfaceBrowsers(t, []string{"1.15.0", "1.17.0", "1.18.0", "4.9.0"}, "apps_directory_controls_browser.mjs", "STEWARD_APP_DIRECTORY_CONTROLS_BROWSER_EVIDENCE")
}

func TestAppsPagesActualBrowserOriginalControlsAndViews(t *testing.T) {
	versions := []string{"1.17.0", "1.19.0", "1.20.0", "4.11.0"}
	for _, script := range []string{"apps_directory_controls_browser.mjs", "apps_account_markup_browser.mjs"} {
		t.Run(strings.TrimSuffix(script, ".mjs"), func(t *testing.T) {
			runAppsInterfaceBrowsers(t, versions, script, "STEWARD_APP_PAGES_BROWSER_EVIDENCE")
		})
	}
}

func TestAppsClientActualBrowserOriginalControlsAndStyles(t *testing.T) {
	for _, script := range []string{"apps_directory_controls_browser.mjs", "apps_account_markup_browser.mjs"} {
		t.Run(strings.TrimSuffix(script, ".mjs"), func(t *testing.T) {
			runAppsInterfaceBrowsers(t, []string{"1.21.0", "1.23.0"}, script, "STEWARD_APP_CLIENT_BROWSER_EVIDENCE")
		})
	}
}

// The real browser consumers share the same isolated App/SQLite/Guardian
// fixture and original HTTP peer. Backend account methods remain pre-opt-in.
func runAppsInterfaceBrowsers(t *testing.T, versions []string, script, evidenceVariable string) {
	if os.Getenv("STEWARD_TEST_APP_BROWSER") != "1" {
		t.Skip("required by make test-browser")
	}
	ctx := context.Background()
	carrier := filepath.Join(t.TempDir(), "steward")
	buildTestBinary(t, "../../cmd/steward", carrier)
	for _, version := range versions {
		t.Run(version, func(t *testing.T) {
			root := t.TempDir()
			cfg, err := config.Load("", func(key string) (string, bool) { return root, key == "STEWARD_DATA_ROOT" })
			if err != nil {
				t.Fatal(err)
			}
			secrets := memory.New()
			const token = "synthetic-interface-browser-owner-atleast24"
			if err := secrets.Put(ctx, "credential:interface-browser", []byte(token)); err != nil {
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
			revision, policy := prepareInterface(t, ctx, host, version)
			activateInterface(t, ctx, host, "", revision, policy, "interface-browser")
			_, profileOpted, compositionOpted, _, _, _, e := a.store.AppsInterfaceSelection(ctx)
			if e != nil || !profileOpted || !compositionOpted {
				t.Fatal("version2-only graph did not establish durable opt-in", profileOpted, compositionOpted, e)
			}
			var mu sync.Mutex
			states := map[string]string{}
			creations := 0
			var catalogueQueries []map[string]string
			peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				mu.Lock()
				defer mu.Unlock()
				switch r.URL.Path {
				case "/test/configure-project":
					if err := secrets.Put(r.Context(), connectedapps.DefaultKeyRef, []byte("synthetic-browser-project")); err != nil {
						t.Error(err)
					}
					w.WriteHeader(204)
					return
				case "/test/activate":
					states[r.URL.Query().Get("id")] = "ACTIVE"
					w.WriteHeader(204)
					return
				case "/test/expire":
					states[r.URL.Query().Get("id")] = "EXPIRED"
					w.WriteHeader(204)
					return
				case "/test/quarantine-interface":
					if err := host.Store.Quarantine(r.Context(), revision); err != nil {
						t.Error(err)
					}
					w.WriteHeader(204)
					return
				case "/test/catalog-requests":
					_ = json.NewEncoder(w).Encode(catalogueQueries)
					return
				case "/test/count":
					_ = json.NewEncoder(w).Encode(map[string]int{"created": creations})
					return
				case "/toolkits":
					if script == "apps_directory_controls_browser.mjs" {
						q := r.URL.Query()
						catalogueQueries = append(catalogueQueries, map[string]string{"search": q.Get("search"), "category": q.Get("category"), "cursor": q.Get("cursor")})
						next := `next opaque /<&" 中文`
						if q.Get("cursor") == next {
							next = ""
						}
						item := composio.Toolkit{Slug: "gmail", Name: "Gmail"}
						item.Meta.Description = "Read mail"
						item.Meta.Categories = []composio.Category{{ID: "communication", Name: "Communication"}}
						_ = json.NewEncoder(w).Encode(composio.Catalog{Items: []composio.Toolkit{item}, NextCursor: next})
						return
					}
					_, _ = w.Write([]byte(`{"items":[{"slug":"gmail","name":"Gmail","meta":{"description":"Connect your mail account."}}]}`))
					return
				case "/toolkits/gmail":
					_, _ = w.Write([]byte(`{"slug":"gmail","name":"Gmail"}`))
					return
				case "/auth_configs":
					_, _ = w.Write([]byte(`{"items":[]}`))
					return
				case "/tool_router/session":
					_, _ = w.Write([]byte(`{"session_id":"trs_browser"}`))
					return
				case "/tool_router/session/trs_browser/link":
					creations++
					id := "ca_" + strconv.Itoa(creations)
					states[id] = "INITIALIZING"
					_ = json.NewEncoder(w).Encode(map[string]string{"redirect_url": "https://app.composio.dev/link/" + id, "connected_account_id": id})
					return
				}
				if strings.HasPrefix(r.URL.Path, "/connected_accounts/") {
					id := strings.TrimPrefix(r.URL.Path, "/connected_accounts/")
					if strings.HasSuffix(id, "/revoke") {
						id = strings.TrimSuffix(id, "/revoke")
						states[id] = "REVOKED"
						_ = json.NewEncoder(w).Encode(map[string]any{"connected_account": map[string]string{"id": id, "status": "REVOKED"}})
						return
					}
					if r.Method == "DELETE" {
						delete(states, id)
						w.WriteHeader(204)
						return
					}
					state, err := a.store.LoadApps(r.Context())
					if err != nil {
						t.Error(err)
					}
					_ = json.NewEncoder(w).Encode(map[string]any{"id": id, "user_id": state.UserID, "status": states[id], "toolkit": map[string]string{"slug": "gmail"}, "data": map[string]string{"access_token": "never-expose"}})
					return
				}
				t.Error("unexpected source", r.Method, r.URL)
				http.NotFound(w, r)
			}))
			defer peer.Close()
			a.connectedApps.Client = composio.Client{Endpoint: peer.URL}
			a.connectedApps.Changed = nil
			server := &web.Server{Config: web.Config{TokenRef: "credential:interface-browser"}, Core: a, Secrets: secrets, Apps: a.connectedApps, Interface: func(c context.Context, reference, locale string) (web.AppInterfacePolicy, error) {
				return a.appsInterfaceWithHost(c, host, reference, locale)
			}}
			live := httptest.NewServer(server)
			defer live.Close()
			evidence := os.Getenv(evidenceVariable)
			if evidence == "" {
				evidence = t.TempDir()
			}
			if evidenceVariable == "STEWARD_APP_PAGES_BROWSER_EVIDENCE" || evidenceVariable == "STEWARD_APP_CLIENT_BROWSER_EVIDENCE" {
				evidence = filepath.Join(evidence, strings.TrimSuffix(script, ".mjs"))
			}
			evidence = filepath.Join(evidence, "managed-"+version)
			options, _ := json.Marshal(map[string]any{"token": token, "appsOnly": true, "interfaceRevision": revision, "stopCheck": script == "apps_connections_browser.mjs", "sidebarUnavailable": version == "3.0.0", "markupUnavailable": version == "3.2.0", "graphicsUnavailable": version == "4.0.0", "directoryReversed": version == "1.18.0", "clientView": evidenceVariable == "STEWARD_APP_CLIENT_BROWSER_EVIDENCE", "styleVariant": version == "1.23.0", "pagesView": evidenceVariable == "STEWARD_APP_PAGES_BROWSER_EVIDENCE" || evidenceVariable == "STEWARD_APP_CLIENT_BROWSER_EVIDENCE", "pagesVariant": version == "1.20.0", "directoryUnavailable": version == "4.9.0" || version == "4.11.0", "accountVariant": version == "1.10.0", "accountUnavailable": version == "4.3.0" || version == "4.5.0" || version == "4.11.0", "connectView": version == "1.13.0" || version == "1.14.0", "connectViewVariant": version == "1.14.0", "accountCopy": version == "1.11.0" || version == "1.12.0", "accountCopyVariant": version == "1.12.0"})
			c, cancel := context.WithTimeout(ctx, 180*time.Second)
			defer cancel()
			command := exec.CommandContext(c, "node", "../../tests/system/"+script, live.URL, peer.URL, evidence, string(options))
			if out, err := command.CombinedOutput(); err != nil {
				t.Fatal(string(out), err)
			}
		})
	}
}

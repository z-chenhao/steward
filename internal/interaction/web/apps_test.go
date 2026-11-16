package web

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"slices"
	"strings"
	"testing"

	"github.com/z-chenhao/assistant/internal/agent/sqlite"
	"github.com/z-chenhao/assistant/internal/connectedapps"
	"github.com/z-chenhao/assistant/internal/connector/composio"
	"github.com/z-chenhao/assistant/internal/plugin/appsdirectory/contract"
)

func TestAppsOfflineDirectoryAndOwnerConnectionFlow(t *testing.T) {
	ctx := context.Background()
	store, err := sqlite.Open(ctx, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	s := languageServer()
	service := &connectedapps.Service{Store: store}
	s.Apps = service
	w := httptest.NewRecorder()
	s.ServeHTTP(w, languageRequest("GET", "/settings/apps", "zh-CN", ""))
	if w.Code != 200 || !strings.Contains(w.Body.String(), "Gmail") || strings.Contains(w.Body.String(), "应用连接服务尚未配置") || strings.Contains(w.Body.String(), `value="connect"`) {
		t.Fatal(w.Code, w.Body.String())
	}
	if strings.Contains(w.Body.String(), "composio-key-ref") || strings.Contains(w.Body.String(), "auth_config") {
		t.Fatal("deployment fields leaked into Apps")
	}
	w = httptest.NewRecorder()
	s.ServeHTTP(w, languageRequest("GET", "/settings/apps/service", "en", ""))
	if w.Code != 200 || !strings.Contains(w.Body.String(), "composio-key-ref") || !strings.Contains(w.Body.String(), `href="/settings/apps" aria-current="page"`) {
		t.Fatal("deployment settings not separate", w.Code, w.Body.String())
	}
	for _, request := range []struct{ method, path, body string }{{"GET", "/settings/apps?search=notion", ""}, {"GET", "/settings/apps?category=communication", ""}} {
		w = httptest.NewRecorder()
		s.ServeHTTP(w, languageRequest(request.method, request.path, "en", request.body))
		if w.Code != 200 {
			t.Fatal(w.Code, w.Body.String())
		}
		if request.path == "/settings/apps?search=notion" && (strings.Contains(w.Body.String(), "<strong>Gmail</strong>") || !strings.Contains(w.Body.String(), "Notion")) {
			t.Fatal("suggestion filtering")
		}
	}
	peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/auth_configs":
			_, _ = w.Write([]byte(`{"items":[]}`))
		case "/toolkits":
			_, _ = w.Write([]byte(`{"items":[{"slug":"gmail","name":"Gmail","meta":{"description":"Mail <script>injection</script>"}}],"next_cursor":"page+2=&","total_items":1700}`))
		case "/toolkits/gmail":
			_, _ = w.Write([]byte(`{"slug":"gmail","name":"Gmail"}`))
		case "/tool_router/session":
			_, _ = w.Write([]byte(`{"session_id":"trs_test"}`))
		case "/tool_router/session/trs_test/link":
			_, _ = w.Write([]byte(`{"redirect_url":"https://app.composio.dev/link/private-link","connected_account_id":"ca_test"}`))
		case "/connected_accounts/ca_test":
			if r.Method == "DELETE" {
				w.WriteHeader(204)
				return
			}
			state, _ := store.LoadApps(r.Context())
			_, _ = w.Write([]byte(`{"id":"ca_test","user_id":"` + state.UserID + `","status":"ACTIVE","toolkit":{"slug":"gmail"},"data":{"access_token":"never-expose"}}`))
		case "/connected_accounts/ca_test/revoke":
			_, _ = w.Write([]byte(`{"connected_account":{"id":"ca_test","status":"REVOKED"}}`))
		default:
			t.Error(r.URL)
			http.NotFound(w, r)
		}
	}))
	defer peer.Close()
	service.Client = composio.Client{Endpoint: peer.URL}
	service.Secrets = fakeSecrets{}
	post := func(form url.Values) *httptest.ResponseRecorder {
		t.Helper()
		result := httptest.NewRecorder()
		s.ServeHTTP(result, languageRequest("POST", "/apps/configure", "en", form.Encode()))
		return result
	}
	w = httptest.NewRecorder()
	s.ServeHTTP(w, languageRequest("POST", "/apps/service/configure", "en", url.Values{"revision": {"1"}, "key_ref": {"env:COMPOSIO_API_KEY"}}.Encode()))
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	w = httptest.NewRecorder()
	s.ServeHTTP(w, languageRequest("GET", "/settings/apps?view=all", "en", ""))
	if strings.Contains(w.Body.String(), "<script>injection</script>") || !strings.Contains(w.Body.String(), "page%2b2%3d%26") || strings.Contains(w.Body.String(), "env:COMPOSIO_API_KEY") {
		t.Fatal(w.Body.String())
	}
	w = post(url.Values{"action": {"connect"}, "toolkit": {"gmail"}, "alias": {"work"}})
	if w.Code != 200 || !strings.Contains(w.Body.String(), "https://app.composio.dev/link/private-link") || w.Header().Get("Referrer-Policy") != "no-referrer" || !strings.Contains(w.Body.String(), "pending") {
		t.Fatal(w.Code, w.Body.String())
	}
	state, _ := store.LoadApps(ctx)
	id := state.Connections[0].ID
	w = post(url.Values{"action": {"check"}, "connection": {id}})
	if w.Code != 200 || !strings.Contains(w.Body.String(), "active") || strings.Contains(w.Body.String(), "never-expose") {
		t.Fatal(w.Code, w.Body.String())
	}
	w = post(url.Values{"action": {"disconnect"}, "connection": {id}})
	if w.Code != 200 || strings.Contains(w.Body.String(), `data-app-account="`+id+`"`) {
		t.Fatal(w.Code, w.Body.String())
	}
	state, _ = store.LoadApps(ctx)
	if !state.Connections[0].RevocationConfirmed {
		t.Fatal("revocation not confirmed")
	}
	r := languageRequest("POST", "/apps/configure", "en", "action=connect&toolkit=gmail&alias=personal")
	r.Header.Set("Origin", "https://attacker.example")
	w = httptest.NewRecorder()
	s.ServeHTTP(w, r)
	if w.Code != 403 {
		t.Fatal("cross-site connection allowed", w.Code)
	}
	r = languageRequest("POST", "/apps/configure", "en", "action=connect&toolkit=gmail&alias=personal")
	r.Header.Del("Authorization")
	w = httptest.NewRecorder()
	s.ServeHTTP(w, r)
	if w.Code == 200 {
		t.Fatal("unauthenticated connection allowed")
	}
}

func TestAppWithoutManagedOAuthExplainsRequiredProjectConfiguration(t *testing.T) {
	for _, scenario := range []struct {
		name, managed string
		custom        bool
		want          bool
		denied        bool
	}{
		{"explicitly unavailable", `,"composio_managed_auth_schemes":[]`, false, false, false},
		{"custom project configured", `,"composio_managed_auth_schemes":[]`, true, true, false},
		{"metadata omitted", ``, false, true, false},
		{"scoped key cannot inspect custom configuration", `,"composio_managed_auth_schemes":[]`, false, true, true},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			ctx := context.Background()
			store, err := sqlite.Open(ctx, t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			defer store.Close()
			links := 0
			peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/toolkits/twitter":
					_, _ = w.Write([]byte(`{"slug":"twitter","name":"Twitter"` + scenario.managed + `}`))
				case "/auth_configs":
					if scenario.denied {
						w.WriteHeader(403)
						return
					}
					body := `{"items":[]}`
					if scenario.custom {
						body = `{"items":[{"id":"ac_owned","toolkit":{"slug":"twitter"},"status":"ENABLED","is_enabled_for_tool_router":true}]}`
					}
					_, _ = w.Write([]byte(body))
				case "/tool_router/session":
					_, _ = w.Write([]byte(`{"session_id":"trs_owned"}`))
				case "/tool_router/session/trs_owned/link":
					links++
					_, _ = w.Write([]byte(`{"redirect_url":"https://app.composio.dev/link/owned","connected_account_id":"ca_owned"}`))
				default:
					t.Error("unexpected request", r.URL.Path)
					http.NotFound(w, r)
				}
			}))
			defer peer.Close()
			service := connectedapps.Service{Store: store, Secrets: fakeSecrets{}, Client: composio.Client{Endpoint: peer.URL}}
			_, err = service.Connect(ctx, "twitter", "", "")
			state, _ := store.LoadApps(ctx)
			if scenario.want {
				if err != nil || links != 1 || len(state.Connections) != 1 {
					t.Fatal("configured flow failed", err, links)
				}
			} else {
				if err != connectedapps.ErrAuthConfigRequired || links != 0 || len(state.Connections) != 0 {
					t.Fatal("unconfigured flow created authorization", err, links)
				}
				message := NativeAppInterface().errorLabel(err)
				if !strings.Contains(message, "OAuth") || strings.Contains(message, "service unavailable") {
					t.Fatal("missing actionable error", message)
				}
			}
		})
	}
}

type appSettingsFixture struct {
	AppConnections
	state   connectedapps.State
	calls   int
	configs map[string]string
}

func (f *appSettingsFixture) Settings(context.Context) (connectedapps.SettingsSelection, error) {
	return connectedapps.NativeSettingsSelection(f.state), nil
}
func (f *appSettingsFixture) State(context.Context) (connectedapps.State, error) { return f.state, nil }
func (f *appSettingsFixture) ConfigureProject(_ context.Context, _ string, revision int64, configs map[string]string) error {
	if revision != f.state.Revision {
		return connectedapps.ErrConflict
	}
	f.calls++
	f.configs = configs
	f.state.AuthConfigs = configs
	f.state.Revision++
	return nil
}
func TestAppServiceUsesFieldsAndPreservesInvalidDraft(t *testing.T) {
	s := languageServer()
	f := &appSettingsFixture{state: connectedapps.State{Revision: 3, KeyRef: "env:COMPOSIO_API_KEY"}}
	s.Apps = f
	send := func(values url.Values) *httptest.ResponseRecorder {
		t.Helper()
		w := httptest.NewRecorder()
		s.ServeHTTP(w, languageRequest("POST", "/apps/service/configure", "en", values.Encode()))
		return w
	}
	values := url.Values{"editor": {"fields"}, "revision": {"3"}, "key_ref": {"env:COMPOSIO_API_KEY"}, "app_slug": {"gmail"}, "auth_config": {"ac_mail"}, "action": {"add-override"}}
	draft := send(values)
	if draft.Code != 200 || f.calls != 0 || strings.Count(draft.Body.String(), `name="app_slug"`) != 2 || strings.Contains(draft.Body.String(), "(JSON)") {
		t.Fatal("field row edit changed service", draft.Code, f.calls)
	}
	values.Del("action")
	values["app_slug"] = []string{"gmail", "gmail"}
	values["auth_config"] = []string{"ac_mail", "ac_duplicate"}
	rejected := send(values)
	if rejected.Code != 422 || f.calls != 0 || !strings.Contains(rejected.Body.String(), `value="ac_duplicate"`) {
		t.Fatal("invalid draft was accepted or lost", rejected.Code, f.calls)
	}
	values["app_slug"] = []string{"gmail", ""}
	values["auth_config"] = []string{"ac_mail", ""}
	saved := send(values)
	if saved.Code != 200 || f.calls != 1 || len(f.configs) != 1 || f.configs["gmail"] != "ac_mail" {
		t.Fatal("field values did not reach the existing service", saved.Code, f.calls, f.configs)
	}
	stale := send(values)
	if stale.Code != 422 || f.calls != 1 {
		t.Fatal("stale revision changed service", stale.Code, f.calls)
	}
}

func TestAppsDiscoveryPreviewsAndCollectionsKeepEveryAppReachable(t *testing.T) {
	s := languageServer()
	state := connectedapps.State{KeyRef: "env:COMPOSIO_API_KEY", Connections: []connectedapps.Connection{{ID: "saved-gmail", Toolkit: "gmail", Name: "Gmail", Status: "active"}}}
	editorial, err := (&connectedapps.Service{}).Directory(context.Background(), contract.Query{})
	if err != nil {
		t.Fatal(err)
	}
	home, err := s.loadAppDiscovery(state, "", editorial)
	if err == nil {
		err = s.projectAppInventory(context.Background(), editorial, home)
	}
	if err != nil {
		t.Fatal(err)
	}
	var featured []string
	for _, item := range home.Groups[0].Items {
		featured = append(featured, item.Slug)
	}
	if !slices.Equal(featured, []string{"gmail", "googledrive", "googlecalendar", "github", "slack", "notion"}) {
		t.Fatal("featured order", featured)
	}
	if !home.Groups[0].Items[0].Managed || !home.Groups[0].Items[0].Connected || home.Suggested {
		t.Fatal("saved connection status was lost")
	}
	seen := map[string]bool{}
	reachable := map[string]bool{}
	for _, group := range home.Groups {
		if len(group.Items) > 6 {
			t.Fatal("unbounded preview", group.Title)
		}
		for _, item := range group.Items {
			if seen[item.Slug] {
				t.Fatal("duplicate home app", item.Slug)
			}
			seen[item.Slug] = true
		}
		if group.URL == "" {
			continue
		}
		link, err := url.Parse(group.URL)
		if err != nil {
			t.Fatal(err)
		}
		collection, err := s.loadAppDiscovery(state, link.Query().Get("collection"), editorial)
		if err != nil {
			t.Fatal(err)
		}
		if len(collection.Groups) != 1 || collection.Groups[0].Title != group.Title {
			t.Fatal("collection changed groups", group.Title)
		}
		var names []string
		for _, item := range collection.Items {
			reachable[item.Slug] = true
			names = append(names, strings.ToLower(item.Name))
		}
		if !slices.IsSorted(names) {
			t.Fatal("collection order", names)
		}
	}
	for _, item := range editorial.Items {
		if !reachable[item.Slug] {
			t.Fatal("app lost behind preview", item.Slug)
		}
	}
	if _, err := s.loadAppDiscovery(state, "unknown", editorial); err != connectedapps.ErrInvalid {
		t.Fatal("unknown collection", err)
	}
}

func (f *appSettingsFixture) AccountInventory(context.Context) (connectedapps.AccountInventoryPolicy, error) {
	return connectedapps.NativeAccountInventory(), nil
}

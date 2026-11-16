package app

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/z-chenhao/assistant/internal/config"
	"github.com/z-chenhao/assistant/internal/connectedapps"
	"github.com/z-chenhao/assistant/internal/connector/composio"
	"github.com/z-chenhao/assistant/internal/interaction/web"
	"github.com/z-chenhao/assistant/internal/llm/memory"
	"github.com/z-chenhao/assistant/internal/rsi"
)

func TestAppsSettingsViewActualVersionsDraftSaveAndColdReferences(t *testing.T) {
	testAppsSettingsViewLifecycle(t, false)
}
func TestAppsSettingsViewActualBrowserDraftSave(t *testing.T) {
	if os.Getenv("STEWARD_TEST_APP_BROWSER") != "1" {
		t.Skip("required by make test-browser")
	}
	testAppsSettingsViewLifecycle(t, true)
}
func testAppsSettingsViewLifecycle(t *testing.T, browser bool) {
	t.Helper()
	ctx := context.Background()
	root := t.TempDir()
	cfg, err := config.Load("", func(k string) (string, bool) { return root, k == "STEWARD_DATA_ROOT" })
	if err != nil {
		t.Fatal(err)
	}
	secrets := memory.New()
	const token = "synthetic-settings-view-owner-atleast24"
	for k, v := range map[string]string{"credential:settings-view-owner": token, "env:COMPOSIO_API_KEY": "synthetic-settings-project"} {
		if err := secrets.Put(ctx, k, []byte(v)); err != nil {
			t.Fatal(err)
		}
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
	carrier := filepath.Join(t.TempDir(), "steward")
	if out, err := exec.Command("go", "build", "-o", carrier, "../../cmd/steward").CombinedOutput(); err != nil {
		t.Fatal(string(out), err)
	}
	peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.URL.Path == "/toolkits":
			_, _ = w.Write([]byte(`{"items":[]}`))
		case strings.HasPrefix(r.URL.Path, "/auth_configs/"):
			id := strings.TrimPrefix(r.URL.Path, "/auth_configs/")
			_ = json.NewEncoder(w).Encode(map[string]any{"id": id, "toolkit": map[string]string{"slug": "gmail"}, "status": "ENABLED", "is_enabled_for_tool_router": true})
		default:
			t.Error("unexpected source", r.Method, r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	defer peer.Close()
	var host rsi.Host
	var server *web.Server
	gateway := &settingsHTTPGateway{}
	bind := func() {
		host = a.Plugins()
		host.Catalog.Launcher = carrier
		a.connectedApps.Changed = nil
		a.connectedApps.Client = composio.Client{Endpoint: peer.URL}
		a.connectedApps.ReadSettings = func(c context.Context) (settings connectedapps.SettingsPolicy, err error) {
			return a.appsSettingsWithHost(c, host)
		}
		gateway.AppConnections = a.connectedApps
		server = &web.Server{Config: web.Config{TokenRef: "credential:settings-view-owner"}, Core: a, Secrets: secrets, Apps: gateway, Interface: func(c context.Context, ref, locale string) (web.AppInterfacePolicy, error) {
			return a.appsInterfaceWithHost(c, host, ref, locale)
		}}
	}
	bind()
	state, err := a.store.LoadApps(ctx)
	if err != nil {
		t.Fatal(err)
	}
	state.KeyRef = "env:COMPOSIO_API_KEY"
	state.AuthConfigs = map[string]string{"gmail": "ac_old"}
	if err := a.store.SaveApps(ctx, state, state.Revision); err != nil {
		t.Fatal(err)
	}
	before, err := a.store.LoadApps(ctx)
	if err != nil {
		t.Fatal(err)
	}
	send := func(method, path string, form url.Values) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, "http://127.0.0.1:8080"+path, strings.NewReader(form.Encode()))
		r.Header.Set("Authorization", "Bearer "+token)
		r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		w := httptest.NewRecorder()
		server.ServeHTTP(w, r)
		return w
	}
	read := func(ref string, variant bool) {
		t.Helper()
		path := "/settings/apps/service"
		if ref != "" {
			path += "?interface_revision=" + url.QueryEscape(ref)
		}
		w := send("GET", path, nil)
		label := "Verify and save"
		if variant {
			label = "Save connection service"
		}
		if w.Code != 200 || !strings.Contains(w.Body.String(), ">"+label+"</button>") || !strings.Contains(w.Body.String(), `name="key_ref" value="env:COMPOSIO_API_KEY"`) {
			t.Fatal("actual settings source/version unavailable", ref, w.Code, w.Body.String())
		}
	}
	browserCheck := func(version, revision, label string, save bool) {
		t.Helper()
		if !browser {
			return
		}
		served := httptest.NewServer(server)
		defer served.Close()
		evidence := os.Getenv("STEWARD_APP_SETTINGS_VIEW_BROWSER_EVIDENCE")
		if evidence == "" {
			evidence = t.TempDir()
		}
		input, _ := json.Marshal(map[string]any{"URL": served.URL, "Token": token, "Evidence": filepath.Join(evidence, version), "Order": []string{"gmail"}, "SaveLabel": label, "Save": save, "RemoveDraft": true, "InterfaceRevision": revision})
		c, cancel := context.WithTimeout(ctx, 90*time.Second)
		defer cancel()
		command := exec.CommandContext(c, "node", "../../tests/system/apps_settings_plugins_browser.mjs")
		command.Stdin = strings.NewReader(string(input))
		if out, err := command.CombinedOutput(); err != nil {
			t.Fatal("actual settings source browser", string(out), err)
		}
	}
	old, oldPolicy := prepareInterface(t, ctx, host, "1.13.0")
	activateInterface(t, ctx, host, "", old, oldPolicy, "settings-view-old4")
	read("", false)
	first, firstPolicy := prepareInterface(t, ctx, host, "1.15.0")
	activateInterface(t, ctx, host, old, first, firstPolicy, "settings-view-first5")
	browserCheck("1.15.0", first, "Verify and save", false)
	read("", false)
	read(old, false)
	second, secondPolicy := prepareInterface(t, ctx, host, "1.16.0")
	activateInterface(t, ctx, host, first, second, secondPolicy, "settings-view-upgrade")
	read("", true)
	read(first, false)
	activateInterface(t, ctx, host, second, first, firstPolicy, "settings-view-rollback")
	read("", false)
	read(second, true)
	activateInterface(t, ctx, host, first, second, secondPolicy, "settings-view-cold-upgrade")
	if err := a.Close(); err != nil {
		t.Fatal(err)
	}
	a, err = OpenRuntimeWithSecrets(ctx, cfg, secrets)
	if err != nil {
		t.Fatal(err)
	}
	a.eventsRequired.Store(false)
	bind()
	// First dependent reads after actual recovery: no reselect or fixture repair.
	read("", true)
	read(first, false)
	read(old, false)
	afterRead, err := a.store.LoadApps(ctx)
	if err != nil || !reflect.DeepEqual(before, afterRead) {
		t.Fatal("presentation iteration changed settings", err)
	}
	form := url.Values{"interface_revision": {first}, "revision": {fmt.Sprint(before.Revision)}, "editor": {"fields"}, "key_ref": {"env:COMPOSIO_API_KEY"}, "app_slug": {"gmail"}, "auth_config": {"ac_gmail"}, "action": {"add-override"}}
	draft := send("POST", "/apps/service/configure", form)
	if draft.Code != 200 || !strings.Contains(draft.Body.String(), "Unsaved changes.") || strings.Count(draft.Body.String(), `name="app_slug"`) != 2 || !strings.Contains(draft.Body.String(), ">Verify and save</button>") {
		t.Fatal("old view draft changed source or row behavior", draft.Code, draft.Body.String())
	}
	afterDraft, err := a.store.LoadApps(ctx)
	if err != nil || !reflect.DeepEqual(before, afterDraft) || gateway.calls != 0 {
		t.Fatal("draft saved configuration", err, gateway.calls)
	}
	form.Set("action", "remove-override-1")
	form["app_slug"] = []string{"gmail", ""}
	form["auth_config"] = []string{"ac_gmail", ""}
	removed := send("POST", "/apps/service/configure", form)
	if removed.Code != 200 || strings.Count(removed.Body.String(), `name="app_slug"`) != 1 || gateway.calls != 0 {
		t.Fatal("remove draft changed explicit save", removed.Code, gateway.calls)
	}
	form.Del("action")
	form["app_slug"] = []string{"gmail"}
	form["auth_config"] = []string{"ac_gmail"}
	saved := send("POST", "/apps/service/configure", form)
	if saved.Code != 200 || gateway.calls != 1 || !strings.Contains(saved.Body.String(), "App connection service saved.") || !strings.Contains(saved.Body.String(), `name="interface_revision" value="`+first+`"`) || !strings.Contains(saved.Body.String(), ">Verify and save</button>") {
		t.Fatal("accepted old save mixed presentation or replayed effect", saved.Code, gateway.calls, saved.Body.String())
	}
	accepted, err := a.store.LoadApps(ctx)
	if err != nil || accepted.Revision != before.Revision+1 || accepted.AuthConfigs["gmail"] != "ac_gmail" {
		t.Fatal("original save facts lost", accepted, err)
	}
	expectedSaves := 1
	if browser {
		browserCheck("1.16.0", second, "Save connection service", true)
		afterBrowser, e := a.store.LoadApps(ctx)
		if e != nil || gateway.calls != 2 || afterBrowser.Revision != accepted.Revision+1 || !reflect.DeepEqual(afterBrowser.AuthConfigs, accepted.AuthConfigs) {
			t.Fatal("browser draft/save effect mismatch", e, gateway.calls)
		}
		accepted = afterBrowser
		expectedSaves = 2
	}
	bad, badPolicy := prepareInterface(t, ctx, host, "4.7.0")
	activateInterface(t, ctx, host, second, bad, badPolicy, "settings-view-invalid-controls")
	if w := send("GET", "/settings/apps/service", nil); w.Code != 503 {
		t.Fatal("malformed managed settings restored native", w.Code)
	}
	read(first, false)
	if _, err := host.Store.Deactivate(ctx, "settings-view-stop", bad); err != nil {
		t.Fatal(err)
	}
	if err := a.Close(); err != nil {
		t.Fatal(err)
	}
	a, err = OpenRuntimeWithSecrets(ctx, cfg, secrets)
	if err != nil {
		t.Fatal(err)
	}
	a.eventsRequired.Store(false)
	bind()
	read(old, false)
	read(first, false)
	read(second, true)
	if w := send("GET", "/settings/apps/service", nil); w.Code != 503 {
		t.Fatal("cold deactivation restored native", w.Code)
	}
	if _, _, _, _, _, boundary, err := a.store.AppsInterfaceSelection(ctx); err != nil || boundary != 5 {
		t.Fatal("settings boundary lost", boundary, err)
	}
	recovered, err := a.store.LoadApps(ctx)
	if err != nil || !reflect.DeepEqual(accepted, recovered) || gateway.calls != expectedSaves {
		t.Fatal("cold view replayed original save", err, gateway.calls)
	}
}

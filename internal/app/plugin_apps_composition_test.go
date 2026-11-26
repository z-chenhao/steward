package app

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
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

func TestAppsCompositionActualVersionsColdOriginalProfileOnlyAndUnavailable(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	cfg, err := config.Load("", func(key string) (string, bool) { return root, key == "STEWARD_DATA_ROOT" })
	if err != nil {
		t.Fatal(err)
	}
	secrets := memory.New()
	const token = "synthetic-composition-owner-atleast24"
	if err := secrets.Put(ctx, "credential:composition-owner", []byte(token)); err != nil {
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
	carrier := filepath.Join(t.TempDir(), "steward")
	buildTestBinary(t, "../../cmd/steward", carrier)
	var host rsi.Host
	var server *web.Server
	bind := func() {
		host = a.Plugins()
		host.Catalog.Launcher = carrier
		server = &web.Server{Config: web.Config{TokenRef: "credential:composition-owner"}, Core: a, Secrets: secrets, Apps: a.connectedApps, Interface: func(c context.Context, ref, locale string) (web.AppInterfacePolicy, error) {
			return a.appsInterfaceWithHost(c, host, ref, locale)
		}}
		a.connectedApps.Changed = nil
	}
	bind()
	get := func(ref string) *httptest.ResponseRecorder {
		path := "/settings/apps/service"
		if ref != "" {
			path += "?interface_revision=" + url.QueryEscape(ref)
		}
		r := httptest.NewRequest("GET", "http://127.0.0.1:8080"+path, nil)
		r.Header.Set("Authorization", "Bearer "+token)
		w := httptest.NewRecorder()
		server.ServeHTTP(w, r)
		return w
	}
	order := func(w *httptest.ResponseRecorder, backFirst bool) {
		t.Helper()
		body := w.Body.String()
		back := strings.Index(body, `class="back-link"`)
		save := strings.Index(body, "Verify and save")
		if w.Code != 200 || back < 0 || save < 0 || (back < save) != backFirst {
			t.Fatal("actual section order changed", w.Code, back, save, body)
		}
	}
	browserCheck := func(version, revision string, backFirst bool) {
		t.Helper()
		if os.Getenv("STEWARD_TEST_APP_BROWSER") != "1" {
			return
		}
		public := httptest.NewServer(server)
		defer public.Close()
		evidence := os.Getenv("STEWARD_APP_COMPOSITION_BROWSER_EVIDENCE")
		if evidence == "" {
			evidence = t.TempDir()
		}
		input, _ := json.Marshal(struct {
			URL, Token, Evidence, Revision, Title string
			BackFirst                             bool
		}{public.URL, token, filepath.Join(evidence, "managed-"+version), revision, map[bool]string{true: "App connection service", false: "Connection service"}[backFirst], backFirst})
		c, cancel := context.WithTimeout(ctx, 90*time.Second)
		defer cancel()
		cmd := exec.CommandContext(c, "node", "../../tests/system/apps_composition_browser.mjs")
		cmd.Stdin = strings.NewReader(string(input))
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatal(string(out), err)
		}
	}
	order(get(""), true)
	old, oldPolicy := prepareInterface(t, ctx, host, "0.9.0")
	activateInterface(t, ctx, host, "", old, oldPolicy, "composition-profile-only")
	order(get(""), true)
	_, profileOpted, compositionOpted, _, _, _, err := a.store.AppsInterfaceSelection(ctx)
	if err != nil || !profileOpted || compositionOpted {
		t.Fatal("separate original profile-only boundary lost", profileOpted, compositionOpted, err)
	}
	captured, err := a.appsInterfaceWithHost(ctx, host, "", "en")
	if err != nil {
		t.Fatal(err)
	}
	first, firstPolicy := prepareInterface(t, ctx, host, "1.0.0")
	activateInterface(t, ctx, host, old, first, firstPolicy, "composition-first")
	order(get(""), true)
	browserCheck("1.0.0", first, true)
	reader := server.Interface
	server.Interface = func(context.Context, string, string) (web.AppInterfacePolicy, error) { return captured, nil }
	order(get(""), true) // A genuinely pre-opt-in in-flight interpretation is retained.
	server.Interface = reader
	second, secondPolicy := prepareInterface(t, ctx, host, "1.2.0")
	activateInterface(t, ctx, host, first, second, secondPolicy, "composition-second")
	order(get(""), false)
	browserCheck("1.2.0", second, false)
	order(get(first), true)
	order(get(old), true) // Explicit original profile-only published reference.
	// Preserve the real variant section order across an actual service save,
	// even when the accepted write activates the original composition.
	if err := secrets.Put(ctx, "env:COMPOSIO_API_KEY", []byte("synthetic-composition-key")); err != nil {
		t.Fatal(err)
	}
	calls := 0
	peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.Method != "GET" || r.URL.Path != "/toolkits" {
			t.Errorf("unexpected original source %s %s", r.Method, r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"items":[],"next_cursor":null}`))
	}))
	defer peer.Close()
	a.connectedApps.Client = composio.Client{Endpoint: peer.URL}
	before, err := a.store.LoadApps(ctx)
	if err != nil {
		t.Fatal(err)
	}
	before.KeyRef = "env:COMPOSIO_API_KEY"
	if err := a.store.SaveApps(ctx, before, before.Revision); err != nil {
		t.Fatal(err)
	}
	before, err = a.store.LoadApps(ctx)
	if err != nil {
		t.Fatal(err)
	}
	spy := &accountCloseStore{Store: a.store, afterSave: func(n int) {
		if n == 1 {
			activateInterface(t, ctx, host, second, first, firstPolicy, "composition-save-switch")
		}
	}}
	a.connectedApps.Store = spy
	reads := 0
	reader = server.Interface
	server.Interface = func(c context.Context, reference, locale string) (web.AppInterfacePolicy, error) {
		reads++
		return reader(c, reference, locale)
	}
	values := url.Values{"revision": {strconv.FormatInt(before.Revision, 10)}, "key_ref": {before.KeyRef}, "auth_configs": {"{}"}, "interface_revision": {second}}
	request := httptest.NewRequest("POST", "http://127.0.0.1:8080/apps/service/configure", strings.NewReader(values.Encode()))
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	request.Header.Set("Authorization", "Bearer "+token)
	saved := httptest.NewRecorder()
	server.ServeHTTP(saved, request)
	order(saved, false)
	if reads != 1 || calls != 1 || spy.saves != 1 || !strings.Contains(saved.Body.String(), "App connection service saved.") || !strings.Contains(saved.Body.String(), "<h1>Connection service</h1>") || !strings.Contains(saved.Body.String(), `name="interface_revision" value="`+second+`"`) {
		t.Fatal("accepted save reselected composition", reads, calls, spy.saves, saved.Body.String())
	}
	server.Interface = reader
	a.connectedApps.Store = a.store
	order(get(""), true)
	activateInterface(t, ctx, host, first, second, secondPolicy, "composition-cold-variant")
	if err := a.Close(); err != nil {
		t.Fatal(err)
	}
	a, err = OpenRuntimeWithSecrets(ctx, cfg, secrets)
	if err != nil {
		t.Fatal(err)
	}
	a.eventsRequired.Store(false)
	bind()
	order(get(""), false)
	activateInterface(t, ctx, host, second, first, firstPolicy, "composition-rollback")
	order(get(""), true)
	bad, badPolicy := prepareInterface(t, ctx, host, "2.1.0")
	activateInterface(t, ctx, host, first, bad, badPolicy, "composition-malicious")
	if w := get(""); w.Code != 503 {
		t.Fatal("invalid owner-read composition restored native", w.Code)
	}
	order(get(first), true)
	state, err := a.store.LoadApps(ctx)
	if err != nil {
		t.Fatal(err)
	}
	state.UserID = "owner"
	state.Connections = []connectedapps.Connection{{ID: "closing", Toolkit: "gmail", Name: "Gmail", Alias: "public", Public: true, Status: "active"}}
	if err := a.store.SaveApps(ctx, state, state.Revision); err != nil {
		t.Fatal(err)
	}
	r := httptest.NewRequest("POST", "http://127.0.0.1:8080/apps/configure", strings.NewReader("action=disconnect&connection=closing"))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	r.Header.Set("Authorization", "Bearer "+token)
	accepted := httptest.NewRecorder()
	server.ServeHTTP(accepted, r)
	if accepted.Code != 200 || !strings.Contains(accepted.Body.String(), "The account action was accepted.") {
		t.Fatal("invalid layout hid independently accepted close", accepted.Code, accepted.Body.String())
	}
	after, err := a.store.LoadApps(ctx)
	if err != nil || after.Connections[0].Status != "disconnected" || !after.Connections[0].RevocationConfirmed {
		t.Fatal("original close not durable", after, err)
	}
	activateInterface(t, ctx, host, bad, old, oldPolicy, "composition-missing")
	if w := get(""); w.Code != 503 {
		t.Fatal("current original profile-only graph silently restored native composition", w.Code)
	}
	order(get(old), true)
	if err := host.Store.Quarantine(ctx, old); err != nil {
		t.Fatal(err)
	}
	if w := get(old); w.Code != 503 {
		t.Fatal("quarantined old reference bypassed layout/profile fence", w.Code)
	}
	if _, err := host.Store.Deactivate(ctx, "composition-disable", old); err != nil {
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
	if w := get(""); w.Code != 503 {
		t.Fatal("cold disabled composition restored native", w.Code)
	}
	_, profileOpted, compositionOpted, _, _, _, err = a.store.AppsInterfaceSelection(ctx)
	if err != nil || !profileOpted || !compositionOpted {
		t.Fatal("durable independent composition opt-in lost", profileOpted, compositionOpted, err)
	}
}

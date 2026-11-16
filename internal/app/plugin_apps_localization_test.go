package app

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/z-chenhao/assistant/internal/config"
	"github.com/z-chenhao/assistant/internal/connectedapps"
	"github.com/z-chenhao/assistant/internal/interaction/web"
	"github.com/z-chenhao/assistant/internal/llm/memory"
	"github.com/z-chenhao/assistant/internal/plugin/appsinterface/contract"
	"github.com/z-chenhao/assistant/internal/rsi"
)

func TestAppsLocalizationActualOwnedTextV1RefsColdFrozenEffectsAndNoFallback(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	cfg, err := config.Load("", func(key string) (string, bool) { return root, key == "STEWARD_DATA_ROOT" })
	if err != nil {
		t.Fatal(err)
	}
	secrets := memory.New()
	const token = "synthetic-locale-owner-atleast24"
	if err := secrets.Put(ctx, "credential:locale-owner", []byte(token)); err != nil {
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
	if out, err := exec.Command("go", "build", "-o", carrier, "../../cmd/steward").CombinedOutput(); err != nil {
		t.Fatal(string(out), err)
	}
	var host rsi.Host
	var server *web.Server
	var locales []string
	bind := func() {
		host = a.Plugins()
		host.Catalog.Launcher = carrier
		a.connectedApps.Changed = nil
		server = &web.Server{Config: web.Config{TokenRef: "credential:locale-owner"}, Core: a, Secrets: secrets, Apps: a.connectedApps, Interface: func(c context.Context, reference, locale string) (web.AppInterfacePolicy, error) {
			locales = append(locales, locale)
			return a.appsInterfaceWithHost(c, host, reference, locale)
		}}
	}
	bind()
	if _, err := a.appsInterfaceWithHost(ctx, host, "", "fr"); !errors.Is(err, rsi.ErrInvalid) {
		t.Fatal("unsupported locale accepted", err)
	}
	request := func(method, path, locale string, values url.Values, jsonReply bool) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, "http://127.0.0.1:8080"+path, strings.NewReader(values.Encode()))
		r.Header.Set("Authorization", "Bearer "+token)
		r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		r.AddCookie(&http.Cookie{Name: "steward-language", Value: locale})
		if jsonReply {
			r.Header.Set("Accept", "application/json")
		}
		w := httptest.NewRecorder()
		server.ServeHTTP(w, r)
		return w
	}
	service := func(locale, ref string) *httptest.ResponseRecorder {
		path := "/settings/apps/service"
		if ref != "" {
			path += "?interface_revision=" + url.QueryEscape(ref)
		}
		return request("GET", path, locale, nil, false)
	}
	title := func(w *httptest.ResponseRecorder, want string) {
		t.Helper()
		if w.Code != 200 || !strings.Contains(w.Body.String(), "<h1>"+want+"</h1>") || !strings.Contains(w.Body.String(), "<title>Steward · "+want+"</title>") {
			t.Fatal("owned title was translated/reselected", w.Code, want, w.Body.String())
		}
	}
	old, oldPolicy := prepareInterface(t, ctx, host, "0.8.0")
	activateInterface(t, ctx, host, "", old, oldPolicy, "locale-original-v1")
	legacy := service("zh-CN", "")
	if legacy.Code != 200 || !strings.Contains(legacy.Body.String(), "<h1>应用连接服务</h1>") {
		t.Fatal("actual original v1 interpretation lost", legacy.Code, legacy.Body.String())
	}
	first, firstPolicy := prepareInterface(t, ctx, host, "1.0.0")
	activateInterface(t, ctx, host, old, first, firstPolicy, "locale-first-v2")
	title(service("en", ""), "App connection service")
	title(service("zh-CN", ""), "应用连接服务")
	variant, variantPolicy := prepareInterface(t, ctx, host, "1.3.0")
	activateInterface(t, ctx, host, first, variant, variantPolicy, "locale-independent-copy")
	title(service("en", ""), "Connection workspace")
	title(service("zh-CN", ""), "Workspace")
	if w := service("zh-CN", old); w.Code != 200 || !strings.Contains(w.Body.String(), "<h1>应用连接服务</h1>") {
		t.Fatal("retained version1 interpretation changed", w.Code)
	}
	invalid := request("POST", "/apps/configure", "zh-CN", url.Values{"action": {"not-an-action"}}, true)
	var reply map[string]string
	if err := json.Unmarshal(invalid.Body.Bytes(), &reply); err != nil {
		t.Fatal(err)
	}
	if invalid.Code != 422 || reply["error"] != "Workspace" {
		t.Fatal("JSON error was retranslated by Host", invalid.Code, reply)
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
	title(service("zh-CN", ""), "Workspace")
	state, err := a.store.LoadApps(ctx)
	if err != nil {
		t.Fatal(err)
	}
	state.UserID = "owner"
	state.Connections = []connectedapps.Connection{{ID: "closing", Toolkit: "gmail", Name: "Gmail", Alias: "public", Public: true, Status: "active"}}
	if err := a.store.SaveApps(ctx, state, state.Revision); err != nil {
		t.Fatal(err)
	}
	spy := &accountCloseStore{Store: a.store, afterSave: func(n int) {
		if n == 1 {
			activateInterface(t, ctx, host, variant, first, firstPolicy, "locale-during-accepted-close")
		}
	}}
	a.connectedApps.Store = spy
	locales = nil
	accepted := request("POST", "/apps/configure", "zh-CN", url.Values{"action": {"disconnect"}, "connection": {"closing"}, "interface_revision": {variant}}, false)
	if accepted.Code != 200 || spy.saves != 2 || len(locales) != 1 || locales[0] != "zh-CN" || !strings.Contains(accepted.Body.String(), `role="status">Workspace</div>`) {
		t.Fatal("accepted native notice lost original locale/text", accepted.Code, spy.saves, locales, accepted.Body.String())
	}
	a.connectedApps.Store = a.store
	title(service("zh-CN", ""), "应用连接服务")
	baseline := first
	for _, version := range []string{"2.2.0", "2.3.0"} {
		bad, policy := prepareInterface(t, ctx, host, version)
		activateInterface(t, ctx, host, baseline, bad, policy, "locale-no-fallback-"+version)
		// Both healthy lower versions actually exist. Failure must not retry them.
		for _, legacyContract := range []string{contract.Interface, contract.CompositionInterface} {
			if _, err := host.ReadService(ctx, bad, legacyContract, json.RawMessage(`{}`), nil); err != nil {
				t.Fatal("lower declared service is not healthy", version, legacyContract, err)
			}
		}
		if w := service("zh-CN", ""); w.Code != 503 {
			t.Fatal("invalid declared v2 retried healthy v1", version, w.Code)
		}
		baseline = bad
	}
	activateInterface(t, ctx, host, baseline, old, oldPolicy, "locale-explicit-version1-rollback")
	if w := service("zh-CN", ""); w.Code != 200 || !strings.Contains(w.Body.String(), "<h1>应用连接服务</h1>") {
		t.Fatal("declared version1 rollback changed", w.Code)
	}
	title(service("zh-CN", variant), "Workspace")
	if err := host.Store.Quarantine(ctx, variant); err != nil {
		t.Fatal(err)
	}
	if w := service("zh-CN", variant); w.Code != 503 {
		t.Fatal("revoked original version bypassed locale adapter", w.Code)
	}
	if _, err := host.Store.Deactivate(ctx, "locale-disable", old); err != nil {
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
	if w := service("zh-CN", ""); w.Code != 503 {
		t.Fatal("cold stop restored native locale business", w.Code)
	}
}

func TestAppsLocalizationCompositionOnlyNeverRestoresNative(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	cfg, err := config.Load("", func(key string) (string, bool) { return root, key == "STEWARD_DATA_ROOT" })
	if err != nil {
		t.Fatal(err)
	}
	secrets := memory.New()
	a, err := OpenRuntimeWithSecrets(ctx, cfg, secrets)
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	a.eventsRequired.Store(false)
	if _, err := a.BindChannel(ctx, "web", "browser", "local", true, 0); err != nil {
		t.Fatal(err)
	}
	carrier := filepath.Join(t.TempDir(), "steward")
	if out, err := exec.Command("go", "build", "-o", carrier, "../../cmd/steward").CombinedOutput(); err != nil {
		t.Fatal(string(out), err)
	}
	host := a.Plugins()
	host.Catalog.Launcher = carrier
	revision, policy := prepareInterface(t, ctx, host, "2.4.0")
	activateInterface(t, ctx, host, "", revision, policy, "locale-composition-only")
	_, profile, composition, _, _, _, err := a.store.AppsInterfaceSelection(ctx)
	if err != nil || profile || !composition {
		t.Fatal("invalid fixture boundary", profile, composition, err)
	}
	if _, err := a.appsInterfaceWithHost(ctx, host, "", "en"); err == nil {
		t.Fatal("composition opt-in silently restored native profile and layout")
	}
	const token = "synthetic-composition-only-atleast24"
	if err := secrets.Put(ctx, "credential:composition-only", []byte(token)); err != nil {
		t.Fatal(err)
	}
	server := &web.Server{Config: web.Config{TokenRef: "credential:composition-only"}, Core: a, Secrets: secrets, Apps: a.connectedApps, Interface: func(c context.Context, ref, locale string) (web.AppInterfacePolicy, error) {
		return a.appsInterfaceWithHost(c, host, ref, locale)
	}}
	request := httptest.NewRequest("GET", "http://127.0.0.1:8080/settings/apps/service", nil)
	request.Header.Set("Authorization", "Bearer "+token)
	response := httptest.NewRecorder()
	server.ServeHTTP(response, request)
	if response.Code != 503 {
		t.Fatal("actual composition-only page restored native", response.Code, response.Body.String())
	}

}

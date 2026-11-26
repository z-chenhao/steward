package app

import (
	"context"
	"github.com/z-chenhao/assistant/internal/config"
	"github.com/z-chenhao/assistant/internal/connectedapps"
	"github.com/z-chenhao/assistant/internal/interaction/web"
	"github.com/z-chenhao/assistant/internal/llm/memory"
	"github.com/z-chenhao/assistant/internal/rsi"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"testing"
)

// Actual App/SQLite/Guardian/HTTP, independent from the native atom oracle.
func TestAppsCardViewActualResourceVersionsRefsColdAndUnavailable(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	cfg, err := config.Load("", func(key string) (string, bool) { return root, key == "STEWARD_DATA_ROOT" })
	if err != nil {
		t.Fatal(err)
	}
	secrets := memory.New()
	const token = "synthetic-card-owner-atleast24"
	if err := secrets.Put(ctx, "credential:card-owner", []byte(token)); err != nil {
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
	reads := 0
	bind := func() {
		host = a.Plugins()
		host.Catalog.Launcher = carrier
		a.connectedApps.Changed = nil
		server = &web.Server{Config: web.Config{TokenRef: "credential:card-owner"}, Core: a, Secrets: secrets, Apps: a.connectedApps, Interface: func(c context.Context, ref, locale string) (web.AppInterfacePolicy, error) {
			reads++
			return a.appsInterfaceWithHost(c, host, ref, locale)
		}}
	}
	bind()
	get := func(path, ref string) *httptest.ResponseRecorder {
		if ref != "" {
			path += "?interface_revision=" + url.QueryEscape(ref)
		}
		r := httptest.NewRequest("GET", "http://127.0.0.1:8080"+path, nil)
		r.Header.Set("Authorization", "Bearer "+token)
		w := httptest.NewRecorder()
		server.ServeHTTP(w, r)
		return w
	}
	order := func(ref string, copyFirst bool) {
		t.Helper()
		before := reads
		w := get("/settings/apps", ref)
		body := w.Body.String()
		start := strings.Index(body, `<article class="app-tile"`)
		if w.Code != 200 || start < 0 {
			t.Fatal("card unavailable", w.Code, body)
		}
		body = body[start:]
		body = body[:strings.Index(body, "</article>")]
		copy := strings.Index(body, `class="app-copy"`)
		icon := strings.Index(body, `class="app-logo"`)
		if copy < 0 || icon < 0 || (copy < icon) != copyFirst || reads != before+1 {
			t.Fatal("actual resource was not consumed once", copy, icon, reads, before, body)
		}
		if !strings.Contains(body, `data-app-add`) || (strings.Contains(body, `<form`) && (!strings.Contains(body, `name="action" value="connect"`) || !strings.Contains(body, `name="toolkit" value="gmail"`))) {
			t.Fatal("original native form binding changed", body)
		}
	}
	order("", false)
	old, oldPolicy := prepareInterface(t, ctx, host, "0.7.0")
	activateInterface(t, ctx, host, "", old, oldPolicy, "card-original-v2")
	order("", false)
	_, profile, composition, card, _, _, err := a.store.AppsInterfaceSelection(ctx)
	if err != nil || !profile || !composition || card {
		t.Fatal("actual pre-card boundary", profile, composition, card, err)
	}
	captured, err := a.appsInterfaceWithHost(ctx, host, "", "en")
	if err != nil {
		t.Fatal(err)
	}
	first, firstPolicy := prepareInterface(t, ctx, host, "1.0.0")
	activateInterface(t, ctx, host, old, first, firstPolicy, "card-first")
	order("", false)
	second, secondPolicy := prepareInterface(t, ctx, host, "1.4.0")
	activateInterface(t, ctx, host, first, second, secondPolicy, "card-actual-resource-variant")
	order("", true)
	order(first, false)
	order(old, false)
	reader := server.Interface
	server.Interface = func(context.Context, string, string) (web.AppInterfacePolicy, error) { reads++; return captured, nil }
	order("", false)
	server.Interface = reader
	if err := a.Close(); err != nil {
		t.Fatal(err)
	}
	a, err = OpenRuntimeWithSecrets(ctx, cfg, secrets)
	if err != nil {
		t.Fatal(err)
	}
	a.eventsRequired.Store(false)
	bind()
	order("", true)
	order(old, false)
	frozenVariant, err := a.appsInterfaceWithHost(ctx, host, "", "en")
	if err != nil {
		t.Fatal(err)
	}
	activateInterface(t, ctx, host, second, first, firstPolicy, "card-rollback")
	reader = server.Interface
	server.Interface = func(context.Context, string, string) (web.AppInterfacePolicy, error) {
		reads++
		return frozenVariant, nil
	}
	order("", true)
	server.Interface = reader
	order("", false)
	current := first
	for _, version := range []string{"2.5.0", "2.6.0"} {
		bad, policy := prepareInterface(t, ctx, host, version)
		activateInterface(t, ctx, host, current, bad, policy, "card-invalid-"+version)
		current = bad
		if w := get("/settings/apps", ""); w.Code != 503 {
			t.Fatal("invalid scope/digest restored native", version, w.Code)
		}
		if w := get("/settings/apps/service", ""); w.Code != 200 {
			t.Fatal("optional card vetoed unrelated service UI", version, w.Code)
		}
		if w := get("/api/apps/detail?app=gmail", ""); w.Code != 200 {
			t.Fatal("optional card vetoed account detail", version, w.Code, w.Body.String())
		}
		state, err := a.store.LoadApps(ctx)
		if err != nil {
			t.Fatal(err)
		}
		state.UserID = "owner"
		state.Connections = []connectedapps.Connection{{ID: "closing", Toolkit: "gmail", Name: "Gmail", Alias: "public", Public: true, Status: "active"}}
		if err := a.store.SaveApps(ctx, state, state.Revision); err != nil {
			t.Fatal(err)
		}
		before := reads
		r := httptest.NewRequest("POST", "http://127.0.0.1:8080/apps/configure", strings.NewReader("action=disconnect&connection=closing"))
		r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		r.Header.Set("Authorization", "Bearer "+token)
		accepted := httptest.NewRecorder()
		server.ServeHTTP(accepted, r)
		after, err := a.store.LoadApps(ctx)
		if accepted.Code != 200 || reads != before+1 || err != nil || len(after.Connections) != 1 || after.Connections[0].Status != "disconnected" || !after.Connections[0].RevocationConfirmed {
			t.Fatal("optional bad cards blocked/reselected accepted native close", version, accepted.Code, reads, before, after, err)
		}
		order(first, false)
	}
	activateInterface(t, ctx, host, current, old, oldPolicy, "card-current-missing")
	if w := get("/settings/apps", ""); w.Code != 503 {
		t.Fatal("current old package silently restored cards", w.Code)
	}
	order(old, false)
	if w := get("/settings/apps/service", ""); w.Code != 200 {
		t.Fatal("missing cards vetoed service", w.Code)
	}
	activateInterface(t, ctx, host, old, second, secondPolicy, "card-before-quarantine")
	if err := host.Store.Quarantine(ctx, second); err != nil {
		t.Fatal(err)
	}
	if w := get("/settings/apps", second); w.Code != 503 {
		t.Fatal("retained reference bypassed quarantine", w.Code)
	}
	order(first, false)
	if _, err := host.Store.Deactivate(ctx, "card-stop", second); err != nil {
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
	if w := get("/settings/apps", ""); w.Code != 503 {
		t.Fatal("cold stop restored native", w.Code)
	}
	_, profile, composition, card, _, _, err = a.store.AppsInterfaceSelection(ctx)
	if err != nil || !profile || !composition || !card {
		t.Fatal("durable card boundary lost", profile, composition, card, err)
	}
	order(first, false)
	order(old, false)
}

func TestAppsCardViewOnlyNeverRestoresWholeNative(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	cfg, err := config.Load("", func(key string) (string, bool) { return root, key == "STEWARD_DATA_ROOT" })
	if err != nil {
		t.Fatal(err)
	}
	a, err := OpenRuntimeWithSecrets(ctx, cfg, memory.New())
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	a.eventsRequired.Store(false)
	if _, err := a.BindChannel(ctx, "web", "browser", "local", true, 0); err != nil {
		t.Fatal(err)
	}
	host := a.Plugins()
	carrier := filepath.Join(t.TempDir(), "steward")
	buildTestBinary(t, "../../cmd/steward", carrier)
	host.Catalog.Launcher = carrier
	revision, policy := prepareInterface(t, ctx, host, "2.8.0")
	activateInterface(t, ctx, host, "", revision, policy, "card-only")
	if _, err := a.appsInterfaceWithHost(ctx, host, "", "en"); err == nil {
		t.Fatal("card-only graph restored whole native interface")
	}
	_, p, c, v, _, _, err := a.store.AppsInterfaceSelection(ctx)
	if err != nil || p || c || !v {
		t.Fatal("actual card-only boundary", p, c, v, err)
	}
}

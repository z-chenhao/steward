package app

import (
	"context"
	"encoding/json"
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

func TestAppsSidebarViewActualVersionsRefsColdFrozenEffectsAndReceipts(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	cfg, err := config.Load("", func(k string) (string, bool) { return root, k == "STEWARD_DATA_ROOT" })
	if err != nil {
		t.Fatal(err)
	}
	secrets := memory.New()
	const token = "synthetic-sidebar-owner-atleast24"
	if err := secrets.Put(ctx, "credential:sidebar-owner", []byte(token)); err != nil {
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
		server = &web.Server{Config: web.Config{TokenRef: "credential:sidebar-owner"}, Core: a, Secrets: secrets, Apps: a.connectedApps, Interface: func(c context.Context, ref, locale string) (web.AppInterfacePolicy, error) {
			reads++
			return a.appsInterfaceWithHost(c, host, ref, locale)
		}}
	}
	bind()
	seed := func() {
		t.Helper()
		state, err := a.store.LoadApps(ctx)
		if err != nil {
			t.Fatal(err)
		}
		state.UserID = "owner"
		state.Connections = []connectedapps.Connection{{ID: "keeper", Toolkit: "gmail", Name: "Gmail", Alias: "work", AccountID: "ca_keeper", Status: "active"}, {ID: "closing", Toolkit: "gmail", Name: "Gmail", Alias: "public", Public: true, Status: "active"}}
		if err := a.store.SaveApps(ctx, state, state.Revision); err != nil {
			t.Fatal(err)
		}
	}
	seed()
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
	post := func(ref string, jsonReply bool) *httptest.ResponseRecorder {
		form := url.Values{"action": {"disconnect"}, "connection": {"closing"}, "interface_revision": {ref}}
		r := httptest.NewRequest("POST", "http://127.0.0.1:8080/apps/configure", strings.NewReader(form.Encode()))
		r.Header.Set("Authorization", "Bearer "+token)
		r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		if jsonReply {
			r.Header.Set("Accept", "application/json")
		}
		w := httptest.NewRecorder()
		server.ServeHTTP(w, r)
		return w
	}
	orderHTML := func(body string, nameFirst bool) {
		t.Helper()
		start := strings.Index(body, `class="section-navigation apps-connections"`)
		if start < 0 {
			t.Fatal("original sidebar absent", body)
		}
		body = body[start:]
		body = body[:strings.Index(body, "</nav>")]
		name := strings.Index(body, "<span>Gmail</span>")
		icon := strings.Index(body, `class="app-logo"`)
		if name < 0 || icon < 0 || (name < icon) != nameFirst || !strings.Contains(body, `href="/settings/apps?app=gmail" data-app-open="gmail"`) {
			t.Fatal("actual sidebar resource not consumed/original binding changed", nameFirst, body)
		}
	}
	order := func(ref string, nameFirst bool) {
		t.Helper()
		before := reads
		w := get("/settings/apps", ref)
		if w.Code != 200 || reads != before+1 {
			t.Fatal("sidebar unavailable/reselected", w.Code, reads, before, w.Body.String())
		}
		orderHTML(w.Body.String(), nameFirst)
	}
	order("", false)
	old, oldPolicy := prepareInterface(t, ctx, host, "0.6.0")
	activateInterface(t, ctx, host, "", old, oldPolicy, "sidebar-original-three-services")
	order("", false)
	_, profile, composition, card, sidebar, _, err := a.store.AppsInterfaceSelection(ctx)
	if err != nil || !profile || !composition || !card || sidebar {
		t.Fatal("pre-sidebar fixture boundary", profile, composition, card, sidebar, err)
	}
	captured, err := a.appsInterfaceWithHost(ctx, host, "", "en")
	if err != nil {
		t.Fatal(err)
	}
	first, firstPolicy := prepareInterface(t, ctx, host, "1.0.0")
	activateInterface(t, ctx, host, old, first, firstPolicy, "sidebar-first")
	order("", false)
	second, secondPolicy := prepareInterface(t, ctx, host, "1.5.0")
	activateInterface(t, ctx, host, first, second, secondPolicy, "sidebar-actual-resource-variant")
	order("", true)
	order(first, false)
	order(old, false)
	reader := server.Interface
	server.Interface = func(context.Context, string, string) (web.AppInterfacePolicy, error) { reads++; return captured, nil }
	order("", false)
	server.Interface = reader
	for i, jsonReply := range []bool{true, false} {
		if i > 0 {
			activateInterface(t, ctx, host, first, second, secondPolicy, "sidebar-before-native-effect")
		}
		seed()
		spy := &accountCloseStore{Store: a.store, afterSave: func(n int) {
			if n == 1 {
				activateInterface(t, ctx, host, second, first, firstPolicy, "sidebar-mid-effect-"+map[bool]string{true: "json", false: "native"}[jsonReply])
			}
		}}
		a.connectedApps.Store = spy
		before := reads
		w := post(second, jsonReply)
		after, err := a.store.LoadApps(ctx)
		if w.Code != 200 || reads != before+1 || spy.saves != 2 || err != nil || after.Connections[1].Status != "disconnected" || !after.Connections[1].RevocationConfirmed {
			t.Fatal("accepted original close lost/reselected/repeated", w.Code, reads, before, spy.saves, after, err, w.Body.String())
		}
		body := w.Body.String()
		if jsonReply {
			var reply map[string]string
			if err := json.Unmarshal(w.Body.Bytes(), &reply); err != nil {
				t.Fatal(err)
			}
			if reply["presentation_unavailable"] != "" || !strings.Contains(reply["accounts_html"], second) {
				t.Fatal("accepted account fragment lost original profile", reply)
			}
			body = reply["sidebar_html"]
		}
		orderHTML(body, true)
		a.connectedApps.Store = a.store
		order("", false)
	}
	activateInterface(t, ctx, host, first, second, secondPolicy, "sidebar-cold-baseline")
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
	activateInterface(t, ctx, host, second, first, firstPolicy, "sidebar-rollback")
	order("", false)
	current := first
	for _, version := range []string{"3.0.0", "3.1.0"} {
		bad, policy := prepareInterface(t, ctx, host, version)
		activateInterface(t, ctx, host, current, bad, policy, "sidebar-invalid-"+version)
		current = bad
		seed()
		if w := get("/settings/apps", ""); w.Code != 503 {
			t.Fatal("bad sidebar silently restored native", version, w.Code)
		}
		if w := get("/api/apps/detail?app=gmail", ""); w.Code != 200 {
			t.Fatal("sidebar vetoed nonconsumer account detail", version, w.Code, w.Body.String())
		}
		if w := get("/settings/apps/service", ""); w.Code != 200 {
			t.Fatal("sidebar vetoed nonconsumer service", version, w.Code)
		}
		for _, jsonReply := range []bool{true, false} {
			seed()
			spy := &accountCloseStore{Store: a.store}
			a.connectedApps.Store = spy
			before := reads
			w := post("", jsonReply)
			a.connectedApps.Store = a.store
			after, err := a.store.LoadApps(ctx)
			if w.Code != 200 || reads != before+1 || spy.saves != 2 || err != nil || after.Connections[1].Status != "disconnected" || !after.Connections[1].RevocationConfirmed {
				t.Fatal("optional sidebar hid/replayed accepted close", version, jsonReply, w.Code, reads, before, spy.saves, after, err)
			}
			if jsonReply {
				var reply map[string]string
				if err := json.Unmarshal(w.Body.Bytes(), &reply); err != nil || reply["presentation_unavailable"] != "true" || reply["sidebar_html"] != "" || reply["accounts_html"] != "" {
					t.Fatal("invented partial native reply", reply, err)
				}
			} else if !strings.Contains(w.Body.String(), "The account action was accepted.") || strings.Contains(w.Body.String(), "data-apps ") {
				t.Fatal("native known receipt lost", w.Body.String())
			}
		}
		order(first, false)
		order(old, false)
	}
	activateInterface(t, ctx, host, current, old, oldPolicy, "sidebar-current-missing")
	if w := get("/settings/apps", ""); w.Code != 503 {
		t.Fatal("current pre-sidebar graph silently restored native", w.Code)
	}
	order(old, false)
	activateInterface(t, ctx, host, old, second, secondPolicy, "sidebar-before-quarantine")
	if err := host.Store.Quarantine(ctx, second); err != nil {
		t.Fatal(err)
	}
	if w := get("/settings/apps", second); w.Code != 503 {
		t.Fatal("retained sidebar bypassed quarantine", w.Code)
	}
	order(first, false)
	if _, err := host.Store.Deactivate(ctx, "sidebar-stop", second); err != nil {
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
	order(first, false)
	order(old, false)
	_, profile, composition, card, sidebar, _, err = a.store.AppsInterfaceSelection(ctx)
	if err != nil || !profile || !composition || !card || !sidebar {
		t.Fatal("four durable boundaries lost", profile, composition, card, sidebar, err)
	}
}
func TestAppsSidebarViewOnlyNeverRestoresWholeNative(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	cfg, err := config.Load("", func(k string) (string, bool) { return root, k == "STEWARD_DATA_ROOT" })
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
	buildTestBinary(t, "../../cmd/steward", carrier)
	host := a.Plugins()
	host.Catalog.Launcher = carrier
	revision, policy := prepareInterface(t, ctx, host, "2.9.0")
	activateInterface(t, ctx, host, "", revision, policy, "sidebar-only")
	_, p, c, v, b, _, err := a.store.AppsInterfaceSelection(ctx)
	if err != nil || p || c || v || !b {
		t.Fatal("sidebar-only boundary", p, c, v, b, err)
	}
	if _, err := a.appsInterfaceWithHost(ctx, host, "", "en"); err == nil {
		t.Fatal("sidebar-only restored whole native")
	}
	const token = "synthetic-sidebar-only-atleast24"
	if err := secrets.Put(ctx, "credential:sidebar-only", []byte(token)); err != nil {
		t.Fatal(err)
	}
	server := &web.Server{Config: web.Config{TokenRef: "credential:sidebar-only"}, Core: a, Secrets: secrets, Apps: a.connectedApps, Interface: func(c context.Context, ref, locale string) (web.AppInterfacePolicy, error) {
		return a.appsInterfaceWithHost(c, host, ref, locale)
	}}
	r := httptest.NewRequest("GET", "http://127.0.0.1:8080/settings/apps/service", nil)
	r.Header.Set("Authorization", "Bearer "+token)
	w := httptest.NewRecorder()
	server.ServeHTTP(w, r)
	if w.Code != 503 {
		t.Fatal("sidebar-only HTTP restored native", w.Code)
	}
}

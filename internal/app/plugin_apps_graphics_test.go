package app

import (
	"context"
	"encoding/json"
	"github.com/z-chenhao/assistant/internal/config"
	"github.com/z-chenhao/assistant/internal/connectedapps"
	"github.com/z-chenhao/assistant/internal/interaction/web"
	"github.com/z-chenhao/assistant/internal/llm/memory"
	"github.com/z-chenhao/assistant/internal/plugin/appsinterface/contract"
	"github.com/z-chenhao/assistant/internal/rsi"
	"github.com/z-chenhao/assistant/pkg/rsiplugin"
	"net/http/httptest"
	"net/url"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestAppsGraphicsActualVersionsRefsColdFrozenEffectsAndReceipts(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	cfg, err := config.Load("", func(k string) (string, bool) { return root, k == "STEWARD_DATA_ROOT" })
	if err != nil {
		t.Fatal(err)
	}
	secrets := memory.New()
	const token = "synthetic-graphics-owner-atleast24"
	if err := secrets.Put(ctx, "credential:graphics-owner", []byte(token)); err != nil {
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
	reads := 0
	bind := func() {
		host = a.Plugins()
		host.Catalog.Launcher = carrier
		a.connectedApps.Changed = nil
		server = &web.Server{Config: web.Config{TokenRef: "credential:graphics-owner"}, Core: a, Secrets: secrets, Apps: a.connectedApps, Interface: func(c context.Context, ref, locale string) (web.AppInterfacePolicy, error) {
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
	orderHTML := func(body string, variant bool) {
		t.Helper()
		if !strings.Contains(body, `class="section-navigation apps-connections"`) {
			t.Fatal("original sidebar absent", body)
		}
		if strings.Contains(body, `data-plugin-icon="v1.8"`) != variant {
			t.Fatal("actual markup resource not consumed", variant, body)
		}
		if !strings.Contains(body, `href="/settings/apps?app=gmail" data-app-open="gmail"`) {
			t.Fatal("original target binding changed", body)
		}
	}
	order := func(ref string, variant bool) {
		t.Helper()
		before := reads
		w := get("/settings/apps", ref)
		if w.Code != 200 || reads != before+1 {
			t.Fatal("sidebar unavailable/reselected", w.Code, reads, before, w.Body.String())
		}
		if strings.Contains(w.Body.String(), `data-plugin-more="v1.8"`) != variant {
			t.Fatal("card markup resource not consumed", variant, w.Body.String())
		}
		orderHTML(w.Body.String(), variant)
		r := httptest.NewRequest("GET", "http://127.0.0.1:8080/api/apps/detail?app=gmail&interface_revision="+url.QueryEscape(ref), nil)
		r.Header.Set("Authorization", "Bearer "+token)
		detail := httptest.NewRecorder()
		server.ServeHTTP(detail, r)
		if detail.Code != 200 {
			t.Fatal("graphic detail consumer unavailable", detail.Code, detail.Body.String())
		}
		if strings.Contains(detail.Body.String(), `data-plugin-icon="v1.8"`) != variant {
			t.Fatal("detail header bypassed owning graphic", variant, detail.Body.String())
		}
	}
	order("", false)
	old, oldPolicy := prepareInterface(t, ctx, host, "0.4.0")
	activateInterface(t, ctx, host, "", old, oldPolicy, "graphics-original-three-services")
	order("", false)
	_, profile, composition, card, sidebar, markup, err := a.store.AppsInterfaceSelection(ctx)
	if err != nil || !profile || !composition || !card || !sidebar || markup != 1 {
		t.Fatal("pre-sidebar fixture boundary", profile, composition, card, sidebar, err)
	}
	captured, err := a.appsInterfaceWithHost(ctx, host, "", "en")
	if err != nil {
		t.Fatal(err)
	}
	first, firstPolicy := prepareInterface(t, ctx, host, "1.0.0")
	activateInterface(t, ctx, host, old, first, firstPolicy, "graphics-first")
	order("", false)
	second, secondPolicy := prepareInterface(t, ctx, host, "1.8.0")
	activateInterface(t, ctx, host, first, second, secondPolicy, "graphics-actual-resource-variant")
	order("", true)
	order(first, false)
	order(old, false)
	reader := server.Interface
	server.Interface = func(context.Context, string, string) (web.AppInterfacePolicy, error) { reads++; return captured, nil }
	order("", false)
	server.Interface = reader
	for i, jsonReply := range []bool{true, false} {
		if i > 0 {
			activateInterface(t, ctx, host, first, second, secondPolicy, "graphics-before-native-effect")
		}
		seed()
		spy := &accountCloseStore{Store: a.store, afterSave: func(n int) {
			if n == 1 {
				activateInterface(t, ctx, host, second, first, firstPolicy, "graphics-mid-effect-"+map[bool]string{true: "json", false: "native"}[jsonReply])
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
	activateInterface(t, ctx, host, first, second, secondPolicy, "graphics-cold-baseline")
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
	activateInterface(t, ctx, host, second, first, firstPolicy, "graphics-rollback")
	order("", false)
	current := first
	for _, version := range []string{"4.0.0", "4.2.0"} {
		bad, policy := prepareInterface(t, ctx, host, version)
		activateInterface(t, ctx, host, current, bad, policy, "graphics-invalid-"+version)
		current = bad
		if version == "4.2.0" {
			c, err := readAppsViewContribution(ctx, host, bad, contract.MarkupInterface, contract.MarkupResource)
			var m contract.Markup
			if err != nil || !c.Provided || rsiplugin.DecodeResult(c.Output, &m) != nil || rsiplugin.FileDigest([]byte(m.Source)) != c.Digest {
				t.Fatal("actual healthy /1 contribution not available", c, err)
			}
			base, err := a.appsInterfaceWithHost(ctx, host, old, "en")
			if err != nil {
				t.Fatal(err)
			}
			if _, err := web.ManagedAppMarkup(base, m); err != nil {
				t.Fatal("declared /1 was not independently healthy", err)
			}
		}
		seed()
		if w := get("/settings/apps", ""); w.Code != 503 {
			t.Fatal("bad sidebar silently restored native", version, w.Code)
		}
		if w := get("/api/apps/detail?app=gmail", ""); w.Code != 503 {
			t.Fatal("bad graphics silently restored native detail", version, w.Code, w.Body.String())
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
		// The native response still consumes its selected header graphic even
		// after disconnect removes the final managed sidebar row.
		seed()
		only, err := a.store.LoadApps(ctx)
		if err != nil {
			t.Fatal(err)
		}
		only.Connections = only.Connections[1:]
		if err := a.store.SaveApps(ctx, only, only.Revision); err != nil {
			t.Fatal(err)
		}
		spy := &accountCloseStore{Store: a.store}
		a.connectedApps.Store = spy
		before := reads
		native := post("", false)
		a.connectedApps.Store = a.store
		if native.Code != 200 || reads != before+1 || spy.saves != 2 || !strings.Contains(native.Body.String(), "The account action was accepted.") {
			t.Fatal("empty sidebar hid selected graphic failure/accepted receipt", version, native.Code, reads, before, spy.saves, native.Body.String())
		}
		closed, err := a.store.LoadApps(ctx)
		if err != nil || len(closed.Connections) != 1 || closed.Connections[0].Status != "disconnected" || !closed.Connections[0].RevocationConfirmed {
			t.Fatal("last connection outcome lost", closed, err)
		}
		// Begin independent retained-reference cases after verifying the actual effect.
		seed()
		order(first, false)
		order(old, false)
	}
	activateInterface(t, ctx, host, current, old, oldPolicy, "graphics-current-v1-rollback")
	if _, _, _, _, _, history, err := a.store.AppsInterfaceSelection(ctx); err != nil || history != 2 {
		t.Fatal("highest graphic boundary lost", history, err)
	}
	// A declared actual /1 rollback remains valid after /2,without changing its semantics.
	order("", false)

	missing, missingPolicy := prepareInterface(t, ctx, host, "0.5.0")
	activateInterface(t, ctx, host, old, missing, missingPolicy, "graphics-current-missing")
	if w := get("/settings/apps", ""); w.Code != 503 {
		t.Fatal("current missing graphic restored native", w.Code)
	}
	if w := get("/api/apps/detail?app=gmail", ""); w.Code != 503 {
		t.Fatal("missing graphic detail restored native", w.Code)
	}
	order(missing, false)

	order(old, false)
	activateInterface(t, ctx, host, missing, second, secondPolicy, "graphics-before-quarantine")
	if err := host.Store.Quarantine(ctx, second); err != nil {
		t.Fatal(err)
	}
	if w := get("/settings/apps", second); w.Code != 503 {
		t.Fatal("retained sidebar bypassed quarantine", w.Code)
	}
	order(first, false)
	if _, err := host.Store.Deactivate(ctx, "graphics-stop", second); err != nil {
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
	_, profile, composition, card, sidebar, markup, err = a.store.AppsInterfaceSelection(ctx)
	if err != nil || !profile || !composition || !card || !sidebar || markup == 0 {
		t.Fatal("four durable boundaries lost", profile, composition, card, sidebar, err)
	}
}

package app

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/z-chenhao/assistant/internal/config"
	"github.com/z-chenhao/assistant/internal/connectedapps"
	"github.com/z-chenhao/assistant/internal/connector/composio"
	"github.com/z-chenhao/assistant/internal/interaction/web"
	"github.com/z-chenhao/assistant/internal/llm/memory"
	"github.com/z-chenhao/assistant/internal/rsi"
)

func TestAppsAccountMarkupActualVersionsOriginalEffectAndColdReferences(t *testing.T) {
	testAppsAccountPresentationLifecycle(t, "markup")
}

func TestAppsAccountCopyActualVersionsOriginalEffectAndColdReferences(t *testing.T) {
	testAppsAccountPresentationLifecycle(t, "copy")
}

func testAppsAccountPresentationLifecycle(t *testing.T, kind string) {
	t.Helper()
	copyIteration, connectIteration := kind == "copy", kind == "connect"
	if kind != "copy" && kind != "connect" && kind != "markup" {
		t.Fatal("unknown actual presentation consumer", kind)
	}
	firstVersion, secondVersion := "1.9.0", "1.10.0"
	if copyIteration {
		firstVersion, secondVersion = "1.11.0", "1.12.0"
	}
	if connectIteration {
		firstVersion, secondVersion = "1.13.0", "1.14.0"
	}
	ctx := context.Background()
	root := t.TempDir()
	cfg, err := config.Load("", func(k string) (string, bool) { return root, k == "STEWARD_DATA_ROOT" })
	if err != nil {
		t.Fatal(err)
	}
	secrets := memory.New()
	const token = "synthetic-account-markup-owner-atleast24"
	if err := secrets.Put(ctx, "credential:account-markup-owner", []byte(token)); err != nil {
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
	var peer *httptest.Server
	if connectIteration {
		if err := secrets.Put(ctx, "credential:connect-view-project", []byte("synthetic-connect-view-project")); err != nil {
			t.Fatal(err)
		}
		peer = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			switch r.URL.Path {
			case "/toolkits/gmail":
				_, _ = w.Write([]byte(`{"slug":"gmail","name":"Gmail"}`))
			case "/toolkits":
				_, _ = w.Write([]byte(`{"items":[{"slug":"gmail","name":"Gmail"}]}`))
			default:
				t.Error("unexpected view source", r.Method, r.URL.Path)
				http.NotFound(w, r)
			}
		}))
		defer peer.Close()
	}
	var host rsi.Host
	var server *web.Server
	bind := func() {
		host = a.Plugins()
		host.Catalog.Launcher = carrier
		a.connectedApps.Changed = nil
		if peer != nil {
			a.connectedApps.Client = composio.Client{Endpoint: peer.URL}
		}
		server = &web.Server{Config: web.Config{TokenRef: "credential:account-markup-owner"}, Core: a, Secrets: secrets, Apps: a.connectedApps, Interface: func(c context.Context, ref, locale string) (web.AppInterfacePolicy, error) {
			return a.appsInterfaceWithHost(c, host, ref, locale)
		}}
	}
	bind()
	state, err := a.store.LoadApps(ctx)
	if err != nil {
		t.Fatal(err)
	}
	state.UserID = "owner"
	if connectIteration {
		state.KeyRef = "credential:connect-view-project"
	}
	state.Connections = []connectedapps.Connection{{ID: "keeper", Toolkit: "gmail", Name: "Gmail", Alias: "work", AccountID: "ca_keeper", Status: "active"}, {ID: "closing", Toolkit: "gmail", Name: "Gmail", Alias: "public", Public: true, Status: "active"}, {ID: "unconfirmed", Toolkit: "gmail", Name: "Gmail", Alias: "revocation pending", AccountID: "ca_unconfirmed", Status: "disconnected", RevocationConfirmed: false}}
	if err := a.store.SaveApps(ctx, state, state.Revision); err != nil {
		t.Fatal(err)
	}
	before, err := a.store.LoadApps(ctx)
	if err != nil {
		t.Fatal(err)
	}
	get := func(ref string) *httptest.ResponseRecorder {
		path := "/settings/apps"
		query := url.Values{}
		if connectIteration {
			query.Set("app", "gmail")
		}
		if ref != "" {
			query.Set("interface_revision", ref)
		}
		if len(query) > 0 {
			path += "?" + query.Encode()
		}
		req := httptest.NewRequest("GET", "http://127.0.0.1:8080"+path, nil)
		req.Header.Set("Authorization", "Bearer "+token)
		out := httptest.NewRecorder()
		server.ServeHTTP(out, req)
		return out
	}
	read := func(ref string, variant bool) {
		t.Helper()
		out := get(ref)
		want, other := "Gmail · work", "work · Gmail"
		if variant && kind == "markup" {
			want, other = other, want
		}
		if out.Code != 200 || !strings.Contains(out.Body.String(), want) || strings.Contains(out.Body.String(), other) {
			t.Fatal("owning account markup not selected", ref, out.Code, out.Body.String())
		}
		if connectIteration {
			label := "Account label"
			if variant {
				label = "Account name"
			}
			if !strings.Contains(out.Body.String(), `<label>`+label+`<input name="alias"`) {
				t.Fatal("owning actual connection form source not selected", ref, label)
			}
			if !strings.Contains(out.Body.String(), `pattern="[A-Za-z0-9][A-Za-z0-9_-]{0,63}" maxlength="64" required`) {
				t.Fatal("original alias constraints changed")
			}
		}
		if copyIteration {
			label := "Disconnect"
			if variant {
				label = "Close account"
			}
			if !strings.Contains(out.Body.String(), `>`+label+`</button>`) {
				t.Fatal("original or independently upgraded account label not selected", ref, label)
			}
		}
		if !strings.Contains(out.Body.String(), "Upstream revocation was not confirmed") {
			t.Fatal("original closed/unconfirmed Host disclosure lost across view version or recovery", ref)
		}
		if !strings.Contains(out.Body.String(), `name="connection" value="keeper"`) {
			t.Fatal("original connection identity missing")
		}
	}
	original, originalPolicy := prepareInterface(t, ctx, host, "1.0.0")
	activateInterface(t, ctx, host, "", original, originalPolicy, "account-original-pre-v3")
	read("", false)
	first, firstPolicy := prepareInterface(t, ctx, host, firstVersion)
	activateInterface(t, ctx, host, original, first, firstPolicy, "account-source-first")
	read("", false)
	read(original, false)
	second, secondPolicy := prepareInterface(t, ctx, host, secondVersion)
	activateInterface(t, ctx, host, first, second, secondPolicy, "account-source-upgrade")
	read("", true)
	read(first, false)
	activateInterface(t, ctx, host, second, first, firstPolicy, "account-source-rollback")
	read("", false)
	read(second, true)
	afterReads, err := a.store.LoadApps(ctx)
	if err != nil || !reflect.DeepEqual(before, afterReads) {
		t.Fatal("pure view iteration changed original accounts", err)
	}
	// A submitted old view keeps its exact graph for the accepted effect reply.
	activateInterface(t, ctx, host, first, second, secondPolicy, "account-before-old-view-effect")
	form := url.Values{"action": {"disconnect"}, "connection": {"closing"}, "interface_revision": {first}}
	req := httptest.NewRequest("POST", "http://127.0.0.1:8080/apps/configure", strings.NewReader(form.Encode()))
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")
	out := httptest.NewRecorder()
	server.ServeHTTP(out, req)
	var reply map[string]string
	if err := json.Unmarshal(out.Body.Bytes(), &reply); err != nil || out.Code != 200 || !strings.Contains(reply["accounts_html"], "Gmail · work") || strings.Contains(reply["accounts_html"], "work · Gmail") {
		t.Fatal("effect reply reselected markup", out.Code, out.Body.String(), err)
	}
	if copyIteration && (!strings.Contains(reply["accounts_html"], ">Disconnect</button>") || strings.Contains(reply["accounts_html"], ">Close account</button>")) {
		t.Fatal("accepted old action reply mixed account copy versions")
	}
	afterEffect, err := a.store.LoadApps(ctx)
	if err != nil || afterEffect.Connections[0].Status != "active" || afterEffect.Connections[1].Status != "disconnected" || !afterEffect.Connections[1].RevocationConfirmed {
		t.Fatal("original owner close outcome changed", afterEffect, err)
	}
	if _, err := host.Store.Deactivate(ctx, "account-source-stop", second); err != nil {
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
	// First dependent reads after real recovery: no select, repair or state write.
	read(original, false)
	read(first, false)
	read(second, true)
	if out := get(""); out.Code != 503 {
		t.Fatal("cold deactivation silently restored native", out.Code)
	}
	wantBoundary := 3
	if connectIteration {
		wantBoundary = 4
	}
	if _, _, _, _, _, boundary, err := a.store.AppsInterfaceSelection(ctx); err != nil || boundary != wantBoundary {
		t.Fatal("account migration boundary lost", boundary, err)
	}
	recovered, err := a.store.LoadApps(ctx)
	if err != nil || !reflect.DeepEqual(afterEffect, recovered) {
		t.Fatal("recovery rewrote accepted account outcome", err)
	}
}

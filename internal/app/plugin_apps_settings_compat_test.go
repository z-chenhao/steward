package app

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/z-chenhao/assistant/internal/config"
	"github.com/z-chenhao/assistant/internal/connectedapps"
	"github.com/z-chenhao/assistant/internal/interaction/web"
	"github.com/z-chenhao/assistant/internal/llm/memory"
	"github.com/z-chenhao/assistant/internal/plugin/appsaccounts/contract"
	"github.com/z-chenhao/assistant/internal/rsi"
	"github.com/z-chenhao/assistant/pkg/rsiplugin"
)

func TestAppsSettingsExactPreServiceColdInflightAndIndependentBoundary(t *testing.T) {
	ctx := context.Background()
	directory := retainedAccountPackage(t, "apps-accounts-pre-settings-v1")
	root := t.TempDir()
	cfg, err := config.Load("", func(key string) (string, bool) { return root, key == "STEWARD_DATA_ROOT" })
	if err != nil {
		t.Fatal(err)
	}
	secrets := memory.New()
	token := "synthetic-settings-old-token-at-least24"
	_ = secrets.Put(ctx, "credential:settings-test", []byte(token))
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
		a.connectedApps.ReadSettings = func(c context.Context) (connectedapps.SettingsPolicy, error) { return a.appsSettingsWithHost(c, host) }
		server = &web.Server{Config: web.Config{TokenRef: "credential:settings-test"}, Core: a, Secrets: secrets, Apps: a.connectedApps}
	}
	bind()
	pkg, err := host.Install(ctx, directory)
	if err != nil {
		t.Fatal(err)
	}
	graph, err := host.Prepare(ctx, rsiplugin.Graph{Protocol: rsiplugin.Protocol, Bindings: []rsiplugin.Binding{{InstanceID: "apps-accounts", PackageDigest: pkg.Digest, Config: json.RawMessage(`{}`), StateNamespace: "apps-accounts", StateVersion: 1}}})
	if err != nil {
		t.Fatal(err)
	}
	policy := rsi.Policy{ID: "retained-pre-settings-owner", AllowedContracts: []string{contract.ConnectIntent, contract.AuthConfig, contract.SessionFlow, contract.AccountCheck, contract.AccountClose, contract.Reconnect}, AllowedRequests: []string{"input.read"}, Cases: []rsi.Case{
		{InstanceID: "apps-accounts", Contract: contract.ConnectIntent, Input: json.RawMessage(`{"count":0,"alias":""}`), Inputs: []rsi.InputData{{ID: contract.ConnectInputID}}, Expected: json.RawMessage(`{"kind":"new","alias":"personal"}`)},
		{InstanceID: "apps-accounts", Contract: contract.AuthConfig, Input: json.RawMessage(`{"step":"begin","count":0,"page":0,"explicit":false,"saved":true,"override":false,"no_hosted":false,"has_next":false,"cursor_seen":false,"status":0}`), Inputs: []rsi.InputData{{ID: contract.AuthConfigInputID}}, Expected: json.RawMessage(`{"kind":"fetch","source":"saved"}`)},
		{InstanceID: "apps-accounts", Contract: contract.SessionFlow, Input: json.RawMessage(`{"step":"begin","no_auth":false}`), Expected: json.RawMessage(`{"kind":"toolkit"}`)},
		{InstanceID: "apps-accounts", Contract: contract.AccountCheck, Input: json.RawMessage(`{"public":false,"disabled":false,"active":true,"initializing":false,"initiated":false}`), Expected: json.RawMessage(`{"status":"active"}`)},
		{InstanceID: "apps-accounts", Contract: contract.AccountClose, Input: json.RawMessage(`{"step":"closed","matched":false,"failed":false,"revoked":false}`), Expected: json.RawMessage(`{"kind":"account"}`)},
		{InstanceID: "apps-accounts", Contract: contract.Reconnect, Input: json.RawMessage(`{"step":"begin","pending":true}`), Expected: json.RawMessage(`{"kind":"close"}`)},
	}}
	if err := host.Store.PutPolicy(ctx, policy); err != nil {
		t.Fatal(err)
	}
	if err := host.Propose(ctx, rsi.Candidate{ID: "retained-pre-settings", Target: graph.Revision, PolicyID: policy.ID, Goal: "Retain exact pre-settings contract"}); err != nil {
		t.Fatal(err)
	}
	decision, err := host.Verify(ctx, "retained-pre-settings")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := host.Activate(ctx, rsi.Activation{OperationID: "retained-pre-settings", Target: graph.Revision, DecisionID: decision}); err != nil {
		t.Fatal(err)
	}
	state, err := a.store.LoadApps(ctx)
	if err != nil {
		t.Fatal(err)
	}
	state.KeyRef = "env:COMPOSIO_API_KEY"
	state.AuthConfigs = map[string]string{"a": "ac_a", "b": "ac_b"}
	if err := a.store.SaveApps(ctx, state, state.Revision); err != nil {
		t.Fatal(err)
	}
	send := func(method, path, body string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, "http://127.0.0.1:8080"+path, strings.NewReader(body))
		r.Header.Set("Authorization", "Bearer "+token)
		r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		w := httptest.NewRecorder()
		server.ServeHTTP(w, r)
		return w
	}
	assertOrder := func(w *httptest.ResponseRecorder, first string) {
		t.Helper()
		if w.Code != 200 {
			t.Fatal("settings unavailable", w.Code)
		}
		body := w.Body.String()
		aa, bb := strings.Index(body, `name="app_slug" value="a"`), strings.Index(body, `name="app_slug" value="b"`)
		if aa < 0 || bb < 0 || first == "a" && aa > bb || first == "b" && bb > aa {
			t.Fatal("settings order lost", first, aa, bb)
		}
	}
	get := func() *httptest.ResponseRecorder { return send("GET", "/settings/apps/service", "") }
	assertOrder(get(), "a")
	reopen := func() {
		t.Helper()
		if err := a.Close(); err != nil {
			t.Fatal(err)
		}
		a, err = OpenRuntimeWithSecrets(ctx, cfg, secrets)
		if err != nil {
			t.Fatal(err)
		}
		a.eventsRequired.Store(false)
		bind()
	}
	reopen()
	assertOrder(get(), "a")
	seen, proceed := make(chan struct{}), make(chan struct{})
	unblock := sync.OnceFunc(func() { close(proceed) })
	defer unblock()
	a.connectedApps.ReadSettings = func(c context.Context) (connectedapps.SettingsPolicy, error) {
		selected, err := a.appsSettingsWithHost(c, host)
		close(seen)
		<-proceed
		return selected, err
	}
	done := make(chan *httptest.ResponseRecorder, 1)
	go func() { done <- get() }()
	<-seen
	current, _ := activateAccountIntent(t, ctx, host, graph.Revision, "1.7.0")
	unblock()
	assertOrder(<-done, "a")
	bind()
	assertOrder(get(), "b")
	if err := host.Propose(ctx, rsi.Candidate{ID: "old-settings-after-opt-in", Baseline: current, Target: graph.Revision, PolicyID: policy.ID, Goal: "Verify durable editor boundary"}); err != nil {
		t.Fatal(err)
	}
	decision, err = host.Verify(ctx, "old-settings-after-opt-in")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := host.Activate(ctx, rsi.Activation{OperationID: "old-settings-after-opt-in", Expected: current, Target: graph.Revision, DecisionID: decision}); err != nil {
		t.Fatal(err)
	}
	before, err := a.store.LoadApps(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if response := get(); response.Code < 400 {
		t.Fatal("old6graph undid editor opt-in", response.Code)
	}
	values := url.Values{"revision": {fmt.Sprint(before.Revision)}, "key_ref": {"env:COMPOSIO_API_KEY"}, "editor": {"fields"}, "app_slug": {"a", "b"}, "auth_config": {"ac_a", "ac_b"}, "action": {"add-override"}}
	if response := send("POST", "/apps/service/configure", values.Encode()); response.Code < 400 {
		t.Fatal("missing editor used native draft projection", response.Code)
	}
	after, err := a.store.LoadApps(ctx)
	if err != nil || after.Revision != before.Revision {
		t.Fatal("missing editor mutated source", after, err)
	}
	// Missing editor does not suppress the independent original owner-close API.
	after.Connections = []connectedapps.Connection{{ID: "public-owner", Toolkit: "example", Alias: "local", Status: "active", Public: true}}
	if err := a.store.SaveApps(ctx, after, after.Revision); err != nil {
		t.Fatal(err)
	}
	if err := a.connectedApps.Disconnect(ctx, "public-owner"); err != nil {
		t.Fatal("editor absence blocked independent owner close", err)
	}
	after, err = a.store.LoadApps(ctx)
	if err != nil || after.Connections[0].Status != "disconnected" || !after.Connections[0].RevocationConfirmed {
		t.Fatal("independent owner closure lost", after, err)
	}
	reopen()
	if response := get(); response.Code < 400 {
		t.Fatal("cold old graph restored native editor", response.Code)
	}
}

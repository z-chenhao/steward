package app

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os/exec"
	"path/filepath"
	"sync"
	"testing"

	"github.com/z-chenhao/assistant/internal/config"
	"github.com/z-chenhao/assistant/internal/connectedapps"
	"github.com/z-chenhao/assistant/internal/connector/composio"
	"github.com/z-chenhao/assistant/internal/llm/memory"
	"github.com/z-chenhao/assistant/internal/plugin/appsaccounts/contract"
	"github.com/z-chenhao/assistant/internal/rsi"
	"github.com/z-chenhao/assistant/pkg/rsiplugin"
)

func TestAppsReconnectExactPreServiceColdInflightAndOptInBoundary(t *testing.T) {
	ctx := context.Background()
	directory := retainedAccountPackage(t, "apps-accounts-pre-reconnect-v1")
	root := t.TempDir()
	cfg, err := config.Load("", func(key string) (string, bool) { return root, key == "STEWARD_DATA_ROOT" })
	if err != nil {
		t.Fatal(err)
	}
	secrets := memory.New()
	_ = secrets.Put(ctx, "credential:fixture", []byte("synthetic-reconnect-key"))
	a, err := OpenRuntimeWithSecrets(ctx, cfg, secrets)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = a.Close() }()
	a.eventsRequired.Store(false)
	carrier := filepath.Join(t.TempDir(), "steward")
	if out, err := exec.Command("go", "build", "-o", carrier, "../../cmd/steward").CombinedOutput(); err != nil {
		t.Fatal(string(out), err)
	}
	var mu sync.Mutex
	requests := []string{}
	var entered, release chan struct{}
	peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		requests = append(requests, r.Method+" "+r.URL.RequestURI())
		seen, proceed := entered, release
		if r.Method == "GET" && r.URL.Path == "/connected_accounts/account_original" {
			entered, release = nil, nil
		}
		mu.Unlock()
		if r.Method == "GET" && r.URL.Path == "/connected_accounts/account_original" {
			if seen != nil {
				close(seen)
				<-proceed
			}
			_, _ = w.Write([]byte(`{"id":"account_original","user_id":"owner","toolkit":{"slug":"example"}}`))
			return
		}
		if r.Method == "POST" && r.URL.Path == "/connected_accounts/account_original/revoke" {
			_, _ = w.Write([]byte(`{"connected_account":{"id":"account_original","status":"REVOKED"}}`))
			return
		}
		if r.Method == "DELETE" && r.URL.Path == "/connected_accounts/account_original" {
			w.WriteHeader(204)
			return
		}
		switch r.URL.Path {
		case "/toolkits/example":
			_, _ = w.Write([]byte(`{"slug":"example","name":"Original"}`))
		case "/auth_configs/ac_original":
			_, _ = w.Write([]byte(`{"id":"ac_original","toolkit":{"slug":"example"},"status":"ENABLED","is_composio_managed":false,"is_enabled_for_tool_router":true}`))
		case "/tool_router/session":
			_, _ = w.Write([]byte(`{"session_id":"session_original"}`))
		case "/tool_router/session/session_original/link":
			_, _ = w.Write([]byte(`{"redirect_url":"https://connect.composio.dev/link/original","connected_account_id":"account_new"}`))
		default:
			t.Error("unexpected source", r.Method, r.URL)
			http.NotFound(w, r)
		}
	}))
	defer peer.Close()
	var host rsi.Host
	bind := func() {
		host = a.Plugins()
		host.Catalog.Launcher = carrier
		a.connectedApps.Client = composio.Client{Endpoint: peer.URL}
		a.connectedApps.Changed = nil
		a.connectedApps.ReadAccountPolicies = func(c context.Context) (connectedapps.AccountPolicies, error) {
			return a.appsAccountPoliciesWithHost(c, host)
		}
		a.connectedApps.ReadAccountClose = func(c context.Context) (connectedapps.AccountClosePolicy, error) {
			return a.appsAccountCloseWithHost(c, host)
		}
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
	policy := rsi.Policy{ID: "retained-pre-reconnect-owner", AllowedContracts: []string{contract.ConnectIntent, contract.AuthConfig, contract.SessionFlow, contract.AccountCheck, contract.AccountClose}, AllowedRequests: []string{"input.read"}, Cases: []rsi.Case{
		{InstanceID: "apps-accounts", Contract: contract.ConnectIntent, Input: json.RawMessage(`{"count":0,"alias":""}`), Inputs: []rsi.InputData{{ID: contract.ConnectInputID}}, Expected: json.RawMessage(`{"kind":"new","alias":"personal"}`)},
		{InstanceID: "apps-accounts", Contract: contract.AuthConfig, Input: json.RawMessage(`{"step":"begin","count":0,"page":0,"explicit":false,"saved":true,"override":false,"no_hosted":false,"has_next":false,"cursor_seen":false,"status":0}`), Inputs: []rsi.InputData{{ID: contract.AuthConfigInputID}}, Expected: json.RawMessage(`{"kind":"fetch","source":"saved"}`)},
		{InstanceID: "apps-accounts", Contract: contract.SessionFlow, Input: json.RawMessage(`{"step":"begin","no_auth":false}`), Expected: json.RawMessage(`{"kind":"toolkit"}`)},
		{InstanceID: "apps-accounts", Contract: contract.AccountCheck, Input: json.RawMessage(`{"public":false,"disabled":false,"active":true,"initializing":false,"initiated":false}`), Expected: json.RawMessage(`{"status":"active"}`)},
		{InstanceID: "apps-accounts", Contract: contract.AccountClose, Input: json.RawMessage(`{"step":"closed","matched":false,"failed":false,"revoked":false}`), Expected: json.RawMessage(`{"kind":"account"}`)},
	}}
	if err := host.Store.PutPolicy(ctx, policy); err != nil {
		t.Fatal(err)
	}
	if err := host.Propose(ctx, rsi.Candidate{ID: "retained-pre-reconnect", Target: graph.Revision, PolicyID: policy.ID, Goal: "Retain exact pre-reconnect contract"}); err != nil {
		t.Fatal(err)
	}
	decision, err := host.Verify(ctx, "retained-pre-reconnect")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := host.Activate(ctx, rsi.Activation{OperationID: "retained-pre-reconnect", Target: graph.Revision, DecisionID: decision}); err != nil {
		t.Fatal(err)
	}
	setup := func() connectedapps.State {
		t.Helper()
		state, err := a.store.LoadApps(ctx)
		if err != nil {
			t.Fatal(err)
		}
		state.KeyRef = "credential:fixture"
		state.UserID = "owner"
		state.AuthConfigs = map[string]string{"example": "ac_original"}
		state.Connections = []connectedapps.Connection{{ID: "connection_original", Toolkit: "example", Alias: "work", AccountID: "account_original", Status: "expired"}}
		if err := a.store.SaveApps(ctx, state, state.Revision); err != nil {
			t.Fatal(err)
		}
		state, err = a.store.LoadApps(ctx)
		if err != nil {
			t.Fatal(err)
		}
		mu.Lock()
		requests = []string{}
		mu.Unlock()
		return state
	}
	trace := func() []string { mu.Lock(); defer mu.Unlock(); return append([]string{}, requests...) }
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
	before := setup()
	link, err := a.connectedApps.Reconnect(ctx, "connection_original")
	if err != nil || link.ConnectionID == "" || len(trace()) != 7 {
		t.Fatal("original full reconnect changed", link, err, trace())
	}
	state, err := a.store.LoadApps(ctx)
	if err != nil || connectedapps.BindingHash(before, before.Connections[0]) != connectedapps.BindingHash(state, state.Connections[0]) {
		t.Fatal("old identity changed", state, err)
	}
	reopen()
	if _, err := a.connectedApps.Continue(ctx, link.ConnectionID); !errors.Is(err, connectedapps.ErrLinkExpired) {
		t.Fatal("cold old cache loss reissued link", err)
	}
	setup()
	if link, err := a.connectedApps.Reconnect(ctx, "connection_original"); err != nil || link.ConnectionID == "" || len(trace()) != 7 {
		t.Fatal("cold old recipe lost", link, err, trace())
	}
	setup()
	seen, proceed := make(chan struct{}), make(chan struct{})
	unblock := sync.OnceFunc(func() { close(proceed) })
	defer unblock()
	mu.Lock()
	entered, release = seen, proceed
	mu.Unlock()
	type result struct {
		link composio.Link
		err  error
	}
	done := make(chan result, 1)
	go func() { link, err := a.connectedApps.Reconnect(ctx, "connection_original"); done <- result{link, err} }()
	<-seen
	current, _ := activateAccountIntent(t, ctx, host, graph.Revision, "1.6.0")
	unblock()
	old := <-done
	if old.err != nil || old.link.ConnectionID == "" || len(trace()) != 7 {
		t.Fatal("first opt-in reselected native old recipe", old, trace())
	}
	before = setup()
	if _, err := a.connectedApps.Reconnect(ctx, "connection_original"); !errors.Is(err, connectedapps.ErrDenied) || len(trace()) != 0 {
		t.Fatal("new conservative recipe ignored", err, trace())
	}
	if err := host.Propose(ctx, rsi.Candidate{ID: "old-reconnect-after-opt-in", Baseline: current, Target: graph.Revision, PolicyID: policy.ID, Goal: "Verify independent persistent reconnect boundary"}); err != nil {
		t.Fatal(err)
	}
	decision, err = host.Verify(ctx, "old-reconnect-after-opt-in")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := host.Activate(ctx, rsi.Activation{OperationID: "old-reconnect-after-opt-in", Expected: current, Target: graph.Revision, DecisionID: decision}); err != nil {
		t.Fatal(err)
	}
	if _, err := a.connectedApps.Reconnect(ctx, "connection_original"); !errors.Is(err, connectedapps.ErrUnavailable) || len(trace()) != 0 {
		t.Fatal("old5graph undid reconnect opt-in", err, trace())
	}
	state, err = a.store.LoadApps(ctx)
	if err != nil || state.Revision != before.Revision || state.Connections[0].Status != "expired" {
		t.Fatal("missing recipe closed original binding", state, err)
	}
	another, err := a.connectedApps.Connect(ctx, "example", "another", "")
	if err != nil || another.ConnectionID == "" || len(trace()) != 4 {
		t.Fatal("independent original creation disabled by missing reconnect", another, err, trace())
	}
	if cached, err := a.connectedApps.Continue(ctx, another.ConnectionID); err != nil || cached != another {
		t.Fatal("accepted original service cache lost", cached, err)
	}
	setup()
	reopen()
	if _, err := a.connectedApps.Reconnect(ctx, "connection_original"); !errors.Is(err, connectedapps.ErrUnavailable) || len(trace()) != 0 {
		t.Fatal("cold old graph restored native reconnect", err, trace())
	}
	if err := a.connectedApps.Disconnect(ctx, "connection_original"); err != nil || len(trace()) != 3 {
		t.Fatal("missing reconnect disabled independent owner cleanup", err, trace())
	}
	state, err = a.store.LoadApps(ctx)
	if err != nil || state.Connections[0].Status != "disconnected" || !state.Connections[0].RevocationConfirmed {
		t.Fatal("independent owner revoke lost source proof", state, err)
	}
}

package app

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"github.com/z-chenhao/assistant/internal/config"
	"github.com/z-chenhao/assistant/internal/connectedapps"
	"github.com/z-chenhao/assistant/internal/connector/composio"
	"github.com/z-chenhao/assistant/internal/llm/memory"
	"github.com/z-chenhao/assistant/internal/plugin/appsaccounts/contract"
	"github.com/z-chenhao/assistant/internal/rsi"
	rsistore "github.com/z-chenhao/assistant/internal/rsi/sqlite"
	"github.com/z-chenhao/assistant/pkg/rsiplugin"
	"net/http"
	"net/http/httptest"
	"os/exec"
	"path/filepath"
	"reflect"
	"sync"
	"testing"
)

func TestAppsAccountCloseExactPreServiceColdInflightAndIndependentOwnerBoundary(t *testing.T) {
	ctx := context.Background()
	directory := retainedAccountPackage(t, "apps-accounts-pre-close-v1")
	root := t.TempDir()
	cfg, err := config.Load("", func(key string) (string, bool) { return root, key == "STEWARD_DATA_ROOT" })
	if err != nil {
		t.Fatal(err)
	}
	secrets := memory.New()
	_ = secrets.Put(ctx, "credential:fixture", []byte("synthetic-close-key"))
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
	policy := rsi.Policy{ID: "retained-pre-close-owner", AllowedContracts: []string{contract.ConnectIntent, contract.AuthConfig, contract.SessionFlow, contract.AccountCheck}, AllowedRequests: []string{"input.read"}, Cases: []rsi.Case{
		{InstanceID: "apps-accounts", Contract: contract.ConnectIntent, Input: json.RawMessage(`{"count":0,"alias":""}`), Inputs: []rsi.InputData{{ID: contract.ConnectInputID}}, Expected: json.RawMessage(`{"kind":"new","alias":"personal"}`)},
		{InstanceID: "apps-accounts", Contract: contract.AuthConfig, Input: json.RawMessage(`{"step":"begin","count":0,"page":0,"explicit":false,"saved":true,"override":false,"no_hosted":false,"has_next":false,"cursor_seen":false,"status":0}`), Inputs: []rsi.InputData{{ID: contract.AuthConfigInputID}}, Expected: json.RawMessage(`{"kind":"fetch","source":"saved"}`)},
		{InstanceID: "apps-accounts", Contract: contract.SessionFlow, Input: json.RawMessage(`{"step":"begin","no_auth":false}`), Expected: json.RawMessage(`{"kind":"toolkit"}`)},
		{InstanceID: "apps-accounts", Contract: contract.AccountCheck, Input: json.RawMessage(`{"public":false,"disabled":false,"active":true,"initializing":false,"initiated":false}`), Expected: json.RawMessage(`{"status":"active"}`)},
	}}
	if err := host.Store.PutPolicy(ctx, policy); err != nil {
		t.Fatal(err)
	}
	if err := host.Propose(ctx, rsi.Candidate{ID: "retained-pre-close", Target: graph.Revision, PolicyID: policy.ID, Goal: "Retain exact pre-close contract"}); err != nil {
		t.Fatal(err)
	}
	decision, err := host.Verify(ctx, "retained-pre-close")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := host.Activate(ctx, rsi.Activation{OperationID: "retained-pre-close", Target: graph.Revision, DecisionID: decision}); err != nil {
		t.Fatal(err)
	}
	setup := func(status string) connectedapps.State {
		t.Helper()
		state, err := a.store.LoadApps(ctx)
		if err != nil {
			t.Fatal(err)
		}
		state.KeyRef = "credential:fixture"
		state.UserID = "owner"
		state.AuthConfigs = map[string]string{"example": "ac_original"}
		state.Connections = []connectedapps.Connection{{ID: "connection_original", Toolkit: "example", Alias: "work", AccountID: "account_original", Status: status}}
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
	oldTrace := []string{"GET /connected_accounts/account_original", "POST /connected_accounts/account_original/revoke", "DELETE /connected_accounts/account_original"}
	setup("active")
	if err := a.connectedApps.Disconnect(ctx, "connection_original"); err != nil || !reflect.DeepEqual(trace(), oldTrace) {
		t.Fatal("old source cleanup changed", err, trace())
	}
	reopen()
	setup("active")
	if err := a.connectedApps.Disconnect(ctx, "connection_original"); err != nil || !reflect.DeepEqual(trace(), oldTrace) {
		t.Fatal("cold exact old cleanup changed", err, trace())
	}
	// Other business authority may fail, while this still-native owner feature
	// independently selects its exact pre-opt-in interpretation from durable SQL.
	denied := host
	base := *host.Store.(*rsistore.Store)
	base.Fence = func(context.Context, *sql.Tx) error { return rsi.ErrRevoked }
	denied.Store = &base
	a.connectedApps.ReadAccountPolicies = func(context.Context) (connectedapps.AccountPolicies, error) {
		return connectedapps.AccountPolicies{}, rsi.ErrRevoked
	}
	a.connectedApps.ReadAccountClose = func(c context.Context) (connectedapps.AccountClosePolicy, error) {
		return a.appsAccountCloseWithHost(c, denied)
	}
	setup("active")
	if err := a.connectedApps.Disconnect(ctx, "connection_original"); err != nil || !reflect.DeepEqual(trace(), oldTrace) {
		t.Fatal("unrelated graph failure suppressed pre-opt-in owner cleanup", err, trace())
	}
	bind()
	setup("active")
	seen, proceed := make(chan struct{}), make(chan struct{})
	unblock := sync.OnceFunc(func() { close(proceed) })
	defer unblock()
	mu.Lock()
	entered, release = seen, proceed
	mu.Unlock()
	done := make(chan error, 1)
	go func() { done <- a.connectedApps.Disconnect(ctx, "connection_original") }()
	<-seen
	current, _ := activateAccountIntent(t, ctx, host, graph.Revision, "1.5.0")
	unblock()
	if err := <-done; err != nil || !reflect.DeepEqual(trace(), oldTrace) {
		t.Fatal("first opt-in reselected old owner cleanup", err, trace())
	}
	setup("active")
	if err := a.connectedApps.Disconnect(ctx, "connection_original"); err != nil || len(trace()) != 2 {
		t.Fatal("new cleanup policy not used", err, trace())
	}
	// Reconnect freezes the cleanup facet together with original creation facets.
	before := setup("pending")
	seen2, proceed2 := make(chan struct{}), make(chan struct{})
	unblock2 := sync.OnceFunc(func() { close(proceed2) })
	defer unblock2()
	mu.Lock()
	entered, release = seen2, proceed2
	mu.Unlock()
	type result struct {
		link composio.Link
		err  error
	}
	reconnected := make(chan result, 1)
	go func() {
		link, err := a.connectedApps.Reconnect(ctx, "connection_original")
		reconnected <- result{link, err}
	}()
	<-seen2
	// Restore the original cleanup recipe during I/O; this already selected1.5
	// reconnect retains record-preserving cleanup and creates one new binding.
	if err := host.Propose(ctx, rsi.Candidate{ID: "close-reconnect-original", Baseline: current, Target: graph.Revision, PolicyID: policy.ID, Goal: "Exercise frozen close selection"}); err != nil {
		t.Fatal(err)
	}
	decision, err = host.Verify(ctx, "close-reconnect-original")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := host.Activate(ctx, rsi.Activation{OperationID: "close-reconnect-original", Expected: current, Target: graph.Revision, DecisionID: decision}); err != nil {
		t.Fatal(err)
	}
	unblock2()
	out := <-reconnected
	if out.err != nil || out.link.ConnectionID == "" || out.link.ConnectionID == "connection_original" {
		t.Fatal("reconnect lost frozen creation selection", out)
	}
	for _, request := range trace() {
		if request == "DELETE /connected_accounts/account_original" {
			t.Fatal("inflight reconnect reselected new missing-close graph", trace())
		}
	}
	state, err := a.store.LoadApps(ctx)
	if err != nil || len(state.Connections) != 2 || state.Connections[0].Status != "disconnected" || !state.Connections[0].RevocationConfirmed || state.Connections[1].ID != out.link.ConnectionID || connectedapps.BindingHash(before, before.Connections[0]) != connectedapps.BindingHash(state, state.Connections[0]) {
		t.Fatal("reconnect changed original identity or lost source proof", state, err)
	}
	// New reconnect sees durable close opt-in and rejects the old4graph before it
	// can close the pending binding; explicit owner Disconnect remains available.
	before = setup("pending")
	if _, err := a.connectedApps.Reconnect(ctx, "connection_original"); !errors.Is(err, connectedapps.ErrUnavailable) {
		t.Fatal("missing cleanup closed original pending before failure", err)
	}
	unchanged, err := a.store.LoadApps(ctx)
	if err != nil || unchanged.Revision != before.Revision || unchanged.Connections[0].Status != "pending" || len(trace()) != 0 {
		t.Fatal("missing cleanup modified original pending", unchanged, trace(), err)
	}
	if err := a.connectedApps.Disconnect(ctx, "connection_original"); err != nil || len(trace()) != 0 {
		t.Fatal("old graph used native after cleanup opt-in or blocked owner closure", err, trace())
	}
	state, err = a.store.LoadApps(ctx)
	if err != nil || state.Connections[0].Status != "disconnected" || state.Connections[0].RevocationConfirmed {
		t.Fatal("unavailable cleanup forged proof", state, err)
	}
	setup("active")
	reopen()
	if err := a.connectedApps.Disconnect(ctx, "connection_original"); err != nil || len(trace()) != 0 {
		t.Fatal("cold old graph undid durable cleanup boundary", err, trace())
	}
}

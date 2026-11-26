package app

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/z-chenhao/assistant/internal/config"
	"github.com/z-chenhao/assistant/internal/connectedapps"
	"github.com/z-chenhao/assistant/internal/connector/composio"
	"github.com/z-chenhao/assistant/internal/llm/memory"
	"github.com/z-chenhao/assistant/internal/plugin/appsaccounts/contract"
	"github.com/z-chenhao/assistant/internal/rsi"
	"github.com/z-chenhao/assistant/pkg/rsiplugin"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sync"
	"testing"
)

func TestAppsAccountCheckExactPreServiceColdInflightAndOptInBoundary(t *testing.T) {
	ctx := context.Background()
	directory := retainedAccountPackage(t, "apps-accounts-pre-check-v1")
	root := t.TempDir()
	cfg, err := config.Load("", func(key string) (string, bool) { return root, key == "STEWARD_DATA_ROOT" })
	if err != nil {
		t.Fatal(err)
	}
	secrets := memory.New()
	_ = secrets.Put(ctx, "credential:fixture", []byte("synthetic-check-old-key"))
	a, err := OpenRuntimeWithSecrets(ctx, cfg, secrets)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = a.Close() }()
	a.eventsRequired.Store(false)
	carrier := filepath.Join(t.TempDir(), "steward")
	buildTestBinary(t, "../../cmd/steward", carrier)
	var mu sync.Mutex
	reads, posts := 0, 0
	var entered, release chan struct{}
	peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/toolkits/example":
			_, _ = w.Write([]byte(`{"slug":"example","name":"Original"}`))
		case "/auth_configs/ac_original":
			_, _ = w.Write([]byte(`{"id":"ac_original","toolkit":{"slug":"example"},"status":"ENABLED","is_composio_managed":false,"is_enabled_for_tool_router":true}`))
		case "/tool_router/session":
			mu.Lock()
			posts++
			mu.Unlock()
			_, _ = w.Write([]byte(`{"session_id":"session_original"}`))
		case "/tool_router/session/session_original/link":
			_, _ = w.Write([]byte(`{"redirect_url":"https://connect.composio.dev/link/original","connected_account_id":"account_original"}`))
		case "/connected_accounts/account_original":
			mu.Lock()
			reads++
			seen, proceed := entered, release
			entered, release = nil, nil
			mu.Unlock()
			if seen != nil {
				close(seen)
				<-proceed
			}
			_, _ = w.Write([]byte(`{"id":"account_original","user_id":"owner","status":"INITIATED","toolkit":{"slug":"example"}}`))
		default:
			t.Error("unexpected source", r.URL)
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
	policy := rsi.Policy{ID: "retained-pre-check-owner", AllowedContracts: []string{contract.ConnectIntent, contract.AuthConfig, contract.SessionFlow}, AllowedRequests: []string{"input.read"}, Cases: []rsi.Case{
		{InstanceID: "apps-accounts", Contract: contract.ConnectIntent, Input: json.RawMessage(`{"count":0,"alias":""}`), Inputs: []rsi.InputData{{ID: contract.ConnectInputID}}, Expected: json.RawMessage(`{"kind":"new","alias":"personal"}`)},
		{InstanceID: "apps-accounts", Contract: contract.AuthConfig, Input: json.RawMessage(`{"step":"begin","count":0,"page":0,"explicit":false,"saved":true,"override":false,"no_hosted":false,"has_next":false,"cursor_seen":false,"status":0}`), Inputs: []rsi.InputData{{ID: contract.AuthConfigInputID}}, Expected: json.RawMessage(`{"kind":"fetch","source":"saved"}`)},
		{InstanceID: "apps-accounts", Contract: contract.SessionFlow, Input: json.RawMessage(`{"step":"begin","no_auth":false}`), Expected: json.RawMessage(`{"kind":"toolkit"}`)},
	}}
	if err := host.Store.PutPolicy(ctx, policy); err != nil {
		t.Fatal(err)
	}
	if err := host.Propose(ctx, rsi.Candidate{ID: "retained-pre-check", Target: graph.Revision, PolicyID: policy.ID, Goal: "Retain exact original pre-check contract"}); err != nil {
		t.Fatal(err)
	}
	decision, err := host.Verify(ctx, "retained-pre-check")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := host.Activate(ctx, rsi.Activation{OperationID: "retained-pre-check", Target: graph.Revision, DecisionID: decision}); err != nil {
		t.Fatal(err)
	}
	state, err := a.store.LoadApps(ctx)
	if err != nil {
		t.Fatal(err)
	}
	state.KeyRef = "credential:fixture"
	state.UserID = "owner"
	state.AuthConfigs = map[string]string{"example": "ac_original"}
	if err := a.store.SaveApps(ctx, state, state.Revision); err != nil {
		t.Fatal(err)
	}
	link, err := a.connectedApps.Connect(ctx, "example", "work", "")
	if err != nil {
		t.Fatal(err)
	}
	state, err = a.store.LoadApps(ctx)
	if err != nil {
		t.Fatal(err)
	}
	hash := connectedapps.BindingHash(state, state.Connections[0])
	checkStatus := func(want string) {
		t.Helper()
		if err := a.connectedApps.Check(ctx, link.ConnectionID); err != nil {
			t.Fatal(err)
		}
		v, err := a.store.LoadApps(ctx)
		if err != nil || v.Connections[0].Status != want || connectedapps.BindingHash(v, v.Connections[0]) != hash {
			t.Fatal("original binding/status changed", want, v, err)
		}
	}
	checkStatus("pending")
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
	checkStatus("pending")
	seen, proceed := make(chan struct{}), make(chan struct{})
	releaseSource := sync.OnceFunc(func() { close(proceed) })
	defer releaseSource()
	mu.Lock()
	entered, release = seen, proceed
	mu.Unlock()
	done := make(chan error, 1)
	go func() { done <- a.connectedApps.Check(ctx, link.ConnectionID) }()
	<-seen
	current, _ := activateAccountIntent(t, ctx, host, graph.Revision, "1.4.0")
	releaseSource()
	if err := <-done; err != nil {
		t.Fatal("inflight original interpretation reselected", err)
	}
	state, err = a.store.LoadApps(ctx)
	if err != nil || state.Connections[0].Status != "pending" {
		t.Fatal("inflight pre-opt-in changed", state, err)
	}
	checkStatus("expired")
	if err := host.Propose(ctx, rsi.Candidate{ID: "old-check-after-opt-in", Baseline: current, Target: graph.Revision, PolicyID: policy.ID, Goal: "Verify independent durable check boundary"}); err != nil {
		t.Fatal(err)
	}
	decision, err = host.Verify(ctx, "old-check-after-opt-in")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := host.Activate(ctx, rsi.Activation{OperationID: "old-check-after-opt-in", Expected: current, Target: graph.Revision, DecisionID: decision}); err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	beforeReads, beforePosts := reads, posts
	mu.Unlock()
	if err := a.connectedApps.Check(ctx, link.ConnectionID); !errors.Is(err, connectedapps.ErrUnavailable) {
		t.Fatal("old graph undid check opt-in", err)
	}
	// Check is independently unavailable; exact original intent/auth/session
	// services may still create an explicitly requested different account label.
	another, err := a.connectedApps.Connect(ctx, "example", "another", "")
	if err != nil {
		t.Fatal("check boundary disabled independent creation services", err)
	}
	if err := a.connectedApps.Check(ctx, another.ConnectionID); !errors.Is(err, connectedapps.ErrUnavailable) {
		t.Fatal("new check used native interpreter", err)
	}
	if cached, err := a.connectedApps.Continue(ctx, another.ConnectionID); err != nil || cached != another {
		t.Fatal("independent accepted link lost", cached, err)
	}
	mu.Lock()
	gotReads, gotPosts := reads, posts
	mu.Unlock()
	if gotReads != beforeReads || gotPosts != beforePosts+1 {
		t.Fatal("missing check method reached source or replayed creation", gotReads, gotPosts)
	}
	reopen()
	if err := a.connectedApps.Check(ctx, another.ConnectionID); !errors.Is(err, connectedapps.ErrUnavailable) {
		t.Fatal("cold old graph cancelled check opt-in", err)
	}
	if _, err := a.connectedApps.Continue(ctx, another.ConnectionID); !errors.Is(err, connectedapps.ErrLinkExpired) {
		t.Fatal("cold cache loss reissued a link", err)
	}
	mu.Lock()
	gotReads, gotPosts = reads, posts
	mu.Unlock()
	if gotReads != beforeReads || gotPosts != beforePosts+1 {
		t.Fatal("cold old graph replayed source", gotReads, gotPosts)
	}
}

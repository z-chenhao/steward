package app

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/z-chenhao/assistant/internal/config"
	"github.com/z-chenhao/assistant/internal/connectedapps"
	"github.com/z-chenhao/assistant/internal/connector/composio"
	"github.com/z-chenhao/assistant/internal/llm/memory"
	"github.com/z-chenhao/assistant/internal/plugin/appsaccounts/contract"
	"github.com/z-chenhao/assistant/internal/rsi"
	"github.com/z-chenhao/assistant/pkg/rsiplugin"
)

func TestAppsSessionFlowExactPreServiceColdInflightAndOptInBoundary(t *testing.T) {
	ctx := context.Background()
	directory := retainedAccountPackage(t, "apps-accounts-pre-session-v1")
	root := t.TempDir()
	cfg, err := config.Load("", func(key string) (string, bool) { return root, key == "STEWARD_DATA_ROOT" })
	if err != nil {
		t.Fatal(err)
	}
	secrets := memory.New()
	_ = secrets.Put(ctx, "credential:fixture", []byte("synthetic-old-session-key"))
	a, err := OpenRuntimeWithSecrets(ctx, cfg, secrets)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = a.Close() }()
	a.eventsRequired.Store(false)
	carrier := filepath.Join(t.TempDir(), "steward")
	buildTestBinary(t, "../../cmd/steward", carrier)
	var mu sync.Mutex
	calls := 0
	var entered, release chan struct{}
	peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		calls++
		seen, proceed := entered, release
		if r.URL.Path == "/tool_router/session" {
			entered, release = nil, nil
		}
		mu.Unlock()
		switch r.URL.Path {
		case "/toolkits/example":
			_, _ = w.Write([]byte(`{"slug":"example","name":"Original"}`))
		case "/auth_configs/ac_original":
			_, _ = w.Write([]byte(`{"id":"ac_original","toolkit":{"slug":"example"},"status":"ENABLED","is_composio_managed":false,"is_enabled_for_tool_router":true}`))
		case "/tool_router/session":
			if seen != nil {
				close(seen)
				<-proceed
			}
			_, _ = w.Write([]byte(`{"session_id":"session_original"}`))
		case "/tool_router/session/session_original/link":
			_, _ = w.Write([]byte(`{"redirect_url":"https://connect.composio.dev/link/original","connected_account_id":"account_original"}`))
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
	policy := rsi.Policy{ID: "retained-pre-session-owner", AllowedContracts: []string{contract.ConnectIntent, contract.AuthConfig}, AllowedRequests: []string{"input.read"}, Cases: []rsi.Case{
		{InstanceID: "apps-accounts", Contract: contract.ConnectIntent, Input: json.RawMessage(`{"count":0,"alias":""}`), Inputs: []rsi.InputData{{ID: contract.ConnectInputID}}, Expected: json.RawMessage(`{"kind":"new","alias":"personal"}`)},
		{InstanceID: "apps-accounts", Contract: contract.AuthConfig, Input: json.RawMessage(`{"step":"begin","count":0,"page":0,"explicit":false,"saved":true,"override":false,"no_hosted":false,"has_next":false,"cursor_seen":false,"status":0}`), Inputs: []rsi.InputData{{ID: contract.AuthConfigInputID}}, Expected: json.RawMessage(`{"kind":"fetch","source":"saved"}`)},
	}}
	if err := host.Store.PutPolicy(ctx, policy); err != nil {
		t.Fatal(err)
	}
	if err := host.Propose(ctx, rsi.Candidate{ID: "retained-pre-session", Target: graph.Revision, PolicyID: policy.ID, Goal: "Retain exact pre-session caller"}); err != nil {
		t.Fatal(err)
	}
	decision, err := host.Verify(ctx, "retained-pre-session")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := host.Activate(ctx, rsi.Activation{OperationID: "retained-pre-session", Target: graph.Revision, DecisionID: decision}); err != nil {
		t.Fatal(err)
	}
	reset := func() {
		t.Helper()
		state, err := a.store.LoadApps(ctx)
		if err != nil {
			t.Fatal(err)
		}
		state.KeyRef = "credential:fixture"
		state.UserID = "owner"
		state.AuthConfigs = map[string]string{"example": "ac_original"}
		state.Connections = nil
		if err := a.store.SaveApps(ctx, state, state.Revision); err != nil {
			t.Fatal(err)
		}
		mu.Lock()
		calls = 0
		mu.Unlock()
	}
	check := func(seconds int) composio.Link {
		t.Helper()
		link, err := a.connectedApps.Connect(ctx, "example", "work", "")
		if err != nil || int(time.Until(link.ExpiresAt).Round(time.Second)/time.Second) != seconds {
			t.Fatal("actual workflow/lifetime changed", seconds, link, err)
		}
		mu.Lock()
		n := calls
		mu.Unlock()
		if n != 4 {
			t.Fatal("actual source sequence changed", n)
		}
		return link
	}
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
	reset()
	check(600)
	reopen()
	reset()
	check(600)
	reset()
	seen, proceed := make(chan struct{}), make(chan struct{})
	releaseSource := sync.OnceFunc(func() { close(proceed) })
	defer releaseSource()
	mu.Lock()
	entered, release = seen, proceed
	mu.Unlock()
	type result struct {
		link composio.Link
		err  error
	}
	done := make(chan result, 1)
	go func() { link, err := a.connectedApps.Connect(ctx, "example", "work", ""); done <- result{link, err} }()
	<-seen
	current, _ := activateAccountIntent(t, ctx, host, graph.Revision, "1.3.0")
	releaseSource()
	old := <-done
	if old.err != nil || int(time.Until(old.link.ExpiresAt).Round(time.Second)/time.Second) != 600 {
		t.Fatal("inflight pre-service workflow reselected", old.err, old.link.ExpiresAt)
	}
	reset()
	link := check(300)
	state, err := a.store.LoadApps(ctx)
	if err != nil {
		t.Fatal(err)
	}
	hash := connectedapps.BindingHash(state, state.Connections[0])
	if err := host.Propose(ctx, rsi.Candidate{ID: "old-session-after-opt-in", Baseline: current, Target: graph.Revision, PolicyID: policy.ID, Goal: "Verify durable workflow boundary"}); err != nil {
		t.Fatal(err)
	}
	decision, err = host.Verify(ctx, "old-session-after-opt-in")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := host.Activate(ctx, rsi.Activation{OperationID: "old-session-after-opt-in", Expected: current, Target: graph.Revision, DecisionID: decision}); err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	calls = 0
	mu.Unlock()
	if _, err := a.connectedApps.Reconnect(ctx, link.ConnectionID); !errors.Is(err, connectedapps.ErrUnavailable) {
		t.Fatal("missing workflow closed original pending binding", err)
	}
	still, err := a.store.LoadApps(ctx)
	if err != nil || still.Connections[0].Status != "pending" || connectedapps.BindingHash(still, still.Connections[0]) != hash {
		t.Fatal("old binding changed", still, err)
	}
	if resumed, err := a.connectedApps.Continue(ctx, link.ConnectionID); err != nil || resumed != link {
		t.Fatal("accepted cache authority depended on new candidate", resumed, err)
	}
	if _, err := a.connectedApps.Connect(ctx, "example", "another", ""); !errors.Is(err, connectedapps.ErrUnavailable) {
		t.Fatal("old graph undid workflow opt-in", err)
	}
	mu.Lock()
	n := calls
	mu.Unlock()
	if n != 0 {
		t.Fatal("missing method reached native source", n)
	}
	reopen()
	if _, err := a.connectedApps.Continue(ctx, link.ConnectionID); !errors.Is(err, connectedapps.ErrLinkExpired) {
		t.Fatal("cold cache loss reissued link", err)
	}
	if _, err := a.connectedApps.Connect(ctx, "example", "another", ""); !errors.Is(err, connectedapps.ErrUnavailable) {
		t.Fatal("cold graph undid opt-in", err)
	}
	mu.Lock()
	n = calls
	mu.Unlock()
	if n != 0 {
		t.Fatal("cold missing method contacted source", n)
	}
}

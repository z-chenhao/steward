package app

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os/exec"
	"path/filepath"
	"strings"
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

func TestAppsAuthConfigExactPreServiceColdInflightAndOptInBoundary(t *testing.T) {
	ctx := context.Background()
	directory := retainedAccountPackage(t, "apps-accounts-pre-auth-v1")
	root := t.TempDir()
	cfg, err := config.Load("", func(key string) (string, bool) { return root, key == "STEWARD_DATA_ROOT" })
	if err != nil {
		t.Fatal(err)
	}
	secrets := memory.New()
	if err := secrets.Put(ctx, "credential:old-auth", []byte("synthetic-old-key")); err != nil {
		t.Fatal(err)
	}
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
	var limits []string
	chosen := ""
	posts := 0
	var entered, release chan struct{}
	peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/toolkits/example":
			_, _ = w.Write([]byte(`{"slug":"example","name":"Example"}`))
		case "/auth_configs":
			mu.Lock()
			limits = append(limits, r.URL.Query().Get("limit"))
			seen, wait := entered, release
			entered, release = nil, nil
			mu.Unlock()
			if seen != nil {
				close(seen)
				<-wait
			}
			_, _ = w.Write([]byte(`{"items":[{"id":"ac_old","created_at":"2025","toolkit":{"slug":"example"},"status":"ENABLED","is_enabled_for_tool_router":true},{"id":"ac_new","created_at":"2026","toolkit":{"slug":"example"},"status":"ENABLED","is_enabled_for_tool_router":true}]}`))
		case "/tool_router/session":
			var input struct {
				Configs map[string]string `json:"auth_configs"`
			}
			if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
				t.Error(err)
			}
			mu.Lock()
			posts++
			chosen = input.Configs["example"]
			mu.Unlock()
			_, _ = w.Write([]byte(`{"session_id":"old_session"}`))
		case "/tool_router/session/old_session/link":
			_, _ = w.Write([]byte(`{"redirect_url":"https://connect.composio.dev/link/old","connected_account_id":"old_account"}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer peer.Close()
	var host rsi.Host
	bind := func() {
		host = a.Plugins()
		host.Catalog.Launcher = carrier
		a.connectedApps.Client = composio.Client{Endpoint: peer.URL}
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
	policy := rsi.Policy{ID: "retained-pre-auth-owner", AllowedContracts: []string{contract.ConnectIntent}, AllowedRequests: []string{"input.read"}, Cases: []rsi.Case{{InstanceID: "apps-accounts", Contract: contract.ConnectIntent, Input: json.RawMessage(`{"count":0,"alias":""}`), Inputs: []rsi.InputData{{ID: contract.ConnectInputID}}, Expected: json.RawMessage(`{"kind":"new","alias":"personal"}`)}}}
	if err := host.Store.PutPolicy(ctx, policy); err != nil {
		t.Fatal(err)
	}
	if err := host.Propose(ctx, rsi.Candidate{ID: "retained-pre-auth", Target: graph.Revision, PolicyID: policy.ID, Goal: "Retain exact original pre-auth caller"}); err != nil {
		t.Fatal(err)
	}
	decision, err := host.Verify(ctx, "retained-pre-auth")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := host.Activate(ctx, rsi.Activation{OperationID: "retained-pre-auth", Target: graph.Revision, DecisionID: decision}); err != nil {
		t.Fatal(err)
	}
	reset := func() {
		state, err := a.store.LoadApps(ctx)
		if err != nil {
			t.Fatal(err)
		}
		state.KeyRef = "credential:old-auth"
		state.UserID = "old_owner"
		state.Connections = nil
		if err := a.store.SaveApps(ctx, state, state.Revision); err != nil {
			t.Fatal(err)
		}
		mu.Lock()
		limits = nil
		posts = 0
		chosen = ""
		mu.Unlock()
	}
	check := func(wantID, wantLimit string) {
		t.Helper()
		mu.Lock()
		defer mu.Unlock()
		if posts != 1 || chosen != wantID || len(limits) != 1 || limits[0] != wantLimit {
			t.Fatal("actual old/new source mismatch", posts, chosen, limits)
		}
	}
	reset()
	if _, err := a.connectedApps.Connect(ctx, "example", "work", ""); err != nil {
		t.Fatal(err)
	}
	check("ac_new", "200")
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
	reset()
	if _, err := a.connectedApps.Connect(ctx, "example", "work", ""); err != nil {
		t.Fatal("cold exact old graph lost original interpretation", err)
	}
	check("ac_new", "200")
	reset()
	seen, wait := make(chan struct{}), make(chan struct{})
	releaseSource := sync.OnceFunc(func() { close(wait) })
	defer releaseSource()
	mu.Lock()
	entered, release = seen, wait
	mu.Unlock()
	done := make(chan error, 1)
	go func() { _, err := a.connectedApps.Connect(ctx, "example", "work", ""); done <- err }()
	<-seen
	current, _ := activateAccountIntent(t, ctx, host, graph.Revision, "1.2.0")
	releaseSource()
	if err := <-done; err != nil {
		t.Fatal("inflight pre-service caller was reselected", err)
	}
	check("ac_new", "200")
	reset()
	if _, err := a.connectedApps.Connect(ctx, "example", "work", ""); err != nil {
		t.Fatal(err)
	}
	check("ac_old", "100")
	// A later deliberate switch to the exact old graph cannot undo durable auth
	// opt-in. New actions fail closed instead of using its historical interpreter.
	if err := host.Propose(ctx, rsi.Candidate{ID: "old-after-opt-in", Baseline: current, Target: graph.Revision, PolicyID: policy.ID, Goal: "Verify durable new-method boundary"}); err != nil {
		t.Fatal(err)
	}
	decision, err = host.Verify(ctx, "old-after-opt-in")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := host.Activate(ctx, rsi.Activation{OperationID: "old-after-opt-in", Expected: current, Target: graph.Revision, DecisionID: decision}); err != nil {
		t.Fatal(err)
	}
	reopen()
	reset()
	if _, err := a.connectedApps.Connect(ctx, "example", "work", ""); !errors.Is(err, connectedapps.ErrUnavailable) {
		t.Fatal("old graph undid auth opt-in", err)
	}
	mu.Lock()
	defer mu.Unlock()
	if posts != 0 || len(limits) != 0 || strings.Contains(chosen, "ac_") {
		t.Fatal("missing method reached native auth source")
	}
}

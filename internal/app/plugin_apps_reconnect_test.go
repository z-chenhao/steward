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
	"github.com/z-chenhao/assistant/internal/rsi"
	rsistore "github.com/z-chenhao/assistant/internal/rsi/sqlite"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type reconnectSourceRequest struct {
	Method, URI string
	Body        any
}
type reconnectSourceCase struct {
	Name, Previous, Selection                                                                   string
	Missing, NoKey, WrongIdentity, NoAuth, ChangedFail, DuplicateAlias                          bool
	AccountStatus, RevokeStatus, ToolkitStatus, AuthStatus, SessionStatus, LinkStatus, FailSave int
	Expected                                                                                    struct {
		Error                       string
		Connections                 []connectedapps.Connection
		Link                        composio.Link
		TTLSeconds                  int
		RevisionDelta               int64
		Saves, Changed              int
		Requests                    []reconnectSourceRequest
		ContinueError, SecondError  string
		SecondRequests, SecondSaves int
		OriginalBindingHash         string
	}
}

func reconnectSourceError(err error) string {
	switch {
	case err == nil:
		return ""
	case errors.Is(err, connectedapps.ErrDenied):
		return "denied"
	case errors.Is(err, connectedapps.ErrUnavailable):
		return "unavailable"
	case errors.Is(err, connectedapps.ErrConflict):
		return "conflict"
	case errors.Is(err, connectedapps.ErrLinkExpired):
		return "expired"
	}
	return "callback"
}
func TestAppsReconnectActualOriginalEffectsVersionColdAndFences(t *testing.T) {
	ctx := context.Background()
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
	buildTestBinary(t, "../../cmd/steward", carrier)
	host := a.Plugins()
	host.Catalog.Launcher = carrier
	first, _ := activateAccountIntent(t, ctx, host, "", "1.0.0")
	var mu sync.Mutex
	var active reconnectSourceCase
	requests := []reconnectSourceRequest{}
	var entered, release chan struct{}
	peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body any
		if r.Body != nil && r.ContentLength != 0 {
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Error(err)
			}
		}
		mu.Lock()
		f := active
		requests = append(requests, reconnectSourceRequest{r.Method, r.URL.RequestURI(), body})
		seen, proceed := entered, release
		if r.Method == "GET" && r.URL.Path == "/connected_accounts/account_original" {
			entered, release = nil, nil
		}
		mu.Unlock()
		if r.Method == "GET" && r.URL.Path == "/connected_accounts/account_original" && seen != nil {
			close(seen)
			<-proceed
		}
		if r.Header.Get("x-api-key") != "synthetic-reconnect-key" {
			t.Error("credential changed")
		}
		if r.URL.Path == "/connected_accounts/account_original" && r.Method == "GET" {
			if f.AccountStatus != 0 {
				w.WriteHeader(f.AccountStatus)
				return
			}
			id := "account_original"
			if f.WrongIdentity {
				id = "foreign"
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"id": id, "user_id": "owner", "toolkit": map[string]string{"slug": "example"}})
			return
		}
		if r.URL.Path == "/connected_accounts/account_original/revoke" && r.Method == "POST" {
			if f.RevokeStatus != 0 {
				w.WriteHeader(f.RevokeStatus)
				return
			}
			_, _ = w.Write([]byte(`{"connected_account":{"id":"account_original","status":"REVOKED"}}`))
			return
		}
		if r.URL.Path == "/connected_accounts/account_original" && r.Method == "DELETE" {
			w.WriteHeader(204)
			return
		}
		switch r.URL.Path {
		case "/toolkits/example":
			if f.ToolkitStatus != 0 {
				w.WriteHeader(f.ToolkitStatus)
				return
			}
			_ = json.NewEncoder(w).Encode(composio.Toolkit{Slug: "example", Name: "Original", NoAuth: f.NoAuth})
		case "/auth_configs/ac_original":
			if f.AuthStatus != 0 {
				w.WriteHeader(f.AuthStatus)
				return
			}
			_, _ = w.Write([]byte(`{"id":"ac_original","toolkit":{"slug":"example"},"status":"ENABLED","is_composio_managed":false,"is_enabled_for_tool_router":true}`))
		case "/tool_router/session":
			if f.SessionStatus != 0 {
				w.WriteHeader(f.SessionStatus)
				return
			}
			_, _ = w.Write([]byte(`{"session_id":"session_original"}`))
		case "/tool_router/session/session_original/link":
			if f.LinkStatus != 0 {
				w.WriteHeader(f.LinkStatus)
				return
			}
			_, _ = w.Write([]byte(`{"redirect_url":"https://connect.composio.dev/link/original","connected_account_id":"account_new"}`))
		default:
			t.Error("unexpected source", r.Method, r.URL)
			http.NotFound(w, r)
		}
	}))
	defer peer.Close()
	store := &accountCloseStore{Store: a.store}
	a.connectedApps.Store = store
	a.connectedApps.Client = composio.Client{Endpoint: peer.URL}
	changed := 0
	a.connectedApps.Changed = func(context.Context) error {
		changed++
		mu.Lock()
		fail := active.ChangedFail
		mu.Unlock()
		if fail {
			return errors.New("private callback")
		}
		return nil
	}
	a.connectedApps.ReadAccountPolicies = func(c context.Context) (connectedapps.AccountPolicies, error) {
		mu.Lock()
		selection := active.Selection
		mu.Unlock()
		if selection == "error" {
			return connectedapps.AccountPolicies{}, connectedapps.ErrUnavailable
		}
		if selection == "intent" {
			return connectedapps.AccountPolicies{}, nil
		}
		p, err := a.appsAccountPoliciesWithHost(c, host)
		if selection == "session" {
			p = p.WithSessionFlow(connectedapps.SessionFlowPolicy{})
		}
		if selection == "close" {
			p = p.WithAccountClose(connectedapps.AccountClosePolicy{})
		}
		return p, err
	}
	raw, err := os.ReadFile("../../tests/fixtures/plugins/apps-reconnect-native-v1.json")
	if err != nil {
		t.Fatal(err)
	}
	var fixtures []reconnectSourceCase
	if err := json.Unmarshal(raw, &fixtures); err != nil {
		t.Fatal(err)
	}
	setup := func(f reconnectSourceCase) connectedapps.State {
		t.Helper()
		store.afterSave = nil
		mu.Lock()
		active = f
		requests = []reconnectSourceRequest{}
		mu.Unlock()
		changed = 0
		store.saves = 0
		store.failAt = f.FailSave
		state, err := a.store.LoadApps(ctx)
		if err != nil {
			t.Fatal(err)
		}
		state.KeyRef = "credential:fixture"
		if f.NoKey {
			state.KeyRef = ""
		}
		state.UserID = "owner"
		state.AuthConfigs = map[string]string{"example": "ac_original"}
		state.Connections = []connectedapps.Connection{{ID: "connection_original", Toolkit: "example", Name: "Original", Alias: "work", AccountID: "account_original", Status: f.Previous}}
		if f.Missing {
			state.Connections = nil
		}
		if f.DuplicateAlias {
			state.Connections = append(state.Connections, connectedapps.Connection{ID: "other", Toolkit: "example", Alias: "work", AccountID: "other_account", Status: "active"})
		}
		if err := a.store.SaveApps(ctx, state, state.Revision); err != nil {
			t.Fatal(err)
		}
		before, err := a.store.LoadApps(ctx)
		if err != nil {
			t.Fatal(err)
		}
		return before
	}
	trace := func() []reconnectSourceRequest {
		mu.Lock()
		defer mu.Unlock()
		return append([]reconnectSourceRequest{}, requests...)
	}
	for i := range fixtures {
		f := &fixtures[i]
		t.Run(f.Name, func(t *testing.T) {
			original := f.Expected
			f.Expected = struct {
				Error                       string
				Connections                 []connectedapps.Connection
				Link                        composio.Link
				TTLSeconds                  int
				RevisionDelta               int64
				Saves, Changed              int
				Requests                    []reconnectSourceRequest
				ContinueError, SecondError  string
				SecondRequests, SecondSaves int
				OriginalBindingHash         string
			}{}

			before := setup(*f)
			link, reconnectErr := a.connectedApps.Reconnect(ctx, "connection_original")
			after, err := a.store.LoadApps(ctx)
			if err != nil {
				t.Fatal(err)
			}
			f.Expected.Error = reconnectSourceError(reconnectErr)
			f.Expected.Connections = append([]connectedapps.Connection{}, after.Connections...)
			f.Expected.Link = link
			f.Expected.RevisionDelta = after.Revision - before.Revision
			f.Expected.Saves = store.saves
			f.Expected.Changed = changed
			mu.Lock()
			f.Expected.Requests = append([]reconnectSourceRequest{}, requests...)
			mu.Unlock()
			for i := range f.Expected.Connections {
				if f.Expected.Connections[i].ID != "connection_original" && f.Expected.Connections[i].ID != "other" {
					f.Expected.Connections[i].ID = "generated"
				}
			}
			if link.ConnectionID != "" {
				f.Expected.Link.ConnectionID = "generated"
				f.Expected.TTLSeconds = int(time.Until(link.ExpiresAt).Round(time.Second) / time.Second)
				f.Expected.Link.ExpiresAt = time.Time{}
			}
			if len(after.Connections) != 0 {
				f.Expected.OriginalBindingHash = connectedapps.BindingHash(after, after.Connections[0])
			}
			_, resumeErr := a.connectedApps.Continue(ctx, link.ConnectionID)
			f.Expected.ContinueError = reconnectSourceError(resumeErr)
			oldClosed := len(after.Connections) > 0 && after.Connections[0].Status == "disconnected"
			if oldClosed {
				mu.Lock()
				n := len(requests)
				mu.Unlock()
				writes := store.saves
				_, again := a.connectedApps.Reconnect(ctx, "connection_original")
				f.Expected.SecondError = reconnectSourceError(again)
				mu.Lock()
				f.Expected.SecondRequests = len(requests) - n
				mu.Unlock()
				f.Expected.SecondSaves = store.saves - writes
			}
			if !reflect.DeepEqual(f.Expected, original) {
				t.Fatalf("original reconnect outcome changed\ngot=%#v\nwant=%#v", f.Expected, original)
			}
		})
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
		host = a.Plugins()
		host.Catalog.Launcher = carrier
		store = &accountCloseStore{Store: a.store}
		a.connectedApps.Store = store
		a.connectedApps.Client = composio.Client{Endpoint: peer.URL}
		a.connectedApps.ReadAccountPolicies = func(c context.Context) (connectedapps.AccountPolicies, error) {
			return a.appsAccountPoliciesWithHost(c, host)
		}
		a.connectedApps.Changed = func(context.Context) error { changed++; return nil }
	}
	expired := fixtures[1]
	setup(expired)
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
	second, _ := activateAccountIntent(t, ctx, host, first, "1.6.0")
	unblock()
	inflight := <-done
	if inflight.err != nil || inflight.link.ConnectionID == "" || len(trace()) != 7 {
		t.Fatal("inflight expired action reselected pending-only recipe", inflight, trace())
	}
	setup(expired)
	if _, err := a.connectedApps.Reconnect(ctx, "connection_original"); !errors.Is(err, connectedapps.ErrDenied) || store.saves != 0 || len(trace()) != 0 {
		t.Fatal("new conservative recipe ignored", err, store.saves, trace())
	}
	reopen()
	setup(expired)
	if _, err := a.connectedApps.Reconnect(ctx, "connection_original"); !errors.Is(err, connectedapps.ErrDenied) || store.saves != 0 || len(trace()) != 0 {
		t.Fatal("cold pending-only recipe lost", err, trace())
	}
	pending := fixtures[0]
	setup(pending)
	link, err := a.connectedApps.Reconnect(ctx, "connection_original")
	if err != nil || link.ConnectionID == "" {
		t.Fatal("pending new version denied", link, err)
	}
	if err := host.Propose(ctx, rsi.Candidate{ID: "reconnect-rollback", Baseline: second, Target: first, PolicyID: "accounts-owner-1.0.0", Goal: "Restore complete original reconnect"}); err != nil {
		t.Fatal(err)
	}
	decision, err := host.Verify(ctx, "reconnect-rollback")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := host.Activate(ctx, rsi.Activation{OperationID: "reconnect-rollback", Expected: second, Target: first, DecisionID: decision}); err != nil {
		t.Fatal(err)
	}
	reopen()
	setup(expired)
	link, err = a.connectedApps.Reconnect(ctx, "connection_original")
	if err != nil || link.ConnectionID == "" || len(trace()) != 7 {
		t.Fatal("cold original recipe rollback failed", link, err, trace())
	}
	// A changed authority between old closure and new creation must not be adopted
	// into the original logical input. The already closed original remains closed.
	setup(pending)
	store.afterSave = func(n int) {
		if n == 2 {
			v, err := a.store.LoadApps(ctx)
			if err != nil {
				t.Error(err)
				return
			}
			v.UserID = "foreign_owner"
			if err := a.store.SaveApps(ctx, v, v.Revision); err != nil {
				t.Error(err)
			}
		}
	}
	if _, err := a.connectedApps.Reconnect(ctx, "connection_original"); !errors.Is(err, connectedapps.ErrConflict) || len(trace()) != 3 {
		t.Fatal("changed original authority reached new connection", err, trace())
	}
	// Previous private fences are retained at the post-close decision. They cannot
	// be bypassed by rebuilding the policy selector or choosing an active graph.
	setup(pending)
	var deny atomic.Bool
	privateHost := host
	base := *host.Store.(*rsistore.Store)
	previous := base.Fence
	base.Fence = func(c context.Context, tx *sql.Tx) error {
		if previous != nil {
			if err := previous(c, tx); err != nil {
				return err
			}
		}
		if deny.Load() {
			return rsi.ErrRevoked
		}
		return nil
	}
	privateHost.Store = &base
	a.connectedApps.ReadAccountPolicies = func(c context.Context) (connectedapps.AccountPolicies, error) {
		return a.appsAccountPoliciesWithHost(c, privateHost)
	}
	store.afterSave = func(n int) {
		if n == 2 {
			deny.Store(true)
		}
	}
	if _, err := a.connectedApps.Reconnect(ctx, "connection_original"); !errors.Is(err, connectedapps.ErrUnavailable) || len(trace()) != 3 || store.saves != 2 {
		t.Fatal("post-close private fence bypassed", err, trace(), store.saves)
	}
	a.connectedApps.ReadAccountPolicies = func(c context.Context) (connectedapps.AccountPolicies, error) {
		return a.appsAccountPoliciesWithHost(c, host)
	}
	baseline := first
	for i, version := range []string{"6.0.0", "6.1.0", "6.2.0", "6.3.0"} {
		graph, _ := activateAccountIntent(t, ctx, host, baseline, version)
		baseline = graph
		before := setup(pending)
		_, err := a.connectedApps.Reconnect(ctx, "connection_original")
		wantErr := connectedapps.ErrUnavailable
		if i == 3 {
			wantErr = connectedapps.ErrDenied
		}
		state, loadErr := a.store.LoadApps(ctx)
		wantWrites, wantRequests := 2, 3
		wantStatus := "disconnected"
		if i == 0 {
			wantWrites, wantRequests = 0, 0
			wantStatus = "pending"
		}
		if !errors.Is(err, wantErr) || loadErr != nil || len(state.Connections) != 1 || state.Connections[0].Status != wantStatus || store.saves != wantWrites || len(trace()) != wantRequests || connectedapps.BindingHash(before, before.Connections[0]) != connectedapps.BindingHash(state, state.Connections[0]) {
			t.Fatal("forged reconnect order/alias escaped scope", version, state, err, trace(), store.saves)
		}
		if i != 0 {
			n := len(trace())
			if _, err := a.connectedApps.Reconnect(ctx, "connection_original"); !errors.Is(err, connectedapps.ErrDenied) || len(trace()) != n {
				t.Fatal("partial reconnect replayed after closed old binding", err, trace())
			}
		}
	}
	if err := host.Propose(ctx, rsi.Candidate{ID: "reconnect-restore", Baseline: baseline, Target: first, PolicyID: "accounts-owner-1.0.0", Goal: "Restore original to check accepted result"}); err != nil {
		t.Fatal(err)
	}
	decision, err = host.Verify(ctx, "reconnect-restore")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := host.Activate(ctx, rsi.Activation{OperationID: "reconnect-restore", Expected: baseline, Target: first, DecisionID: decision}); err != nil {
		t.Fatal(err)
	}
	setup(pending)
	store.afterSave = func(n int) {
		if n == 3 {
			if err := host.Store.Quarantine(ctx, first); err != nil {
				t.Error(err)
			}
		}
	}
	accepted, err := a.connectedApps.Reconnect(ctx, "connection_original")
	if err != nil || accepted.ConnectionID == "" || store.saves != 3 || len(trace()) != 7 {
		t.Fatal("accepted new link hidden by later candidate round", accepted, err, trace())
	}
	if cached, err := a.connectedApps.Continue(ctx, accepted.ConnectionID); err != nil || cached != accepted {
		t.Fatal("accepted cache identity lost after graph quarantine", cached, err)
	}
	setup(pending)
	if _, err := a.connectedApps.Reconnect(ctx, "connection_original"); !errors.Is(err, connectedapps.ErrUnavailable) || store.saves != 0 || len(trace()) != 0 {
		t.Fatal("quarantined reconnect restored native or closed old binding", err, trace())
	}
	if _, err := host.Store.Deactivate(ctx, "reconnect-disable", first); err != nil {
		t.Fatal(err)
	}
	setup(pending)
	reopen()
	if _, err := a.connectedApps.Reconnect(ctx, "connection_original"); !errors.Is(err, connectedapps.ErrUnavailable) || store.saves != 0 || len(trace()) != 0 {
		t.Fatal("cold disabled reconnect restored native", err, trace())
	}
}

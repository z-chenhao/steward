package app

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/z-chenhao/assistant/internal/config"
	"github.com/z-chenhao/assistant/internal/connectedapps"
	"github.com/z-chenhao/assistant/internal/connector/composio"
	"github.com/z-chenhao/assistant/internal/llm/memory"
	"github.com/z-chenhao/assistant/internal/rsi"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"sync"
	"testing"
)

type accountCloseFixture struct {
	Name                                                                                                    string
	Public, Disabled, WrongID, WrongUser, WrongToolkit, InvalidAccount, AlreadyClosed, Missing, ChangedFail bool
	AccountStatus, RevokeStatus, DeleteStatus, FailSave                                                     int
	RevokeBody                                                                                              string
	Expected                                                                                                struct {
		Error, Status               string
		Confirmed, Cached           bool
		Revision                    int64
		Saves, Changed              int
		Requests                    []string
		SecondError                 string
		SecondRequests, SecondSaves int
	}
}
type accountCloseStore struct {
	connectedapps.Store
	saves, failAt int
	afterSave     func(int)
}

func (s *accountCloseStore) SaveApps(ctx context.Context, state connectedapps.State, revision int64) error {
	s.saves++
	if s.saves == s.failAt {
		return connectedapps.ErrConflict
	}
	err := s.Store.SaveApps(ctx, state, revision)
	if err == nil && s.afterSave != nil {
		s.afterSave(s.saves)
	}
	return err
}
func accountCloseError(err error) string {
	switch {
	case err == nil:
		return ""
	case errors.Is(err, connectedapps.ErrConflict):
		return "conflict"
	case errors.Is(err, connectedapps.ErrInvalid):
		return "invalid"
	}
	return "callback"
}
func TestAppsAccountCloseActualOriginalEffectsVersionColdAndFences(t *testing.T) {
	ctx := context.Background()
	raw, err := os.ReadFile("../../tests/fixtures/plugins/apps-account-close-native-v1.json")
	if err != nil {
		t.Fatal(err)
	}
	var fixtures []accountCloseFixture
	if err := json.Unmarshal(raw, &fixtures); err != nil {
		t.Fatal(err)
	}
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
	buildTestBinary(t, "../../cmd/steward", carrier)
	var mu sync.Mutex
	var active accountCloseFixture
	requests := []string{}
	var entered, release chan struct{}
	var afterAccount, afterRevoke func()
	peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		f := active
		requests = append(requests, r.Method+" "+r.URL.RequestURI())
		seen, proceed := entered, release
		accountAction, revokeAction := afterAccount, afterRevoke
		if r.Method == "GET" && r.URL.Path == "/connected_accounts/account_original" {
			entered, release = nil, nil
			afterAccount = nil
		}
		if r.Method == "POST" && r.URL.Path == "/connected_accounts/account_original/revoke" {
			afterRevoke = nil
		}
		mu.Unlock()
		if r.Header.Get("x-api-key") != "synthetic-close-key" {
			t.Error("source credential changed")
		}
		if r.URL.Path == "/connected_accounts/account_original" && r.Method == "GET" {
			if seen != nil {
				close(seen)
				<-proceed
			}
			if accountAction != nil {
				accountAction()
			}
			if f.AccountStatus != 0 {
				w.WriteHeader(f.AccountStatus)
				return
			}
			id, user, toolkit := "account_original", "owner", "example"
			if f.WrongID {
				id = "foreign"
			}
			if f.WrongUser {
				user = "foreign"
			}
			if f.WrongToolkit {
				toolkit = "foreign"
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"id": id, "user_id": user, "status": "ACTIVE", "is_disabled": f.Disabled, "toolkit": map[string]string{"slug": toolkit}, "data": map[string]string{"access_token": "excluded-private"}})
			return
		}
		if r.URL.Path == "/connected_accounts/account_original/revoke" && r.Method == "POST" {
			if revokeAction != nil {
				revokeAction()
			}
			if f.RevokeStatus != 0 {
				w.WriteHeader(f.RevokeStatus)
				return
			}
			body := f.RevokeBody
			if body == "" {
				body = `{"connected_account":{"id":"account_original","status":"REVOKED"}}`
			}
			_, _ = w.Write([]byte(body))
			return
		}
		if r.URL.Path == "/connected_accounts/account_original" && r.Method == "DELETE" {
			if f.DeleteStatus != 0 {
				w.WriteHeader(f.DeleteStatus)
			} else {
				w.WriteHeader(204)
			}
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
	var store *accountCloseStore
	changed := 0
	bind := func() {
		host = a.Plugins()
		host.Catalog.Launcher = carrier
		store = &accountCloseStore{Store: a.store}
		a.connectedApps.Store = store
		a.connectedApps.Client = composio.Client{Endpoint: peer.URL}
		a.connectedApps.ReadAccountPolicies = func(c context.Context) (connectedapps.AccountPolicies, error) {
			return a.appsAccountPoliciesWithHost(c, host)
		}
		a.connectedApps.ReadAccountClose = func(c context.Context) (connectedapps.AccountClosePolicy, error) {
			return a.appsAccountCloseWithHost(c, host)
		}
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
	}
	bind()
	setup := func(f accountCloseFixture) connectedapps.State {
		t.Helper()
		mu.Lock()
		active = f
		requests = []string{}
		afterAccount, afterRevoke = nil, nil
		mu.Unlock()
		changed = 0
		store.saves = 0
		store.failAt = f.FailSave
		state, err := a.store.LoadApps(ctx)
		if err != nil {
			t.Fatal(err)
		}
		state.KeyRef = "credential:fixture"
		state.UserID = "owner"
		state.AuthConfigs = map[string]string{"example": "ac_original"}
		c := connectedapps.Connection{ID: "connection_original", Toolkit: "example", Alias: "work", AccountID: "account_original", Status: "active", Public: f.Public}
		if f.InvalidAccount {
			c.AccountID = "bad/id"
		}
		if f.AlreadyClosed {
			c.Status = "disconnected"
			c.RevocationConfirmed = true
		}
		state.Connections = []connectedapps.Connection{c}
		if f.Missing {
			state.Connections = nil
		}
		if err := a.store.SaveApps(ctx, state, state.Revision); err != nil {
			t.Fatal(err)
		}
		state, err = a.store.LoadApps(ctx)
		if err != nil {
			t.Fatal(err)
		}
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
	first, _ := activateAccountIntent(t, ctx, host, "", "1.0.0")
	for _, f := range fixtures {
		t.Run(f.Name, func(t *testing.T) {
			before := setup(f)
			err := a.connectedApps.Disconnect(ctx, "connection_original")
			state, loadErr := a.store.LoadApps(ctx)
			if loadErr != nil {
				t.Fatal(loadErr)
			}
			status := ""
			confirmed := false
			if len(state.Connections) != 0 {
				status = state.Connections[0].Status
				confirmed = state.Connections[0].RevocationConfirmed
				if connectedapps.BindingHash(before, before.Connections[0]) != connectedapps.BindingHash(state, state.Connections[0]) {
					t.Fatal("original binding identity changed")
				}
			}
			if accountCloseError(err) != f.Expected.Error || status != f.Expected.Status || confirmed != f.Expected.Confirmed || state.Revision-before.Revision != f.Expected.Revision-7 || store.saves != f.Expected.Saves || changed != f.Expected.Changed || !reflect.DeepEqual(trace(), f.Expected.Requests) {
				t.Fatal("original owner closure changed", accountCloseError(err), status, confirmed, state.Revision-before.Revision, store.saves, changed, trace(), f.Expected)
			}
			if status == "disconnected" {
				n, writes := len(trace()), store.saves
				again := a.connectedApps.Disconnect(ctx, "connection_original")
				if accountCloseError(again) != f.Expected.SecondError || len(trace())-n != f.Expected.SecondRequests || store.saves-writes != f.Expected.SecondSaves {
					t.Fatal("explicit second click replayed old unknown effect", again, trace())
				}
			}
		})
	}
	good := fixtures[0]
	setup(good)
	seen, proceed := make(chan struct{}), make(chan struct{})
	unblock := sync.OnceFunc(func() { close(proceed) })
	defer unblock()
	mu.Lock()
	entered, release = seen, proceed
	mu.Unlock()
	done := make(chan error, 1)
	go func() { done <- a.connectedApps.Disconnect(ctx, "connection_original") }()
	<-seen
	second, _ := activateAccountIntent(t, ctx, host, first, "1.5.0")
	unblock()
	if err := <-done; err != nil || len(trace()) != 3 {
		t.Fatal("inflight cleanup reselected graph", err, trace())
	}
	setup(good)
	if err := a.connectedApps.Disconnect(ctx, "connection_original"); err != nil || len(trace()) != 2 {
		t.Fatal("new proved cleanup did not independently retain broker record", err, trace())
	}
	state, err := a.store.LoadApps(ctx)
	if err != nil || !state.Connections[0].RevocationConfirmed {
		t.Fatal("original revoke proof lost", state, err)
	}
	binding := connectedapps.BindingHash(state, state.Connections[0])
	reopen()
	if err := a.connectedApps.Disconnect(ctx, "connection_original"); err != nil {
		t.Fatal(err)
	}
	cold, err := a.store.LoadApps(ctx)
	if err != nil || connectedapps.BindingHash(cold, cold.Connections[0]) != binding {
		t.Fatal("cold original binding lost", cold, err)
	}
	setup(good)
	if err := a.connectedApps.Disconnect(ctx, "connection_original"); err != nil || len(trace()) != 2 {
		t.Fatal("cold cleanup version lost", err, trace())
	}
	uncertain := good
	uncertain.RevokeStatus = 502
	setup(uncertain)
	if err := a.connectedApps.Disconnect(ctx, "connection_original"); err != nil || len(trace()) != 3 {
		t.Fatal("uncertain revoke was retried or promoted", err, trace())
	}
	state, err = a.store.LoadApps(ctx)
	if err != nil || state.Connections[0].RevocationConfirmed {
		t.Fatal("DELETE invented revoke proof", state, err)
	}
	if err := host.Propose(ctx, rsi.Candidate{ID: "close-rollback", Baseline: second, Target: first, PolicyID: "accounts-owner-1.0.0", Goal: "Restore original remote cleanup"}); err != nil {
		t.Fatal(err)
	}
	decision, err := host.Verify(ctx, "close-rollback")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := host.Activate(ctx, rsi.Activation{OperationID: "close-rollback", Expected: second, Target: first, DecisionID: decision}); err != nil {
		t.Fatal(err)
	}
	reopen()
	setup(good)
	if err := a.connectedApps.Disconnect(ctx, "connection_original"); err != nil || len(trace()) != 3 {
		t.Fatal("cold rollback lost original cleanup", err, trace())
	}
	// The state fence is checked before candidate launch after original source IO.
	setup(good)
	mu.Lock()
	afterAccount = func() {
		v, err := a.store.LoadApps(ctx)
		if err != nil {
			t.Error(err)
			return
		}
		if err := a.store.SaveApps(ctx, v, v.Revision); err != nil {
			t.Error(err)
		}
	}
	mu.Unlock()
	if err := a.connectedApps.Disconnect(ctx, "connection_original"); !errors.Is(err, connectedapps.ErrConflict) || len(trace()) != 1 {
		t.Fatal("stale source proceeded to revoke", err, trace())
	}

	// A source proof cannot be saved into a concurrently changed owner revision.
	setup(good)
	mu.Lock()
	afterRevoke = func() {
		v, err := a.store.LoadApps(ctx)
		if err != nil {
			t.Error(err)
			return
		}
		if err := a.store.SaveApps(ctx, v, v.Revision); err != nil {
			t.Error(err)
		}
	}
	mu.Unlock()
	if err := a.connectedApps.Disconnect(ctx, "connection_original"); !errors.Is(err, connectedapps.ErrConflict) || len(trace()) != 2 {
		t.Fatal("post-revoke stale owner scope wrote proof or sent DELETE", err, trace())
	}
	state, err = a.store.LoadApps(ctx)
	if err != nil || state.Connections[0].Status != "disconnected" || state.Connections[0].RevocationConfirmed {
		t.Fatal("stale proof published into new revision", state, err)
	}
	beforeRetry := len(trace())
	if err := a.connectedApps.Disconnect(ctx, "connection_original"); err != nil || len(trace()) != beforeRetry {
		t.Fatal("unknown final receipt was replayed", err, trace())
	}
	baseline := first
	for i, version := range []string{"5.0.0", "5.1.0", "5.2.0", "5.3.0", "5.4.0"} {
		graph, _ := activateAccountIntent(t, ctx, host, baseline, version)
		baseline = graph
		f := good
		if i == 1 {
			f.WrongID = true
		}
		setup(f)
		if err := a.connectedApps.Disconnect(ctx, "connection_original"); err != nil {
			t.Fatal(err)
		}
		state, err := a.store.LoadApps(ctx)
		wantCalls := []int{0, 1, 2, 1, 3}[i]
		wantProof := i == 2 || i == 4
		if err != nil || state.Connections[0].Status != "disconnected" || state.Connections[0].RevocationConfirmed != wantProof || len(trace()) != wantCalls || store.saves != 2 || changed != 1 {
			t.Fatal("forged cleanup order/identity/proof or owner veto", version, state, trace(), store.saves, changed, err)
		}
	}
	if err := host.Propose(ctx, rsi.Candidate{ID: "close-restore", Baseline: baseline, Target: first, PolicyID: "accounts-owner-1.0.0", Goal: "Restore original for post-effect fences"}); err != nil {
		t.Fatal(err)
	}
	decision, err = host.Verify(ctx, "close-restore")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := host.Activate(ctx, rsi.Activation{OperationID: "close-restore", Expected: baseline, Target: first, DecisionID: decision}); err != nil {
		t.Fatal(err)
	}
	// Revocation proof is an owner source fact even if the candidate is lost after
	// the POST. Recording it still requires the original owner CAS/privacy scope.
	setup(good)
	mu.Lock()
	afterRevoke = func() {
		if err := host.Store.Quarantine(ctx, first); err != nil {
			t.Error(err)
		}
	}
	mu.Unlock()
	if err := a.connectedApps.Disconnect(ctx, "connection_original"); err != nil {
		t.Fatal(err)
	}
	state, err = a.store.LoadApps(ctx)
	if err != nil || !state.Connections[0].RevocationConfirmed || len(trace()) != 2 {
		t.Fatal("post-revoke candidate loss erased proof or replayed DELETE", state, trace(), err)
	}
	setup(good)
	if err := a.connectedApps.Disconnect(ctx, "connection_original"); err != nil {
		t.Fatal("candidate blocked independent local close", err)
	}
	state, err = a.store.LoadApps(ctx)
	if err != nil || state.Connections[0].Status != "disconnected" || state.Connections[0].RevocationConfirmed || len(trace()) != 0 {
		t.Fatal("quarantine used native cleanup or vetoed local closure", state, trace(), err)
	}
	if _, err := host.Store.Deactivate(ctx, "close-disable", first); err != nil {
		t.Fatal(err)
	}
	setup(good)
	reopen()
	if err := a.connectedApps.Disconnect(ctx, "connection_original"); err != nil {
		t.Fatal("cold disabled candidate blocked owner", err)
	}
	state, err = a.store.LoadApps(ctx)
	if err != nil || state.Connections[0].Status != "disconnected" || state.Connections[0].RevocationConfirmed || len(trace()) != 0 {
		t.Fatal("cold disabled cleanup restored native", state, trace(), err)
	}
}

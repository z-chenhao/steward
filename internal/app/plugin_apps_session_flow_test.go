package app

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/z-chenhao/assistant/internal/config"
	"github.com/z-chenhao/assistant/internal/connectedapps"
	"github.com/z-chenhao/assistant/internal/connector/composio"
	"github.com/z-chenhao/assistant/internal/llm/memory"
	"github.com/z-chenhao/assistant/internal/rsi"
)

type sessionSourceRequest struct {
	Method, Path, Query string
	Body                any
}
type sessionSourceExpected struct {
	Requests                   []sessionSourceRequest
	Error, ContinueError       string
	Connections                []connectedapps.Connection
	Link                       composio.Link
	TTLSeconds, Saves, Changed int
}
type sessionSourceCase struct {
	Name, Toolkit, Session, Link string
	SessionStatus, LinkStatus    int
	SaveFail, ChangedFail        bool
	Expected                     sessionSourceExpected
}
type sessionFlowStore struct {
	connectedapps.Store
	fail  bool
	saves int
}

func (s *sessionFlowStore) SaveApps(c context.Context, state connectedapps.State, revision int64) error {
	s.saves++
	if s.fail {
		return connectedapps.ErrConflict
	}
	return s.Store.SaveApps(c, state, revision)
}
func sessionFlowError(err error) string {
	switch {
	case err == nil:
		return ""
	case errors.Is(err, connectedapps.ErrUnavailable):
		return "unavailable"
	case errors.Is(err, connectedapps.ErrConflict):
		return "conflict"
	case errors.Is(err, connectedapps.ErrDenied):
		return "denied"
	case errors.Is(err, connectedapps.ErrLinkExpired):
		return "expired"
	}
	return "callback"
}
func TestAppsSessionFlowActualOriginalEffectsVersionColdAndFences(t *testing.T) {
	ctx := context.Background()
	raw, err := os.ReadFile("../../tests/fixtures/plugins/apps-session-flow-native-v1.json")
	if err != nil {
		t.Fatal(err)
	}
	var fixtures []sessionSourceCase
	if err := json.Unmarshal(raw, &fixtures); err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	cfg, err := config.Load("", func(key string) (string, bool) { return root, key == "STEWARD_DATA_ROOT" })
	if err != nil {
		t.Fatal(err)
	}
	secrets := memory.New()
	if err := secrets.Put(ctx, "credential:fixture", []byte("synthetic-session-key")); err != nil {
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
	var active sessionSourceCase
	var requests []sessionSourceRequest
	var entered, release chan struct{}
	var afterSession func()
	peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body any
		if r.Body != nil && r.ContentLength != 0 {
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Error(err)
			}
		}
		mu.Lock()
		f := active
		requests = append(requests, sessionSourceRequest{r.Method, r.URL.Path, r.URL.RawQuery, body})
		seen, proceed := entered, release
		action := afterSession
		if r.URL.Path == "/tool_router/session" {
			entered, release = nil, nil
			afterSession = nil
		}
		mu.Unlock()
		if r.Header.Get("x-api-key") != "synthetic-session-key" {
			t.Error("credential authority changed")
		}
		switch r.URL.Path {
		case "/toolkits/example":
			if f.Toolkit != "" {
				_, _ = w.Write([]byte(f.Toolkit))
			} else {
				_, _ = w.Write([]byte(`{"slug":"example","name":"Original Name"}`))
			}
		case "/auth_configs/ac_original":
			_, _ = w.Write([]byte(`{"id":"ac_original","toolkit":{"slug":"example"},"status":"ENABLED","is_composio_managed":false,"is_enabled_for_tool_router":true}`))
		case "/tool_router/session":
			if seen != nil {
				close(seen)
				<-proceed
			}
			if action != nil {
				action()
			}
			if f.SessionStatus != 0 {
				w.WriteHeader(f.SessionStatus)
			}
			_, _ = w.Write([]byte(f.Session))
		case "/tool_router/session/session_original/link":
			if f.LinkStatus != 0 {
				w.WriteHeader(f.LinkStatus)
			}
			_, _ = w.Write([]byte(f.Link))
		default:
			t.Error("unexpected source request", r.URL)
			http.NotFound(w, r)
		}
	}))
	defer peer.Close()
	var host rsi.Host
	var store *sessionFlowStore
	changed := 0
	bind := func() {
		host = a.Plugins()
		host.Catalog.Launcher = carrier
		store = &sessionFlowStore{Store: a.store}
		a.connectedApps.Store = store
		a.connectedApps.Client = composio.Client{Endpoint: peer.URL}
		a.connectedApps.ReadAccountPolicies = func(c context.Context) (connectedapps.AccountPolicies, error) {
			return a.appsAccountPoliciesWithHost(c, host)
		}
		a.connectedApps.Changed = func(context.Context) error {
			changed++
			mu.Lock()
			fail := active.ChangedFail
			mu.Unlock()
			if fail {
				return errors.New("callback private detail")
			}
			return nil
		}
	}
	bind()
	setup := func(f sessionSourceCase) connectedapps.State {
		t.Helper()
		mu.Lock()
		active = f
		requests = []sessionSourceRequest{}
		mu.Unlock()
		changed = 0
		store.saves = 0
		store.fail = f.SaveFail
		state, err := a.store.LoadApps(ctx)
		if err != nil {
			t.Fatal(err)
		}
		state.KeyRef = "credential:fixture"
		state.UserID = "owner_original"
		state.Connections = nil
		state.AuthConfigs = map[string]string{"example": "ac_original"}
		if err := a.store.SaveApps(ctx, state, state.Revision); err != nil {
			t.Fatal(err)
		}
		state, err = a.store.LoadApps(ctx)
		if err != nil {
			t.Fatal(err)
		}
		return state
	}
	trace := func() []sessionSourceRequest {
		mu.Lock()
		defer mu.Unlock()
		return append([]sessionSourceRequest{}, requests...)
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
	first, _ := activateAccountIntent(t, ctx, host, "", "1.0.0")
	for _, f := range fixtures {
		t.Run(f.Name, func(t *testing.T) {
			before := setup(f)
			link, err := a.connectedApps.Connect(ctx, "example", "work", "")
			state, loadErr := a.store.LoadApps(ctx)
			if loadErr != nil {
				t.Fatal(loadErr)
			}
			out := sessionSourceExpected{Requests: trace(), Error: sessionFlowError(err), Connections: append([]connectedapps.Connection{}, state.Connections...), Link: link, Saves: store.saves, Changed: changed}
			if link.ConnectionID != "" {
				out.TTLSeconds = int(time.Until(link.ExpiresAt).Round(time.Second) / time.Second)
				out.Link.ConnectionID = "generated"
				out.Link.ExpiresAt = time.Time{}
			}
			for i := range out.Connections {
				out.Connections[i].ID = "generated"
			}
			id := link.ConnectionID
			if id == "" && len(state.Connections) > 0 {
				id = state.Connections[0].ID
			}
			resumed, resumeErr := a.connectedApps.Continue(ctx, id)
			out.ContinueError = sessionFlowError(resumeErr)
			if resumeErr == nil && resumed != link {
				t.Fatal("cached accepted original link changed")
			}
			if !reflect.DeepEqual(out, f.Expected) {
				t.Fatalf("original workflow changed\ngot=%#v\nwant=%#v", out, f.Expected)
			}
			if state.Revision != before.Revision+int64(len(state.Connections)) {
				t.Fatal("original CAS shape changed", before.Revision, state.Revision)
			}
		})
	}
	good := fixtures[0]
	setup(good)
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
	second, _ := activateAccountIntent(t, ctx, host, first, "1.3.0")
	releaseSource()
	inflight := <-done
	if inflight.err != nil || int(time.Until(inflight.link.ExpiresAt).Round(time.Second)/time.Second) != 600 {
		t.Fatal("source IO reselected workflow", inflight.err, inflight.link.ExpiresAt)
	}
	setup(good)
	link, err := a.connectedApps.Connect(ctx, "example", "work", "")
	if err != nil || int(time.Until(link.ExpiresAt).Round(time.Second)/time.Second) != 300 {
		t.Fatal("new workflow did not independently change lifetime", err, link.ExpiresAt)
	}
	if len(trace()) != 4 || trace()[3].Body.(map[string]any)["alias"] != "work" {
		t.Fatal("unrelated intent/auth/session shape changed", trace())
	}
	state, err := a.store.LoadApps(ctx)
	if err != nil || len(state.Connections) != 1 {
		t.Fatal(err, state)
	}
	binding := connectedapps.BindingHash(state, state.Connections[0])
	reopen()
	if _, err := a.connectedApps.Continue(ctx, link.ConnectionID); !errors.Is(err, connectedapps.ErrLinkExpired) {
		t.Fatal("cold cache loss retried link", err)
	}
	cold, err := a.store.LoadApps(ctx)
	if err != nil || connectedapps.BindingHash(cold, cold.Connections[0]) != binding {
		t.Fatal("cold reopen changed original binding", err)
	}
	setup(good)
	link, err = a.connectedApps.Connect(ctx, "example", "work", "")
	if err != nil || int(time.Until(link.ExpiresAt).Round(time.Second)/time.Second) != 300 {
		t.Fatal("cold workflow version lost", err)
	}
	if err := host.Propose(ctx, rsi.Candidate{ID: "session-rollback", Baseline: second, Target: first, PolicyID: "accounts-owner-1.0.0", Goal: "Restore original connection workflow"}); err != nil {
		t.Fatal(err)
	}
	decision, err := host.Verify(ctx, "session-rollback")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := host.Activate(ctx, rsi.Activation{OperationID: "session-rollback", Expected: second, Target: first, DecisionID: decision}); err != nil {
		t.Fatal(err)
	}
	reopen()
	setup(good)
	link, err = a.connectedApps.Connect(ctx, "example", "work", "")
	if err != nil || int(time.Until(link.ExpiresAt).Round(time.Second)/time.Second) != 600 {
		t.Fatal("cold rollback lost original workflow", err)
	}
	// Once a session POST has happened, a private-state conflict may not create a
	// link, persist a binding, or replay the source effect.
	before := setup(good)
	mu.Lock()
	afterSession = func() {
		state, err := a.store.LoadApps(ctx)
		if err != nil {
			t.Error(err)
			return
		}
		if err := a.store.SaveApps(ctx, state, state.Revision); err != nil {
			t.Error(err)
		}
	}
	mu.Unlock()
	if _, err := a.connectedApps.Connect(ctx, "example", "work", ""); !errors.Is(err, connectedapps.ErrConflict) {
		t.Fatal("post-effect state conflict accepted", err)
	}
	now, err := a.store.LoadApps(ctx)
	if err != nil || len(now.Connections) != 0 || now.Revision != before.Revision+1 || len(trace()) != 3 {
		t.Fatal("uncertain flow replayed or committed", trace(), now, err)
	}
	baseline := first
	for i, version := range []string{"3.0.0", "3.1.0", "3.2.0", "3.3.0"} {
		graph, _ := activateAccountIntent(t, ctx, host, baseline, version)
		baseline = graph
		setup(good)
		if _, err := a.connectedApps.Connect(ctx, "example", "work", ""); !errors.Is(err, connectedapps.ErrUnavailable) {
			t.Fatal("forged workflow accepted", version, err)
		}
		want := []int{1, 3, 4, 1}[i]
		now, err := a.store.LoadApps(ctx)
		if err != nil || len(now.Connections) != 0 || len(trace()) != want {
			t.Fatal("invalid command escaped authority or replayed", version, trace(), now, err)
		}
	}
	setup(good)
	mu.Lock()
	afterSession = func() {
		if err := host.Store.Quarantine(ctx, baseline); err != nil {
			t.Error(err)
		}
	}
	mu.Unlock()
	// Last malicious version skips auth before reaching a session; restore the
	// original verified graph before testing a real post-session quarantine.
	if err := host.Propose(ctx, rsi.Candidate{ID: "session-restore", Baseline: baseline, Target: first, PolicyID: "accounts-owner-1.0.0", Goal: "Restore original for private fence check"}); err != nil {
		t.Fatal(err)
	}
	decision, err = host.Verify(ctx, "session-restore")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := host.Activate(ctx, rsi.Activation{OperationID: "session-restore", Expected: baseline, Target: first, DecisionID: decision}); err != nil {
		t.Fatal(err)
	}
	baseline = first
	if _, err := a.connectedApps.Connect(ctx, "example", "work", ""); !errors.Is(err, connectedapps.ErrUnavailable) {
		t.Fatal("post-session quarantine accepted", err)
	}
	now, err = a.store.LoadApps(ctx)
	if err != nil || len(now.Connections) != 0 || len(trace()) != 3 {
		t.Fatal("quarantined flow replayed link", trace(), now, err)
	}
	setup(good)
	if _, err := a.connectedApps.Connect(ctx, "example", "work", ""); !errors.Is(err, connectedapps.ErrUnavailable) || len(trace()) != 0 {
		t.Fatal("quarantine restored native flow", trace(), err)
	}
	// Explicit local revocation remains available even when the candidate cannot run.
	state = setup(good)
	state.Connections = []connectedapps.Connection{{ID: "public-owner", Toolkit: "example", Alias: "local", Status: "active", Public: true}}
	if err := a.store.SaveApps(ctx, state, state.Revision); err != nil {
		t.Fatal(err)
	}
	if err := a.connectedApps.Disconnect(ctx, "public-owner"); err != nil {
		t.Fatal("candidate blocked owner revoke", err)
	}
	now, err = a.store.LoadApps(ctx)
	if err != nil || now.Connections[0].Status != "disconnected" || !now.Connections[0].RevocationConfirmed || len(trace()) != 0 {
		t.Fatal("owner local revoke changed", now, trace(), err)
	}
	if _, err := host.Store.Deactivate(ctx, "session-disable", first); err != nil {
		t.Fatal(err)
	}
	reopen()
	setup(good)
	if _, err := a.connectedApps.Connect(ctx, "example", "work", ""); !errors.Is(err, connectedapps.ErrUnavailable) || len(trace()) != 0 {
		t.Fatal("cold disabled workflow restored native", trace(), err)
	}
}

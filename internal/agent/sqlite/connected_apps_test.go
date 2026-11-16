package sqlite

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/z-chenhao/assistant/internal/agent"
	"github.com/z-chenhao/assistant/internal/connectedapps"
	"github.com/z-chenhao/assistant/internal/connector/composio"
	"github.com/z-chenhao/assistant/internal/llm"
	"github.com/z-chenhao/assistant/internal/llm/secret"
	appstools "github.com/z-chenhao/assistant/internal/plugin/connectedapps"
	"github.com/z-chenhao/assistant/internal/tool"
	"github.com/z-chenhao/assistant/internal/tool/codemode"
)

type appSecrets struct{}

func TestConnectedAppsMigrationV45PreservesUIAndRollsBack(t *testing.T) {
	ctx := context.Background()
	db := schemaDB(t)
	if err := migrate(ctx, db, migrations[:45]); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO ui_view_state VALUES('prior-component','{"selected":"kept"}','{}',7,1)`); err != nil {
		t.Fatal(err)
	}
	broken := append(append([]string{}, migrations[:45]...), connectedAppsSQL+`; INVALID SQL`)
	if err := migrate(ctx, db, broken); err == nil {
		t.Fatal("broken connected-app migration succeeded")
	}
	var version, count int
	if err := db.QueryRow(`PRAGMA user_version`).Scan(&version); err != nil || version != 45 {
		t.Fatal("failed migration changed schema version", version, err)
	}
	if err := db.QueryRow(`SELECT count(*) FROM sqlite_schema WHERE name='connected_apps'`).Scan(&count); err != nil || count != 0 {
		t.Fatal("failed migration retained partial app state", count, err)
	}
	if err := migrate(ctx, db, migrations); err != nil {
		t.Fatal(err)
	}
	var saved string
	if err := db.QueryRow(`SELECT state_json FROM ui_view_state WHERE component_ref='prior-component' AND version=7`).Scan(&saved); err != nil || saved != `{"selected":"kept"}` {
		t.Fatal("upgrade lost existing UI state", err)
	}
	store := &Store{db: db}
	state, err := store.LoadApps(ctx)
	if err != nil || !strings.HasPrefix(state.UserID, "steward_") || state.KeyRef != "" || len(state.Connections) != 0 {
		t.Fatal("upgrade invented credentials or account grants", err)
	}
	if err := migrate(ctx, db, migrations); err != nil {
		t.Fatal(err)
	}
	again, err := store.LoadApps(ctx)
	if err != nil || again.UserID != state.UserID || again.Revision != state.Revision {
		t.Fatal("reopening changed the installation identity", err)
	}
}

func TestConnectedAppsDisabledAccountWithdrawsTools(t *testing.T) {
	ctx := context.Background()
	store := openAppsStore(t)
	p := newAppsPeer(t, store)
	connection := connectTestApp(t, p, "personal")
	state, _ := store.LoadApps(ctx)
	hash := connectedapps.BindingHash(state, connection)
	p.disabled.Store(true)
	if _, err := p.service.Execute(ctx, connection.ID, hash, p.tool.Slug, p.tool.Version, connectedapps.ToolHash(p.tool), json.RawMessage(`{"to":"friend@example.test"}`)); !errors.Is(err, connectedapps.ErrDenied) {
		t.Fatal("disabled account dispatched an operation", err)
	}
	if err := p.service.Check(ctx, connection.ID); err != nil {
		t.Fatal(err)
	}
	state, _ = store.LoadApps(ctx)
	if state.Connections[0].Status != "expired" {
		t.Fatal("disabled account still presented as connected")
	}
	definitions, err := appstools.Definitions(ctx, p.service)
	if err != nil || len(definitions) != 0 || p.executions.Load() != 0 {
		t.Fatal("disabled account retained tools or executed", err)
	}
}

func TestConnectedAppsDefaultEnvironmentKey(t *testing.T) {
	ctx := context.Background()
	t.Setenv("COMPOSIO_API_KEY", "")
	store, err := Open(ctx, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	p := newAppsPeer(t, store)
	p.service.Secrets = secret.Environment{}
	view, err := p.service.View(ctx, "", "", "")
	if err != nil || !view.Suggested || view.Unavailable {
		t.Fatal("missing default key must retain the offline directory", err, view)
	}
	before, _ := store.LoadApps(ctx)
	t.Setenv("COMPOSIO_API_KEY", "composio-project-private")
	view, err = p.service.View(ctx, "", "", "")
	if err != nil || view.Suggested || view.Unavailable || len(view.Catalog.Items) != 1 {
		t.Fatal("environment key must enable the live catalogue without setup", err, view)
	}
	after, _ := store.LoadApps(ctx)
	if after.KeyRef != "" || after.Revision != before.Revision || len(after.Connections) != 0 {
		t.Fatal("reading the default created persistent state or connections")
	}
	if _, err = p.service.Connect(ctx, "gmail", "personal", ""); err != nil {
		t.Fatal("default key could not start authorization", err)
	}
	after, _ = store.LoadApps(ctx)
	if after.KeyRef != connectedapps.DefaultKeyRef || len(after.Connections) != 1 || after.Connections[0].Status != "pending" {
		t.Fatal("explicit connect did not persist the reference and pending binding")
	}
	t.Setenv("COMPOSIO_API_KEY", "")
	view, err = p.service.View(ctx, "", "", "")
	if err != nil || view.Suggested || !view.Unavailable || len(view.State.Connections) != 1 {
		t.Fatal("removing a persisted credential must preserve the unavailable connection", err)
	}
}

func TestConnectedAppsExplicitReferenceOverridesEnvironmentDefault(t *testing.T) {
	ctx := context.Background()
	t.Setenv("COMPOSIO_API_KEY", "composio-project-private")
	t.Setenv("STEWARD_TEST_COMPOSIO_CUSTOM", "composio-project-private")
	store, err := Open(ctx, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	p := newAppsPeer(t, store)
	p.service.Secrets = secret.Environment{}
	state, _ := store.LoadApps(ctx)
	state.KeyRef = "env:STEWARD_TEST_COMPOSIO_CUSTOM"
	if err := store.SaveApps(ctx, state, state.Revision); err != nil {
		t.Fatal(err)
	}
	for _, available := range []bool{true, false} {
		if !available {
			t.Setenv("STEWARD_TEST_COMPOSIO_CUSTOM", "")
		}
		view, err := p.service.View(ctx, "", "", "")
		if err != nil || view.State.KeyRef != state.KeyRef || view.Suggested || view.Unavailable == available {
			t.Fatal("explicit reference must win even when its credential is missing", err)
		}
	}
}

func (appSecrets) Put(context.Context, string, []byte) error { return nil }
func (appSecrets) Delete(context.Context, string) error      { return nil }

func (appSecrets) Get(context.Context, string) ([]byte, error) {
	return []byte("composio-project-private"), nil
}

type appsPeer struct {
	service     *connectedapps.Service
	executions  atomic.Int32
	revocations atomic.Int32
	links       atomic.Int32
	broken      atomic.Bool
	drift       atomic.Bool
	wrongUser   atomic.Bool
	disabled    atomic.Bool
	response    atomic.Pointer[string]
	tool        composio.Tool
}

func newAppsPeer(t *testing.T, s *Store) *appsPeer {
	t.Helper()
	p := &appsPeer{}
	p.tool.Slug = "GMAIL_SEND_EMAIL"
	p.tool.Name = "Send email"
	p.tool.Description = "Send one email"
	p.tool.Version = "20261101_00"
	p.tool.Toolkit.Slug = "gmail"
	p.tool.Input = json.RawMessage(`{"type":"object","properties":{"to":{"type":"string"}},"required":["to"]}`)
	p.service = &connectedapps.Service{Store: s, Secrets: appSecrets{}}
	peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("x-api-key") != "composio-project-private" {
			t.Error("missing project key")
		}
		w.Header().Set("Content-Type", "application/json")
		state, err := s.LoadApps(r.Context())
		if err != nil {
			t.Error(err)
			http.Error(w, "storage", 500)
			return
		}
		switch r.URL.Path {
		case "/auth_configs":
			_, _ = w.Write([]byte(`{"items":[]}`))
		case "/toolkits":
			_ = json.NewEncoder(w).Encode(map[string]any{"items": []any{map[string]string{"slug": "gmail", "name": "Gmail"}}, "next_cursor": "more", "total_items": 1700})
		case "/toolkits/gmail":
			_, _ = w.Write([]byte(`{"slug":"gmail","name":"Gmail"}`))
		case "/tool_router/session":
			_, _ = w.Write([]byte(`{"session_id":"trs_test"}`))
		case "/tool_router/session/trs_test/link":
			p.links.Add(1)
			var body struct {
				Alias string `json:"alias"`
			}
			_ = json.NewDecoder(r.Body).Decode(&body)
			_ = json.NewEncoder(w).Encode(map[string]string{"redirect_url": "https://app.composio.dev/link/private", "connected_account_id": "ca_" + body.Alias})
		case "/connected_accounts/ca_personal", "/connected_accounts/ca_work":
			if r.Method == "DELETE" {
				w.WriteHeader(204)
				return
			}
			user := state.UserID
			if p.wrongUser.Load() {
				user = "another_owner"
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"id": strings.TrimPrefix(r.URL.Path, "/connected_accounts/"), "user_id": user, "status": "ACTIVE", "is_disabled": p.disabled.Load(), "toolkit": map[string]string{"slug": "gmail"}, "data": map[string]string{"access_token": "never-persist"}})
		case "/connected_accounts/ca_personal/revoke", "/connected_accounts/ca_work/revoke":
			p.revocations.Add(1)
			// Provider revocation can fail; local closure still must persist.
			http.Error(w, "unsupported", 400)
		case "/tools":
			if r.URL.Query().Get("toolkit_slug") != "gmail" {
				t.Error("cross-toolkit search")
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"items": []composio.Tool{p.tool}, "next_cursor": "next-tools"})
		case "/tools/GMAIL_SEND_EMAIL":
			if r.URL.Query().Get("version") != p.tool.Version {
				t.Error("unfrozen tool version")
			}
			d := p.tool
			if p.drift.Load() {
				d.Description = "Changed operation"
			}
			_ = json.NewEncoder(w).Encode(d)
		case "/tools/execute/GMAIL_SEND_EMAIL":
			p.executions.Add(1)
			var body struct {
				Account   string            `json:"connected_account_id"`
				User      string            `json:"user_id"`
				Version   string            `json:"version"`
				Arguments map[string]string `json:"arguments"`
			}
			if json.NewDecoder(r.Body).Decode(&body) != nil || body.Account != "ca_personal" || body.User != state.UserID || body.Version != p.tool.Version || body.Arguments["to"] != "friend@example.test" {
				t.Error("wrong execution binding", body)
			}
			if p.broken.Load() {
				http.Error(w, "uncertain", http.StatusBadGateway)
				return
			}
			if response := p.response.Load(); response != nil {
				_, _ = io.WriteString(w, *response)
				return
			}
			_, _ = w.Write([]byte(`{"successful":true,"data":{"message_id":"message-1"},"log_id":"log-1"}`))
		default:
			t.Error("unexpected request", r.Method, r.URL)
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(peer.Close)
	p.service.Client = composio.Client{Endpoint: peer.URL}
	return p
}
func openAppsStore(t *testing.T) *Store {
	t.Helper()
	s, err := Open(context.Background(), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}
func connectTestApp(t *testing.T, p *appsPeer, alias string) connectedapps.Connection {
	t.Helper()
	ctx := context.Background()
	state, err := p.service.State(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if state.KeyRef == "" {
		if err = p.service.Configure(ctx, "env:COMPOSIO_API_KEY", state.Revision); err != nil {
			t.Fatal(err)
		}
	}
	if _, err = p.service.Connect(ctx, "gmail", alias, ""); err != nil {
		t.Fatal(err)
	}
	state, _ = p.service.State(ctx)
	c := state.Connections[len(state.Connections)-1]
	if err = p.service.Check(ctx, c.ID); err != nil {
		t.Fatal(err)
	}
	state, _ = p.service.State(ctx)
	return state.Connections[len(state.Connections)-1]
}
func TestConnectedAppsLifecyclePersistentIsolationAndRevocation(t *testing.T) {
	ctx := context.Background()
	s := openAppsStore(t)
	p := newAppsPeer(t, s)
	v, err := p.service.View(ctx, "gmail", "", "")
	if err != nil || v.Suggested || v.Unavailable || len(v.Catalog.Items) != 1 {
		t.Fatal(v, err)
	}
	one := connectTestApp(t, p, "personal")
	two := connectTestApp(t, p, "work")
	state, _ := s.LoadApps(ctx)
	if state.Connections[0].AccountID == state.Connections[1].AccountID {
		t.Fatal("accounts collapsed")
	}
	if err = p.service.Configure(ctx, "env:ANOTHER_PROJECT", state.Revision); !errors.Is(err, connectedapps.ErrConflict) {
		t.Fatal("project changed with active connections", err)
	}
	before := state
	if err = s.SaveApps(ctx, state, state.Revision-1); !errors.Is(err, connectedapps.ErrConflict) {
		t.Fatal("missing CAS", err)
	}
	p.wrongUser.Store(true)
	if err = p.service.Check(ctx, one.ID); !errors.Is(err, connectedapps.ErrDenied) {
		t.Fatal("cross-user account accepted", err)
	}
	p.wrongUser.Store(false)
	if err = p.service.Disconnect(ctx, one.ID); err != nil {
		t.Fatal(err)
	}
	if err = p.service.Check(ctx, one.ID); !errors.Is(err, connectedapps.ErrDenied) {
		t.Fatal("late auth resurrected revoked connection", err)
	}
	reopened, err := Open(ctx, filepath.Dir(s.path))
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	after, err := reopened.LoadApps(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if after.UserID != before.UserID || after.Connections[0].Status != "disconnected" || after.Connections[0].RevocationConfirmed || after.Connections[1].ID != two.ID || after.Connections[1].Status != "active" {
		t.Fatal(after)
	}
	var document string
	_ = s.db.QueryRow(`SELECT document_json FROM connected_apps`).Scan(&document)
	if strings.Contains(document, "composio-project-private") || strings.Contains(document, "never-persist") || strings.Contains(document, "/link/") {
		t.Fatal("credential leaked", document)
	}
}

func TestConnectedAppsApprovedExecutionReplayAndUnknown(t *testing.T) {
	for _, scenario := range []string{"success", "unknown", "revoked", "descriptor-drift", "wrong-user"} {
		t.Run(scenario, func(t *testing.T) {
			ctx := context.Background()
			initial := openAppsStore(t)
			p := newAppsPeer(t, initial)
			c := connectTestApp(t, p, "personal")
			defs, err := appstools.Definitions(ctx, p.service)
			if err != nil {
				t.Fatal(err)
			}
			for i := range defs {
				defs[i].Spec.Exposure = tool.Direct
			} // Exercise the durable executor separately from Code Mode below.
			registry, err := tool.NewRegistry(defs)
			if err != nil {
				t.Fatal(err)
			}
			if len(agent.MainTools(registry.Describe())) != 2 || len(agent.WorkerTools(registry.Describe())) != 2 {
				t.Fatal("missing role capabilities")
			}
			var execute tool.Definition
			for _, d := range defs {
				if d.Spec.Permission == "external_effect" {
					execute = d
				}
			}
			args, _ := json.Marshal(map[string]string{"tool_slug": p.tool.Slug, "version": p.tool.Version, "descriptor_hash": connectedapps.ToolHash(p.tool), "arguments_json": `{"to":"friend@example.test"}`})
			calls := []llm.ToolCall{{ID: "send-mail", Name: execute.Spec.Name, Arguments: string(args)}}
			s, r, claim, e, exchange := admitPolicy(t, defs, calls)
			// Move the same persisted binding into the actual durable Run store.
			state, _ := initial.LoadApps(ctx)
			target, _ := s.LoadApps(ctx)
			if err = s.SaveApps(ctx, state, target.Revision); err != nil {
				t.Fatal(err)
			}
			p.service.Store = s
			if got, err := e.Execute(ctx, exchange, calls); !errors.Is(err, tool.ErrSuspended) || p.executions.Load() != 0 {
				t.Fatal("executed without approval", got, err)
			}
			approvePolicy(t, s, r)
			switch scenario {
			case "unknown":
				p.broken.Store(true)
			case "revoked":
				if err = p.service.Disconnect(ctx, c.ID); err != nil {
					t.Fatal(err)
				}
			case "descriptor-drift":
				p.drift.Store(true)
			case "wrong-user":
				p.wrongUser.Store(true)
			}
			result, err := e.Execute(ctx, exchange, calls)
			if err != nil || len(result) != 1 {
				t.Fatal(result, err)
			}
			want := int32(0)
			if scenario == "success" || scenario == "unknown" {
				want = 1
			}
			if p.executions.Load() != want {
				t.Fatal("incorrect external effects", p.executions.Load())
			}
			if scenario == "success" && (result[0].Error != nil || !strings.Contains(result[0].Text, "message-1")) {
				t.Fatal(result)
			}
			if scenario == "unknown" && scalar(t, s, `SELECT count(*) FROM tool_invocation WHERE status='unknown'`) != 1 {
				t.Fatal("unknown not persisted", result)
			}
			if scenario != "success" && scenario != "unknown" && (result[0].Error == nil || result[0].Error.Code != "PERMISSION_DENIED") {
				t.Fatal(result)
			}
			if err = r.Release(ctx, claim); err != nil {
				t.Fatal(err)
			}
			claim, err = r.Claim(ctx, claim.RunID())
			if err != nil {
				t.Fatal(err)
			}
			e.Store = r.ToolStore(claim)
			if _, err = e.Execute(ctx, exchange, calls); err != nil || p.executions.Load() != want {
				t.Fatal("effect repeated after recovery", err, p.executions.Load())
			}
		})
	}
}

func TestConnectedAppsRealCodeModeDiscoveryApprovalAndFrozenConnection(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	s, r, a, provider := childFixture(t, codemode.Definition())
	p := newAppsPeer(t, s)
	c := connectTestApp(t, p, "personal")
	defs, err := appstools.Definitions(ctx, p.service)
	if err != nil {
		t.Fatal(err)
	}
	for i := range defs {
		defs[i].Spec.CodeModeOnly = true
	}
	for _, spec := range a.Registry.Describe() {
		d, _ := a.Registry.Resolve(spec.Name)
		defs = append(defs, d)
	}
	registry, err := tool.NewRegistry(defs)
	if err != nil {
		t.Fatal(err)
	}
	r.registry, a.Registry = registry, registry
	r.ConfigureJavaScript(realCodeRuntime(t))
	namespace := appstools.Prefix + strings.ReplaceAll(c.ID, "-", "") + "__"
	source := `const found = await tools.` + namespace + `search_tools({query:"send email"}); const operation = found.items[0]; return await tools.` + namespace + `execute({tool_slug:operation.slug, version:operation.version, descriptor_hash:operation.descriptor_hash, arguments_json:JSON.stringify({to:"friend@example.test"})});`
	args, _ := json.Marshal(map[string]string{"source": source})
	provider.respond = delegateToolWork(func(req llm.GenerationRequest) (llm.GenerationResult, error) {
		if req.Messages[len(req.Messages)-1].Role == "tool" {
			return llm.GenerationResult{Text: "App operation observed."}, nil
		}
		return calls(codemode.ToolName, string(args)), nil
	})
	mustSubmit(t, a, "connected-app-code", "Send this exact email using the connected personal account")
	if err = a.Drain(ctx); err != nil {
		t.Fatal(err)
	}
	if p.executions.Load() != 0 || scalar(t, s, `SELECT count(*) FROM approval WHERE status='pending'`) != 1 {
		rows, _ := s.db.Query(`SELECT tool_name,result_json FROM tool_invocation`)
		defer rows.Close()
		for rows.Next() {
			var name, result string
			_ = rows.Scan(&name, &result)
			t.Log(name, result)
		}
		t.Fatal("Code Mode bypassed approval or failed discovery", p.executions.Load())
	}
	// A newly connected account is visible to new snapshots, not the Root that
	// is currently waiting for approval.
	frozen := agent.WorkerTools(registry.Describe())
	connectTestApp(t, p, "work")
	updated, err := appstools.Definitions(ctx, p.service)
	if err != nil {
		t.Fatal(err)
	}
	for i := range updated {
		updated[i].Spec.CodeModeOnly = true
	}
	if err = registry.ReplaceGroup(appstools.Prefix, updated); err != nil {
		t.Fatal(err)
	}
	selected, err := registry.Select(frozen)
	if err != nil {
		t.Fatal(err)
	}
	for _, spec := range selected.Describe() {
		if strings.Contains(spec.NamespaceDescription, "/ work") {
			t.Fatal("old snapshot expanded")
		}
	}
	approvePolicy(t, s, r)
	if err = a.Drain(ctx); err != nil {
		t.Fatal(err)
	}
	if p.executions.Load() != 1 || scalar(t, s, `SELECT count(*) FROM tool_invocation WHERE tool_name=? AND status='succeeded'`, namespace+"execute") != 1 {
		t.Fatal("approved Code Mode operation failed", p.executions.Load())
	}
	if err = a.Drain(ctx); err != nil || p.executions.Load() != 1 {
		t.Fatal("Code Mode replay repeated effect", err)
	}
}

func TestConnectedAppsPublicToolkitRequiresExplicitEnableAndNoAuthTool(t *testing.T) {
	ctx := context.Background()
	s := openAppsStore(t)
	state, _ := s.LoadApps(ctx)
	state.KeyRef = "env:COMPOSIO_API_KEY"
	if err := s.SaveApps(ctx, state, state.Revision); err != nil {
		t.Fatal(err)
	}
	var executions atomic.Int32
	var requireAuth atomic.Bool
	d := composio.Tool{Slug: "PUBLIC_LOOKUP", Version: "20261101_00", NoAuth: true, Input: json.RawMessage(`{"type":"object"}`)}
	d.Toolkit.Slug = "publicapp"
	peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/toolkits/publicapp":
			_, _ = w.Write([]byte(`{"slug":"publicapp","name":"Public app","no_auth":true}`))
		case "/tools/PUBLIC_LOOKUP":
			result := d
			result.NoAuth = !requireAuth.Load()
			_ = json.NewEncoder(w).Encode(result)
		case "/tools/execute/PUBLIC_LOOKUP":
			executions.Add(1)
			_, _ = w.Write([]byte(`{"successful":true,"data":{"answer":42}}`))
		default:
			t.Error("public binding touched an account/auth endpoint", r.URL)
			http.NotFound(w, r)
		}
	}))
	defer peer.Close()
	service := &connectedapps.Service{Store: s, Secrets: appSecrets{}, Client: composio.Client{Endpoint: peer.URL}}
	defs, err := appstools.Definitions(ctx, service)
	if err != nil || len(defs) != 0 {
		t.Fatal("catalogue automatically granted access")
	}
	link, err := service.Connect(ctx, "publicapp", "personal", "")
	if err != nil || link.URL != "" {
		t.Fatal(link, err)
	}
	state, _ = s.LoadApps(ctx)
	c := state.Connections[0]
	if !c.Public || c.Status != "active" || c.AccountID != "" {
		t.Fatal(c)
	}
	result, err := service.Execute(ctx, c.ID, connectedapps.BindingHash(state, c), d.Slug, d.Version, connectedapps.ToolHash(d), json.RawMessage(`{}`))
	if err != nil || !result.Successful || executions.Load() != 1 {
		t.Fatal(result, err)
	}
	requireAuth.Store(true)
	if _, err = service.Execute(ctx, c.ID, connectedapps.BindingHash(state, c), d.Slug, d.Version, connectedapps.ToolHash(d), json.RawMessage(`{}`)); !errors.Is(err, connectedapps.ErrDenied) || executions.Load() != 1 {
		t.Fatal("public tool escalated to account access", err)
	}
	if err = service.Disconnect(ctx, c.ID); err != nil {
		t.Fatal(err)
	}
}

func TestConnectedAppsDisconnectNeverRevokesAMismatchedAccount(t *testing.T) {
	s := openAppsStore(t)
	peer := newAppsPeer(t, s)
	c := connectTestApp(t, peer, "personal")
	peer.wrongUser.Store(true)
	if err := peer.service.Disconnect(context.Background(), c.ID); err != nil {
		t.Fatal(err)
	}
	state, err := s.LoadApps(context.Background())
	if err != nil || state.Connections[0].Status != "disconnected" || peer.revocations.Load() != 0 {
		t.Fatal("wrong user's grant revoked", state, err)
	}
}

func TestConnectedAppsAuthorizationReuseAndExplicitReconnect(t *testing.T) {
	ctx := context.Background()
	s := openAppsStore(t)
	p := newAppsPeer(t, s)
	state, _ := s.LoadApps(ctx)
	if err := p.service.Configure(ctx, "env:COMPOSIO_API_KEY", state.Revision); err != nil {
		t.Fatal(err)
	}
	first, err := p.service.Connect(ctx, "gmail", "", "")
	if err != nil || first.ConnectionID == "" {
		t.Fatal(first, err)
	}
	for range 2 {
		again, e := p.service.Connect(ctx, "gmail", "", "")
		if e != nil || again != first {
			t.Fatal("repeated click created another authorization", again, e)
		}
		continued, e := p.service.Continue(ctx, first.ConnectionID)
		if e != nil || continued != first {
			t.Fatal("continue renewed the link", continued, e)
		}
	}
	if p.links.Load() != 1 {
		t.Fatal("duplicate account created")
	}
	if err = p.service.Check(ctx, first.ConnectionID); err != nil {
		t.Fatal(err)
	}
	stable, _ := s.LoadApps(ctx)
	if err = p.service.Check(ctx, first.ConnectionID); err != nil {
		t.Fatal(err)
	}
	unchanged, _ := s.LoadApps(ctx)
	if stable.Revision != unchanged.Revision {
		t.Fatal("unchanged status invalidated deployment settings")
	}
	if _, err = p.service.Connect(ctx, "gmail", "", ""); !errors.Is(err, connectedapps.ErrAliasRequired) {
		t.Fatal("additional account did not require label", err)
	}
	if _, err = p.service.Reconnect(ctx, first.ConnectionID); !errors.Is(err, connectedapps.ErrDenied) {
		t.Fatal("reconnected working account", err)
	}
	second, err := p.service.Connect(ctx, "gmail", "work", "")
	if err != nil {
		t.Fatal(err)
	}
	// Browser links deliberately do not survive a process restart. The owner
	// can explicitly restart authorization without reviving the old binding.
	restarted := &connectedapps.Service{Store: s, Secrets: appSecrets{}, Client: p.service.Client}
	if _, err = restarted.Continue(ctx, second.ConnectionID); !errors.Is(err, connectedapps.ErrLinkExpired) {
		t.Fatal(err)
	}
	third, err := restarted.Reconnect(ctx, second.ConnectionID)
	if err != nil || third.ConnectionID == second.ConnectionID {
		t.Fatal(third, err)
	}
	if err = restarted.Check(ctx, second.ConnectionID); !errors.Is(err, connectedapps.ErrDenied) {
		t.Fatal("old authorization resurrected", err)
	}
	state, _ = s.LoadApps(ctx)
	if state.Connections[0].Status != "active" || state.Connections[1].Status != "disconnected" || state.Connections[2].Status != "pending" {
		t.Fatal(state)
	}
}

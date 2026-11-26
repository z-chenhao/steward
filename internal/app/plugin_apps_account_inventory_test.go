package app

import (
	"context"
	"errors"
	"github.com/z-chenhao/assistant/internal/config"
	"github.com/z-chenhao/assistant/internal/connectedapps"
	"github.com/z-chenhao/assistant/internal/interaction/web"
	"github.com/z-chenhao/assistant/internal/llm/memory"
	"github.com/z-chenhao/assistant/internal/rsi"
	"github.com/z-chenhao/assistant/internal/rsi/builtin"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
)

func TestAppsAccountInventoryActualDefaultPrivateStateAndCold(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	cfg, err := config.Load("", func(k string) (string, bool) { return root, k == "STEWARD_DATA_ROOT" })
	if err != nil {
		t.Fatal(err)
	}
	secrets := memory.New()
	a, err := OpenRuntimeWithSecrets(ctx, cfg, secrets)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = a.Close() }()
	a.eventsRequired.Store(false)
	if _, err = a.BindChannel(ctx, "web", "browser", "local", true, 0); err != nil {
		t.Fatal(err)
	}
	carrier := filepath.Join(t.TempDir(), "steward")
	buildTestBinary(t, "../../cmd/steward", carrier)
	host := a.Plugins()
	host.Catalog.Launcher = carrier
	bind := func() {
		host = a.Plugins()
		host.Catalog.Launcher = carrier
		a.connectedApps.ReadInventory = func(c context.Context) (connectedapps.AccountInventoryPolicy, error) {
			return a.appsAccountInventoryWithHost(c, host)
		}
	}
	bind()
	if err = builtin.EnsureDefault(ctx, host, builtin.Directories{DOCX: defaultFeaturePackage(t, "docx-reader", nil), AppsAccounts: defaultFeaturePackage(t, "apps-accounts", nil), AppsDirectory: defaultFeaturePackage(t, "apps-directory", nil)}); err != nil {
		t.Fatal(err)
	}
	// Retain the original graph and its independent Owner; remove only directory.
	initial, e := host.Store.Status(ctx)
	if e != nil {
		t.Fatal(e)
	}
	original, e := host.Store.Graph(ctx, initial.Revision)
	if e != nil {
		t.Fatal(e)
	}
	kept := original.Graph
	kept.Bindings = nil
	for _, b := range original.Graph.Bindings {
		if b.InstanceID != "apps-directory" {
			kept.Bindings = append(kept.Bindings, b)
		}
	}
	target, e := host.Prepare(ctx, kept)
	if e != nil {
		t.Fatal(e)
	}
	var policyID string
	if e = a.store.Plugins().DB.QueryRowContext(ctx, `SELECT policy_id FROM rsi_candidate WHERE target=?`, initial.Revision).Scan(&policyID); e != nil {
		t.Fatal(e)
	}
	policy, e := host.Store.Policy(ctx, policyID)
	if e != nil {
		t.Fatal(e)
	}
	policy.ID = "inventory-without-directory"
	cases := policy.Cases
	policy.Cases = nil
	policy.AllowedContracts = nil
	for _, c := range cases {
		if c.InstanceID == "apps-directory" {
			continue
		}
		policy.Cases = append(policy.Cases, c)
		seen := false
		for _, x := range policy.AllowedContracts {
			if x == c.Contract {
				seen = true
			}
		}
		if !seen {
			policy.AllowedContracts = append(policy.AllowedContracts, c.Contract)
		}
	}
	if e = host.Store.PutPolicy(ctx, policy); e != nil {
		t.Fatal(e)
	}
	if e = host.Propose(ctx, rsi.Candidate{ID: "inventory-remove-directory", Baseline: initial.Revision, Target: target.Revision, PolicyID: policy.ID, Goal: "Retain independent account management when directory is removed"}); e != nil {
		t.Fatal(e)
	}
	decision, e := host.Verify(ctx, "inventory-remove-directory")
	if e != nil {
		t.Fatal(e)
	}
	if _, e = host.Activate(ctx, rsi.Activation{OperationID: "inventory-remove-directory", Expected: initial.Revision, Target: target.Revision, DecisionID: decision}); e != nil {
		t.Fatal(e)
	}
	const token = "synthetic-inventory-owner-token-24-chars"
	if e = secrets.Put(ctx, "credential:inventory-owner", []byte(token)); e != nil {
		t.Fatal(e)
	}
	page := func() (int, string) {
		t.Helper()
		server := &web.Server{Config: web.Config{TokenRef: "credential:inventory-owner"}, Core: a, Secrets: secrets, Apps: a.connectedApps}
		request := httptest.NewRequest("GET", "http://127.0.0.1:8080/settings/apps?view=connected", nil)
		request.Header.Set("Authorization", "Bearer "+token)
		response := httptest.NewRecorder()
		server.ServeHTTP(response, request)
		return response.Code, response.Body.String()
	}
	state, err := a.store.LoadApps(ctx)
	if err != nil {
		t.Fatal(err)
	}
	state.Connections = []connectedapps.Connection{{Toolkit: "z", Name: "Zulu", Status: "pending", ID: "private-z", AccountID: "private-provider-z", Alias: "private-alias"}, {Toolkit: "z", Name: "later", Status: "active", Public: true}, {Toolkit: "a", Name: "Alpha", Status: "active"}, {Toolkit: "gone", Name: "Gone", Status: "disconnected", RevocationConfirmed: true}}
	if err = a.store.SaveApps(ctx, state, state.Revision); err != nil {
		t.Fatal(err)
	}
	state, err = a.store.LoadApps(ctx)
	if err != nil {
		t.Fatal(err)
	}
	project := func() {
		t.Helper()
		p, e := a.connectedApps.AccountInventory(ctx)
		if e != nil {
			t.Fatal(e)
		}
		got, e := p.Project(ctx, state, true)
		if e != nil || len(got.Rows) != 2 || got.Rows[0] != 2 || got.Rows[1] != 0 {
			t.Fatal("actual independent account projection", got, e)
		}
		flags := got.States([]string{"a", "z", "gone"})
		if !flags[0].Connected || flags[0].PublicActive || !flags[1].Connected || !flags[1].Pending || !flags[1].PublicActive || flags[2].Managed {
			t.Fatal("lost original state facts", flags)
		}
	}
	project()
	if code, body := page(); code != 200 || !strings.Contains(body, "Alpha") || !strings.Contains(body, "Zulu") {
		t.Fatal("directory removal disabled actual managed account page", code, body)
	}
	p, err := a.connectedApps.AccountInventory(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err = a.store.SaveApps(ctx, state, state.Revision); err != nil {
		t.Fatal(err)
	}
	if _, err = p.Project(ctx, state, true); !errors.Is(err, connectedapps.ErrConflict) {
		t.Fatal("stale state released", err)
	}
	state, err = a.store.LoadApps(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err = a.Close(); err != nil {
		t.Fatal(err)
	}
	a, err = OpenRuntimeWithSecrets(ctx, cfg, secrets)
	if err != nil {
		t.Fatal(err)
	}
	a.eventsRequired.Store(false)
	bind()
	// First dependent operation after actual Store recovery: no reselect or repair.
	project()
	if code, body := page(); code != 200 || !strings.Contains(body, "Zulu") {
		t.Fatal("first recovered account page lost original state", code)
	}
	status, err := a.Plugins().Store.Status(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err = a.Plugins().Store.Quarantine(ctx, status.Revision); err != nil {
		t.Fatal(err)
	}
	if p, err := a.connectedApps.AccountInventory(ctx); err == nil {
		if _, e := p.Project(ctx, state, true); e == nil {
			t.Fatal("quarantined managed inventory entered native policy")
		}
	}
	if code, _ := page(); code != 503 {
		t.Fatal("quarantined inventory page used native fallback", code)
	}
}

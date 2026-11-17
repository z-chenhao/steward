package rsi_test

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/z-chenhao/assistant/internal/agent"
	agentsqlite "github.com/z-chenhao/assistant/internal/agent/sqlite"
	conversationsqlite "github.com/z-chenhao/assistant/internal/conversation/sqlite"
	"github.com/z-chenhao/assistant/internal/llm"
	"github.com/z-chenhao/assistant/internal/plugin/conversation/contract"
	"github.com/z-chenhao/assistant/internal/prompt"
	"github.com/z-chenhao/assistant/internal/rsi"
	"github.com/z-chenhao/assistant/internal/rsi/builtin"
	"github.com/z-chenhao/assistant/pkg/rsiplugin"
)

func conversationPackageSource(t *testing.T) string { return conversationPackageVersion(t, "1.0.1") }
func conversationPackageVersion(t *testing.T, version string) string {
	t.Helper()
	source := t.TempDir()
	cmd := exec.Command("go", "build", "-o", filepath.Join(source, "plugin"), "../../cmd/plugins/conversation")
	if version != "1.0.1" {
		path, err := filepath.Abs("../plugin/conversation/plugin.go")
		if err != nil {
			t.Fatal(err)
		}
		raw, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		code := strings.Replace(string(raw), `Version: "1.0.1"`, `Version: "`+version+`"`, 1)

		if version != "1.0.1" {
			code = strings.Replace(code, `strings.TrimSuffix(rolePolicy, "\n")`, `strings.TrimSuffix(rolePolicy, "\n")+"\n\nINDEPENDENT_MAIN_METHOD_11"`, 1)
		}
		if code == string(raw) {
			t.Fatal("trace overlay unchanged")
		}
		override := filepath.Join(t.TempDir(), "plugin.go")
		if err = os.WriteFile(override, []byte(code), 0600); err != nil {
			t.Fatal(err)
		}
		overlay, _ := json.Marshal(struct{ Replace map[string]string }{map[string]string{path: override}})
		file := filepath.Join(t.TempDir(), "overlay.json")
		if err = os.WriteFile(file, overlay, 0600); err != nil {
			t.Fatal(err)
		}
		cmd = exec.Command("go", "build", "-overlay", file, "-o", filepath.Join(source, "plugin"), "../../cmd/plugins/conversation")
	}

	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatal(string(out), err)
	}
	raw, err := os.ReadFile("../../plugins/conversation/steward.plugin.json")
	if err != nil {
		t.Fatal(err)
	}
	var m rsiplugin.Manifest
	if err = rsiplugin.Decode(raw, &m); err != nil {
		t.Fatal(err)
	}
	m.Version = version
	m, err = rsi.Seal(source, m)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ = json.Marshal(m)
	if err = os.WriteFile(filepath.Join(source, "steward.plugin.json"), raw, 0600); err != nil {
		t.Fatal(err)
	}
	return source
}

func TestConversationIndependentPolicyPinsPendingRunAndPreservesSafety(t *testing.T) {
	requireSandbox(t)
	ctx := context.Background()
	f := newFixture(t)
	seen := map[string]string{}
	f.actor.Provider = recordingProvider{store: f.store, respond: func(req llm.GenerationRequest) llm.GenerationResult {
		seen[req.RunID] = req.Messages[0].Text
		return llm.GenerationResult{Text: "done"}
	}}
	old, err := f.actor.Submit(ctx, "conversation-old", "historical Main input")
	if err != nil {
		t.Fatal(err)
	}
	if err = builtin.EnsureDefault(ctx, f.host, builtin.Directories{DOCX: docxPackageSource(t), Conversation: conversationPackageSource(t)}); err != nil {
		t.Fatal(err)
	}
	first, _ := f.host.Store.Status(ctx)
	if err = f.actor.Drain(ctx); err != nil {
		t.Fatal(err)
	}
	calls := func(id string) int {
		t.Helper()
		var n int
		if err := f.db.QueryRow(`SELECT count(*) FROM rsi_call WHERE run_id=? AND instance_id='conversation' AND result_json IS NOT NULL`, id).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}
	if calls(old.RunID) != 0 {
		t.Fatal("old Main reinterpreted")
	}
	pending, err := f.actor.Submit(ctx, "conversation-pinned", "pending Main method")
	if err != nil {
		t.Fatal(err)
	}
	p, err := f.host.Install(ctx, conversationPackageVersion(t, "1.1.0"))
	if err != nil {
		t.Fatal(err)
	}
	g, err := f.host.Store.Graph(ctx, first.Revision)
	if err != nil {
		t.Fatal(err)
	}
	next := g.Graph
	next.Bindings = append([]rsiplugin.Binding(nil), next.Bindings...)
	for i := range next.Bindings {
		if next.Bindings[i].InstanceID == "conversation" {
			next.Bindings[i].PackageDigest = p.Digest
		}
	}
	second, err := f.host.Prepare(ctx, next)
	if err != nil {
		t.Fatal(err)
	}
	var oldPolicyID string
	if err = f.db.QueryRow(`SELECT policy_id FROM rsi_candidate WHERE target=?`, first.Revision).Scan(&oldPolicyID); err != nil {
		t.Fatal(err)
	}
	// The operator authors the intended new result independently. The candidate
	// cannot change either policy or derive its own expected verdict.
	policy, err := f.host.Store.Policy(ctx, oldPolicyID)
	if err != nil {
		t.Fatal(err)
	}
	policy.ID = "conversation-method-11"
	expected, _ := json.Marshal(contract.Policy{RolePolicy: strings.TrimPrefix(prompt.Main(), agent.MainIdentity) + "\n\nINDEPENDENT_MAIN_METHOD_11", ExecutionPolicy: strings.TrimSuffix(prompt.MainExecution(), "\n</rules>")})
	for i := range policy.Cases {
		if policy.Cases[i].Contract == contract.Contract {
			policy.Cases[i].Expected = expected
		}
	}
	if err = f.host.Store.PutPolicy(ctx, policy); err != nil {
		t.Fatal(err)
	}
	selectVersion := func(id, baseline, target, policy string) {
		t.Helper()
		if err := f.host.Propose(ctx, rsi.Candidate{ID: id, Baseline: baseline, Target: target, PolicyID: policy, Goal: "independent Main method change"}); err != nil {
			t.Fatal(err)
		}
		decision, err := f.host.Verify(ctx, id)
		if err != nil {
			t.Fatal(err)
		}
		if _, err = f.host.Activate(ctx, rsi.Activation{OperationID: id, Expected: baseline, Target: target, DecisionID: decision}); err != nil {
			t.Fatal(err)
		}
	}
	selectVersion("conversation-upgrade", first.Revision, second.Revision, policy.ID)
	if err = f.actor.Drain(ctx); err != nil {
		t.Fatal(err)
	}
	if calls(pending.RunID) != 1 || seen[pending.RunID] != seen[old.RunID] || strings.Contains(seen[pending.RunID], "INDEPENDENT_MAIN_METHOD_11") {
		t.Fatal("queued Main changed policy during upgrade")
	}
	current, err := f.actor.Submit(ctx, "conversation-v11", "new independent method")
	if err != nil {
		t.Fatal(err)
	}
	if err = f.actor.Drain(ctx); err != nil {
		t.Fatal(err)
	}
	if calls(current.RunID) != 1 || !strings.Contains(seen[current.RunID], "INDEPENDENT_MAIN_METHOD_11") || !strings.Contains(seen[current.RunID], prompt.MainSafety()) || !strings.HasPrefix(seen[current.RunID], agent.MainIdentity) {
		t.Fatal("actual new model request bypassed policy or lost Host safety")
	}
	retained, err := f.actor.Submit(ctx, "conversation-retained-cold", "pending new method survives rollback and restart")
	if err != nil {
		t.Fatal(err)
	}
	selectVersion("conversation-rollback", second.Revision, first.Revision, oldPolicyID)
	if err = f.host.Store.Retire(ctx, p.Digest); err != nil {
		t.Fatal(err)
	}
	if _, err = f.host.Catalog.Load(p.Digest); err != nil {
		t.Fatal("retired Main implementation lost", err)
	}
	reopen := func() {
		t.Helper()
		if err := f.store.Close(); err != nil {
			t.Fatal(err)
		}
		store, err := agentsqlite.Open(ctx, f.root)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { store.Close() })
		history, err := conversationsqlite.Open(ctx, f.root)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { history.Close() })
		catalog := rsi.NewCatalog(f.root)
		catalog.Launcher = fixtureExecutable
		runs := store.Runs(history, f.actor.Registry)
		runs.ConfigurePlugins(catalog)
		f.store, f.runs = store, runs
		f.host = rsi.Host{Catalog: catalog, Store: store.Plugins()}
		provider := f.actor.Provider.(recordingProvider)
		provider.store = store
		f.actor.Store, f.actor.Provider = runs, provider
	}
	reopen()
	if err = f.actor.Drain(ctx); err != nil {
		t.Fatal(err)
	}
	if calls(retained.RunID) != 1 || !strings.Contains(seen[retained.RunID], "INDEPENDENT_MAIN_METHOD_11") || !strings.HasPrefix(seen[retained.RunID], agent.MainIdentity) || !strings.Contains(seen[retained.RunID], prompt.MainSafety()) {
		t.Fatal("cold recovery lost the retired frozen Main method or Host safety")
	}
	if _, err = f.host.Store.Deactivate(ctx, "conversation-disable", first.Revision); err != nil {
		t.Fatal(err)
	}
	reopen()
	disabled, err := f.actor.Submit(ctx, "conversation-disabled", "disabled must not use legacy method")
	if err != nil {
		t.Fatal(err)
	}
	if err = f.actor.Drain(ctx); !errors.Is(err, llm.ProviderUnavailable) {
		t.Fatal("disabled Main resurrected native methods", err)
	}
	var status string
	if err = f.db.QueryRow(`SELECT status FROM run WHERE run_id=?`, disabled.RunID).Scan(&status); err != nil || status != "failed" || seen[disabled.RunID] != "" {
		t.Fatal("unavailable initialization was not fenced and committed", status, err)
	}
}

package rsi_test

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"

	agentsqlite "github.com/z-chenhao/assistant/internal/agent/sqlite"
	conversationsqlite "github.com/z-chenhao/assistant/internal/conversation/sqlite"
	"github.com/z-chenhao/assistant/internal/llm"
	"github.com/z-chenhao/assistant/internal/plugin/contextcompact/contract"
	"github.com/z-chenhao/assistant/internal/rsi"
	"github.com/z-chenhao/assistant/internal/rsi/builtin"
	"github.com/z-chenhao/assistant/pkg/rsiplugin"
)

func contextPackageSource(t *testing.T) string { return contextPackageVersion(t, "1.0.1") }
func contextPackageVersion(t *testing.T, version string) string {
	t.Helper()
	source := t.TempDir()
	cmd := exec.Command("go", "build", "-o", filepath.Join(source, "plugin"), "../../cmd/plugins/context-compact")
	if version != "1.0.1" {
		path, err := filepath.Abs("../plugin/contextcompact/plugin.go")
		if err != nil {
			t.Fatal(err)
		}
		raw, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		code := strings.Replace(string(raw), `Version: "1.0.1"`, `Version: "`+version+`"`, 1)
		code = strings.Replace(code, `out.Instructions = instructions(f.SummaryBytes)`, `out.Instructions = instructions(f.SummaryBytes) + "INDEPENDENT_COMPACT_METHOD"`, 1)

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
		cmd = exec.Command("go", "build", "-overlay", file, "-o", filepath.Join(source, "plugin"), "../../cmd/plugins/context-compact")
	}

	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatal(string(out), err)
	}
	raw, err := os.ReadFile("../../plugins/context-compact/steward.plugin.json")
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

func TestContextPluginUsesFrozenRunVersionAndDurableSelection(t *testing.T) {
	requireSandbox(t)
	ctx := context.Background()
	f := newFixture(t)
	old, err := f.actor.Submit(ctx, "context-pre-plugin", "old execution")
	if err != nil {
		t.Fatal(err)
	}
	if err = builtin.EnsureDefault(ctx, f.host, builtin.Directories{DOCX: docxPackageSource(t), Context: contextPackageSource(t)}); err != nil {
		t.Fatal(err)
	}
	first, _ := f.host.Store.Status(ctx)
	if err = f.actor.Drain(ctx); err != nil {
		t.Fatal(err)
	}
	var count int
	if err = f.db.QueryRow(`SELECT count(*) FROM rsi_call WHERE run_id=? AND instance_id='context-compact'`, old.RunID).Scan(&count); err != nil || count != 0 {
		t.Fatal("old run retroactively migrated", count, err)
	}
	calls := 0
	var requests []llm.GenerationRequest
	f.actor.Provider = recordingProvider{store: f.store, respond: func(req llm.GenerationRequest) llm.GenerationResult {
		requests = append(requests, req)
		calls++
		if calls == 1 {
			return llm.GenerationResult{ToolCalls: []llm.ToolCall{{ID: "continuation", Name: "mcp__fixture__echo", Arguments: `{"text":"original tool result"}`}}}
		}
		return llm.GenerationResult{Text: "completed plugin-selected context"}
	}}
	var selectionReads atomic.Int64
	f.store.MainBoundary = func(point string) {
		if point == "after_context_policy_read" {
			selectionReads.Add(1)
		}
	}
	defer func() { f.store.MainBoundary = nil }()
	current, err := f.actor.Submit(ctx, "context-managed", "private current input")
	if err != nil {
		t.Fatal(err)
	}
	if err = f.actor.Drain(ctx); err != nil {
		t.Fatal(err)
	}
	if err = f.db.QueryRow(`SELECT count(*) FROM context_compaction WHERE run_id=?`, current.RunID).Scan(&count); err != nil || count != 2 || selectionReads.Load() != 2 {
		t.Fatal("real generation bypassed managed selection/acceptance", count, selectionReads.Load(), err)
	}
	if err = f.db.QueryRow(`SELECT count(*) FROM rsi_call WHERE run_id=? AND instance_id='context-compact'`, current.RunID).Scan(&count); err != nil || count != 0 {
		t.Fatal("pure selection created opaque pending state", count, err)
	}
	if len(requests) != 2 || len(requests[1].Messages) <= len(requests[0].Messages) || !reflect.DeepEqual(requests[0].Messages, requests[1].Messages[:len(requests[0].Messages)]) {
		t.Fatal("tool continuation changed frozen prefix")
	}
	var snapshot string
	if err = f.db.QueryRow(`SELECT managed_services_json FROM rsi_run WHERE run_id=?`, current.RunID).Scan(&snapshot); err != nil || !strings.Contains(snapshot, "steward.context-selection/1") {
		t.Fatal("missing immutable service boundary", snapshot, err)
	}
	p, err := f.host.Install(ctx, contextPackageVersion(t, "1.1.0"))
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
		if next.Bindings[i].InstanceID == "context-compact" {
			next.Bindings[i].PackageDigest = p.Digest
		}
	}
	second, err := f.host.Prepare(ctx, next)
	if err != nil {
		t.Fatal(err)
	}
	var policy string
	if err = f.db.QueryRow(`SELECT policy_id FROM rsi_candidate WHERE target=?`, first.Revision).Scan(&policy); err != nil {
		t.Fatal(err)
	}
	selectVersion := func(id, baseline, target string) {
		t.Helper()
		if err := f.host.Propose(ctx, rsi.Candidate{ID: id, Baseline: baseline, Target: target, PolicyID: policy, Goal: "context strategy evolution"}); err != nil {
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

	originalPolicy := policy
	independent, err := f.host.Store.Policy(ctx, policy)
	if err != nil {
		t.Fatal(err)
	}
	independent.ID = "context-owner-independent-method"
	for i := range independent.Cases {
		if independent.Cases[i].Contract != contract.Contract {
			continue
		}
		var plan contract.Plan
		if err := json.Unmarshal(independent.Cases[i].Expected, &plan); err != nil {
			t.Fatal(err)
		}
		if plan.Instructions != "" {
			plan.Instructions += "INDEPENDENT_COMPACT_METHOD"
		}
		independent.Cases[i].Expected, _ = json.Marshal(plan)
	}
	if err = f.host.Store.PutPolicy(ctx, independent); err != nil {
		t.Fatal(err)
	}
	checkMethod := func(revision string, changed bool) {
		t.Helper()
		facts := contract.Facts{ModelBytes: 5000, FrameBytes: 5000, Recent: 2, SummaryBytes: 512, ImageLimit: 8}
		facts.Messages = append(facts.Messages, contract.Message{Role: "system", Protected: true, Full: contract.Cost{Model: 100, Frame: 100}, Images: []contract.Image{}})
		for i := 0; i < 12; i++ {
			facts.Messages = append(facts.Messages, contract.Message{Role: "assistant", Full: contract.Cost{Model: 1000, Frame: 1000}, Images: []contract.Image{}})
		}
		facts.Messages = append(facts.Messages, contract.Message{Role: "user", Input: true, Protected: true, Full: contract.Cost{Model: 100, Frame: 100}, Images: []contract.Image{}})
		raw, _ := json.Marshal(facts)
		out, err := f.host.ReadService(ctx, revision, contract.Contract, json.RawMessage(`{"input_id":"facts"}`), []rsi.InputData{{ID: "facts", Data: raw}})
		if err != nil {
			t.Fatal(err)
		}
		var plan contract.Plan
		if err = rsiplugin.DecodeResult(out, &plan); err != nil || plan.Through == 0 || strings.Contains(plan.Instructions, "INDEPENDENT_COMPACT_METHOD") != changed {
			t.Fatal("independent method not dispatched", string(out), err)
		}
	}
	checkMethod(first.Revision, false)
	policy = independent.ID
	selectVersion("context-upgrade", first.Revision, second.Revision)
	checkMethod(second.Revision, true)
	checkMethod(first.Revision, false)
	bound, err := f.host.Store.Binding(ctx, current.RunID)
	if err != nil || bound != first.Revision {
		t.Fatal("upgrade rebound old run", bound, err)
	}
	upgraded, err := f.actor.Submit(ctx, "context-upgraded", "new input")
	if err != nil {
		t.Fatal(err)
	}
	if err = f.actor.Drain(ctx); err != nil {
		t.Fatal(err)
	}
	bound, err = f.host.Store.Binding(ctx, upgraded.RunID)
	if err != nil || bound != second.Revision {
		t.Fatal("new execution did not use upgrade", bound, err)
	}
	retained, err := f.actor.Submit(ctx, "context-retained-cold", "retained input")
	if err != nil {
		t.Fatal(err)
	}
	policy = originalPolicy
	selectVersion("context-rollback", second.Revision, first.Revision)
	checkMethod(first.Revision, false)
	if err = f.host.Store.Retire(ctx, p.Digest); err != nil {
		t.Fatal(err)
	}

	cold := func() {
		t.Helper()
		respond := f.actor.Provider.(recordingProvider).respond
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
		f.store = store
		f.runs = store.Runs(history, f.actor.Registry)
		f.runs.ConfigurePlugins(catalog)
		f.host = rsi.Host{Catalog: catalog, Store: store.Plugins()}
		f.actor.Store = f.runs
		f.actor.Provider = recordingProvider{store: store, respond: respond}
	}
	cold()
	if err = f.actor.Drain(ctx); err != nil {
		t.Fatal("retired queued execution did not recover", err)
	}
	bound, err = f.host.Store.Binding(ctx, retained.RunID)
	if err != nil || bound != second.Revision {
		t.Fatal("cold recovery replaced original graph", bound, err)
	}
	if err = f.db.QueryRow(`SELECT count(*) FROM context_compaction WHERE run_id=?`, retained.RunID).Scan(&count); err != nil || count == 0 {
		t.Fatal("cold execution bypassed selection", count, err)
	}
	checkMethod(second.Revision, true)
	if _, err = f.host.Store.Deactivate(ctx, "context-disable", first.Revision); err != nil {
		t.Fatal(err)
	}
	disabled, err := f.actor.Submit(ctx, "context-disabled", "disabled must not resurrect native selection")
	if err != nil {
		t.Fatal(err)
	}
	beforeCalls := calls
	cold()
	if err = f.actor.Drain(ctx); !errors.Is(err, llm.ProviderUnavailable) {
		t.Fatal("missing explicit unavailable outcome", err)
	}
	if calls != beforeCalls {
		t.Fatal("disabled feature sent model request")
	}
	var status string
	if err = f.db.QueryRow(`SELECT status FROM run WHERE run_id=?`, disabled.RunID).Scan(&status); err != nil || status != "failed" {
		t.Fatal("disabled feature fell back to native", status, err)
	}
	if err = f.db.QueryRow(`SELECT managed_services_json FROM rsi_run WHERE run_id=?`, disabled.RunID).Scan(&snapshot); err != nil || !strings.Contains(snapshot, "steward.context-selection/1") {
		t.Fatal("disable erased migration boundary", snapshot, err)
	}
}

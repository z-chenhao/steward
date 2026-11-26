package sqlite

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"

	"github.com/z-chenhao/assistant/internal/computer/sandbox"
	"github.com/z-chenhao/assistant/internal/plugin/contextcompact/contract"
	"github.com/z-chenhao/assistant/internal/rsi"
	"github.com/z-chenhao/assistant/pkg/rsiplugin"
)

// Only the immutable Host executable is shared. Every test still independently
// builds its candidate, seals/installs/verifies its graph and owns its data,
// processes and private state. Re-linking the same Host for every fixture made
// the growing package suite approach Go's default ten-minute timeout.
func runtimeFeatureHost(t *testing.T) string {
	t.Helper()
	path, err := ensureCachedTestBinary("../../../cmd/steward")
	if err != nil {
		t.Fatal(err)
	}
	return path
}

func TestMain(m *testing.M) {
	code := m.Run()
	if err := cleanupTestBinaryCache(); err != nil {
		fmt.Fprintln(os.Stderr, "remove fixture binaries:", err)
		code = 1
	}
	os.Exit(code)
}

func installContextPlugin(t *testing.T, s *Store, r *Runs) rsi.Host {
	t.Helper()
	facts, _ := json.Marshal(contract.Facts{Messages: []contract.Message{{Role: "user", Protected: true, Input: true, Full: contract.Cost{Model: 200, Frame: 200}, Images: []contract.Image{}}}, ModelBytes: 5000, FrameBytes: 5000, Recent: 6, SummaryBytes: 2048, ImageLimit: 8})
	policy := rsi.Policy{ID: "context-owner-fixture", AllowedContracts: []string{contract.Contract}, AllowedRequests: []string{"input.read"}, Cases: []rsi.Case{{InstanceID: "context-compact", Contract: contract.Contract, Input: json.RawMessage(`{"input_id":"facts"}`), Inputs: []rsi.InputData{{ID: "facts", Data: facts}}, Expected: json.RawMessage(`{"images":[[]],"summarize_through":0}`)}}}
	return installRuntimeFeature(t, s, r, "context-compact", policy)
}

func installRuntimeFeature(t *testing.T, s *Store, r *Runs, name string, policy rsi.Policy, config ...json.RawMessage) rsi.Host {
	t.Helper()
	source := t.TempDir()
	buildTestBinary(t, "../../../cmd/plugins/"+name, filepath.Join(source, "plugin"))
	raw, err := os.ReadFile("../../../plugins/" + name + "/steward.plugin.json")
	if err != nil {
		t.Fatal(err)
	}
	var manifest rsiplugin.Manifest
	if err = rsiplugin.Decode(raw, &manifest); err != nil {
		t.Fatal(err)
	}
	manifest, err = rsi.Seal(source, manifest)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ = json.Marshal(manifest)
	if err = os.WriteFile(filepath.Join(source, "steward.plugin.json"), raw, 0600); err != nil {
		t.Fatal(err)
	}
	return installRuntimePackage(t, s, r, name, policy, source, config...)
}

func installRuntimePackage(t *testing.T, s *Store, r *Runs, name string, policy rsi.Policy, source string, config ...json.RawMessage) rsi.Host {
	t.Helper()
	if err := sandbox.Check(); err != nil {
		t.Skip("kernel sandbox unavailable:", err)
	}
	ctx := context.Background()
	carrier := runtimeFeatureHost(t)
	catalog := rsi.NewCatalog(filepath.Dir(s.path))
	catalog.Launcher = carrier
	r.ConfigurePlugins(catalog)
	h := rsi.Host{Catalog: catalog, Store: s.Plugins()}
	p, err := h.Install(ctx, source)
	if err != nil {
		t.Fatal(err)
	}
	bindingConfig := json.RawMessage(`{}`)
	if len(config) == 1 {
		bindingConfig = config[0]
	}
	if len(config) > 1 {
		t.Fatal("one binding config expected")
	}
	g, err := h.Prepare(ctx, rsiplugin.Graph{Protocol: rsiplugin.Protocol, Bindings: []rsiplugin.Binding{{InstanceID: name, PackageDigest: p.Digest, Config: bindingConfig, StateNamespace: name, StateVersion: 1}}})
	if err != nil {
		t.Fatal(err)
	}

	if err = h.Store.PutPolicy(ctx, policy); err != nil {
		t.Fatal(err)
	}
	if err = h.Propose(ctx, rsi.Candidate{ID: policy.ID, Target: g.Revision, PolicyID: policy.ID, Goal: "test actual " + name + " pipeline"}); err != nil {
		t.Fatal(err)
	}
	decision, err := h.Verify(ctx, policy.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = h.Activate(ctx, rsi.Activation{OperationID: policy.ID, Target: g.Revision, DecisionID: decision}); err != nil {
		t.Fatal(err)
	}
	return h
}

func TestSemanticCompactionPluginRecoversAcceptedSummaryWithoutToolReplay(t *testing.T) {
	testSemanticCompactionRecovery(t, true)
}

func TestContextPluginChildInheritsFrozenFeatureSnapshot(t *testing.T) {
	s, r, a, p := childFixture(t)
	h := installContextPlugin(t, s, r)
	var selectionReads atomic.Int64
	s.MainBoundary = func(point string) {
		if point == "after_context_policy_read" {
			selectionReads.Add(1)
		}
	}
	defer func() { s.MainBoundary = nil }()
	p.respond = childPolicy
	mustSubmit(t, a, "plugin-child-tree", "perform delegated task")
	mustDrain(t, a)
	if got := scalar(t, s, `SELECT count(*) FROM run c JOIN rsi_run cb ON cb.run_id=c.run_id JOIN rsi_run pb ON pb.run_id=c.parent_run_id WHERE c.parent_run_id IS NOT NULL AND cb.graph_revision=pb.graph_revision AND cb.managed_services_json=pb.managed_services_json AND EXISTS(SELECT 1 FROM json_each(cb.managed_services_json) WHERE value='steward.context-selection/1')`); got != 2 {
		t.Fatal("Child/grandchild migration snapshot drift", got)
	}
	status, err := h.Store.Status(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if status.Revision == "" {
		t.Fatal("missing activated fixture")
	}
	if got := scalar(t, s, `SELECT count(*) FROM run r WHERE parent_run_id IS NOT NULL AND NOT EXISTS(SELECT 1 FROM context_compaction c WHERE c.run_id=r.run_id)`); got != 0 {
		t.Fatal("Child lost accepted frozen context", got)
	}
	if selectionReads.Load() < 2 {
		t.Fatal("Child bypassed real pure selector", selectionReads.Load())
	}
}

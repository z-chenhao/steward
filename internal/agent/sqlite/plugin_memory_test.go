package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/z-chenhao/assistant/internal/llm"
	"github.com/z-chenhao/assistant/internal/memory"
	"github.com/z-chenhao/assistant/internal/plugin/memorycurator/contract"
	"github.com/z-chenhao/assistant/internal/recall"
	"github.com/z-chenhao/assistant/internal/rsi"
	"github.com/z-chenhao/assistant/pkg/rsiplugin"
)

func installCuratorPlugin(t *testing.T, s *Store, r *Runs) rsi.Host {
	t.Helper()
	policy := rsi.Policy{ID: "curator-owner-fixture", AllowedContracts: []string{contract.Contract, contract.ScheduleContract}}
	for _, test := range []struct{ Style, Instructions string }{{"facts", memory.CurationInstruction}, {"dream", memory.DreamInstruction}, {"grounded", memory.GroundedDreamInstruction}, {"worker", memory.WorkerDreamInstruction}} {
		input, _ := json.Marshal(contract.Request{Style: test.Style})
		expected, _ := json.Marshal(contract.Policy{Instructions: test.Instructions})
		policy.Cases = append(policy.Cases, rsi.Case{InstanceID: "memory-curator", Contract: contract.Contract, Input: input, Expected: expected})
	}
	policy.Cases = append(policy.Cases, rsi.Case{InstanceID: "memory-curator", Contract: contract.ScheduleContract, Input: json.RawMessage(`{}`), Expected: json.RawMessage(`{"min_changes":8,"max_wait_seconds":300}`)})
	return installRuntimeFeature(t, s, r, "memory-curator", policy)
}

func TestSemanticCuratorPluginAcceptedReplayAndNoUserTask(t *testing.T) {
	testSemanticRuntimeAcceptedReplay(t, true)
}

func TestSemanticCuratorPluginPreservesLegacyIntentAndSealsPolicyProvenance(t *testing.T) {
	ctx := context.Background()
	s, r, _, a, p := semanticRuntime(t)
	mustSubmit(t, a, "curator-legacy", "我喜欢无糖绿茶")
	if err := r.Flush(ctx); err != nil {
		t.Fatal(err)
	}
	h := installCuratorPlugin(t, s, r)
	status, err := h.Store.Status(ctx)
	if err != nil {
		t.Fatal(err)
	}
	systems := []string{}
	p.beforeReply = func(_ context.Context, req llm.GenerationRequest) { systems = append(systems, req.Messages[0].Text) }
	if err = r.CurateMemory(ctx); err != nil {
		t.Fatal(err)
	}
	if len(systems) != 1 || systems[0] != memory.CurationInstruction {
		t.Fatal("old intent retroactively changed method", systems)
	}
	mustSubmit(t, a, "curator-managed", "I prefer green tea without sugar")
	if err = r.Flush(ctx); err != nil {
		t.Fatal(err)
	}
	if err = r.CurateMemory(ctx); err != nil {
		t.Fatal(err)
	}
	if len(systems) != 2 || systems[1] != memoryPolicySafety+"\n\n"+memory.CurationInstruction {
		t.Fatal("new source bypassed actual independent policy", systems)
	}
	rows, err := s.db.Query(`SELECT b.managed,b.graph_revision FROM memory_policy_binding b JOIN memory_mutation m USING(event_id) WHERE m.operation='observe' AND m.source_type='conversation' ORDER BY m.sequence`)
	if err != nil {
		t.Fatal(err)
	}
	var modes []bool
	var revisions []sql.NullString
	for rows.Next() {
		var managed bool
		var revision sql.NullString
		if err = rows.Scan(&managed, &revision); err != nil {
			t.Fatal(err)
		}
		modes = append(modes, managed)
		revisions = append(revisions, revision)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		t.Fatal(err)
	}
	if len(modes) != 2 || modes[0] || !modes[1] || revisions[0].Valid || revisions[1].String != status.Revision {
		t.Fatal("intent migration boundary changed", modes, revisions)
	}
	var generation, sealed string
	if err = s.db.QueryRow(`SELECT g.generation_id,g.request_sealed FROM memory_generation g JOIN memory_policy_binding b USING(event_id) WHERE b.managed=1`).Scan(&generation, &sealed); err != nil {
		t.Fatal(err)
	}
	plain, err := r.vault.Open(ctx, sealed, generation)
	if err != nil {
		t.Fatal(err)
	}
	var work curationWork
	if json.Unmarshal([]byte(plain), &work) != nil || work.PolicyRevision != status.Revision || work.Request.Messages[0].Text != systems[1] {
		t.Fatal("private request lost method provenance")
	}
	if strings.Contains(sealed, "我喜欢") || strings.Contains(sealed, memoryPolicySafety) {
		t.Fatal("private request persisted in plaintext")
	}
	if _, err = s.db.Exec(`UPDATE memory_policy_binding SET graph_revision=NULL WHERE managed=1`); err == nil {
		t.Fatal("accepted intent lost pinned implementation")
	}
}

func TestSemanticCuratorPluginPausedIntentResumesExplicitSelectionAndQuarantine(t *testing.T) {
	ctx := context.Background()
	s, r, _, a, p := semanticRuntime(t)
	h := installCuratorPlugin(t, s, r)
	status, err := h.Store.Status(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = h.Store.Deactivate(ctx, "curator-disable", status.Revision); err != nil {
		t.Fatal(err)
	}
	mustSubmit(t, a, "curator-paused", "我喜欢无糖绿茶")
	if err = r.Flush(ctx); err != nil {
		t.Fatal(err)
	}
	if err = r.CurateMemory(ctx); !errors.Is(err, recall.ErrUnavailable) {
		t.Fatal("disabled method fell back to native", err)
	}
	if p.calls != 0 || scalar(t, s, `SELECT count(*) FROM memory_policy_binding WHERE managed=1 AND graph_revision IS NULL`) == 0 {
		t.Fatal("paused intent lost or model contacted")
	}
	if err = h.Propose(ctx, rsi.Candidate{ID: "curator-resume", Target: status.Revision, PolicyID: "curator-owner-fixture", Goal: "resume retained private intent with an explicit verified selection"}); err != nil {
		t.Fatal(err)
	}
	decision, err := h.Verify(ctx, "curator-resume")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = h.Activate(ctx, rsi.Activation{OperationID: "curator-resume", Target: status.Revision, DecisionID: decision}); err != nil {
		t.Fatal(err)
	}
	if err = r.CurateMemory(ctx); err != nil {
		t.Fatal(err)
	}
	if p.calls != 1 || scalar(t, s, `SELECT count(*) FROM memory_policy_binding WHERE managed=1 AND graph_revision=?`, status.Revision) != 1 {
		t.Fatal("retained intent did not bind exactly once", p.calls)
	}
	if err = h.Store.Quarantine(ctx, status.Revision); err != nil {
		t.Fatal(err)
	}
	mustSubmit(t, a, "curator-quarantined", "I prefer green tea without sugar")
	if err = r.Flush(ctx); err != nil {
		t.Fatal(err)
	}
	if err = r.CurateMemory(ctx); !errors.Is(err, recall.ErrUnavailable) {
		t.Fatal("quarantined method dispatched", err)
	}
	if p.calls != 1 {
		t.Fatal("quarantine contacted model")
	}
	var owner string
	if err = s.db.QueryRow(`SELECT agent_id FROM agent_config`).Scan(&owner); err != nil {
		t.Fatal(err)
	}
	if err = r.write(ctx, func(tx *sql.Tx) error { return r.revokeMemory(ctx, tx, owner) }); err != nil {
		t.Fatal(err)
	}
	if err = r.CurateMemory(ctx); err != nil {
		t.Fatal("Forget could not retire paused private intent", err)
	}
	if p.calls != 1 {
		t.Fatal("revoked source reached model")
	}
}

func TestSemanticCuratorPluginRejectsForgedEvidence(t *testing.T) {
	testSemanticRuntimeInvalid(t, true)
}
func TestSemanticCuratorPluginForgetBeforeAcceptanceAndDispatch(t *testing.T) {
	testSemanticRuntimeForget(t, true)
}

func curatorVersionPackage(t *testing.T, version string) string {
	t.Helper()
	source := t.TempDir()
	path, err := filepath.Abs("../../plugin/memorycurator/plugin.go")
	if err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	code := strings.Replace(string(raw), `Version: "1.0.1"`, `Version: "`+version+`"`, 1)
	code = strings.Replace(code, `strings.TrimSuffix(text, "\n")`, `strings.TrimSuffix(text, "\n")+"\nINDEPENDENT_CURATOR_METHOD_11"`, 1)
	if version == "1.2.0" {
		code = strings.Replace(code, "MinChanges: 8", "MinChanges: 1", 1)
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
	if out, err := exec.Command("go", "build", "-overlay", file, "-o", filepath.Join(source, "plugin"), "../../../cmd/plugins/memory-curator").CombinedOutput(); err != nil {
		t.Fatal(string(out), err)
	}
	raw, err = os.ReadFile("../../../plugins/memory-curator/steward.plugin.json")
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

func TestSemanticCuratorPluginIndependentUpgradePinsIntentAndAcceptedReplay(t *testing.T) {
	ctx := context.Background()
	s, r, _, a, p := semanticRuntime(t)
	h := installCuratorPlugin(t, s, r)
	first, err := h.Store.Status(ctx)
	if err != nil {
		t.Fatal(err)
	}
	mustSubmit(t, a, "curator-pinned", "我喜欢无糖绿茶")
	if err = r.Flush(ctx); err != nil {
		t.Fatal(err)
	}
	nextPackage, err := h.Install(ctx, curatorVersionPackage(t, "1.1.0"))
	if err != nil {
		t.Fatal(err)
	}
	original, err := h.Store.Graph(ctx, first.Revision)
	if err != nil {
		t.Fatal(err)
	}
	next := original.Graph
	next.Bindings = append([]rsiplugin.Binding(nil), next.Bindings...)
	next.Bindings[0].PackageDigest = nextPackage.Digest
	second, err := h.Prepare(ctx, next)
	if err != nil {
		t.Fatal(err)
	}
	policy, err := h.Store.Policy(ctx, "curator-owner-fixture")
	if err != nil {
		t.Fatal(err)
	}
	policy.ID = "curator-owner-method-11"
	for i := range policy.Cases {
		if policy.Cases[i].Contract != contract.Contract {
			continue
		}
		var expected contract.Policy
		if json.Unmarshal(policy.Cases[i].Expected, &expected) != nil {
			t.Fatal("bad independent fixture")
		}
		expected.Instructions += "\nINDEPENDENT_CURATOR_METHOD_11"
		policy.Cases[i].Expected, _ = json.Marshal(expected)
	}
	if err = h.Store.PutPolicy(ctx, policy); err != nil {
		t.Fatal(err)
	}
	selectVersion := func(id, baseline, target, policy string) {
		t.Helper()
		if err := h.Propose(ctx, rsi.Candidate{ID: id, Baseline: baseline, Target: target, PolicyID: policy, Goal: "independent private maintenance method evolution"}); err != nil {
			t.Fatal(err)
		}
		decision, err := h.Verify(ctx, id)
		if err != nil {
			t.Fatal(err)
		}
		if _, err = h.Activate(ctx, rsi.Activation{OperationID: id, Expected: baseline, Target: target, DecisionID: decision}); err != nil {
			t.Fatal(err)
		}
	}
	selectVersion("curator-upgrade", first.Revision, second.Revision, policy.ID)
	systems := []string{}
	p.beforeReply = func(_ context.Context, req llm.GenerationRequest) { systems = append(systems, req.Messages[0].Text) }
	if err = r.CurateMemory(ctx); err != nil {
		t.Fatal(err)
	}
	if len(systems) != 1 || strings.Contains(systems[0], "INDEPENDENT_CURATOR_METHOD_11") {
		t.Fatal("queued intent changed accepted version")
	}
	mustSubmit(t, a, "curator-new-method", "I prefer green tea without sugar")
	if err = r.Flush(ctx); err != nil {
		t.Fatal(err)
	}
	if err = r.CurateMemory(ctx); err != nil {
		t.Fatal(err)
	}
	if len(systems) != 2 || !strings.Contains(systems[1], "INDEPENDENT_CURATOR_METHOD_11") || !strings.HasPrefix(systems[1], memoryPolicySafety) {
		t.Fatal("actual private model request did not use independent method")
	}
	selectVersion("curator-rollback", second.Revision, first.Revision, "curator-owner-fixture")
	if err = h.Store.Retire(ctx, nextPackage.Digest); err != nil {
		t.Fatal(err)
	}
	if _, err = h.Catalog.Load(nextPackage.Digest); err != nil {
		t.Fatal("old private implementation lost", err)
	}
	// Accepted encrypted requests/results are data receipts. Replaying them after
	// a cross-DB interruption does not invoke a now-quarantined method again.
	if err = h.Store.Quarantine(ctx, first.Revision); err != nil {
		t.Fatal(err)
	}
	if err = h.Store.Quarantine(ctx, second.Revision); err != nil {
		t.Fatal(err)
	}
	if _, err = s.db.Exec(`DELETE FROM memory_semantic_receipt`); err != nil {
		t.Fatal(err)
	}
	if err = r.CurateMemory(ctx); err != nil {
		t.Fatal("accepted private work required reinterpretation", err)
	}
	if p.calls != 2 {
		t.Fatal("accepted result re-inferred", p.calls)
	}
}

func TestSemanticCuratorPluginDreamRecoversAllRepresentations(t *testing.T) {
	testLayeredMemoryDreamRecovery(t, true)
}
func TestSemanticCuratorPluginDreamPagesLongUTF8WithoutReplay(t *testing.T) {
	testLayeredMemoryDreamLongTranscript(t, true)
}

func TestSemanticCuratorPluginIndependentlyChangesBoundedDreamCadence(t *testing.T) {
	ctx := context.Background()
	s, r, _, a, p := semanticRuntime(t)
	h := installCuratorPlugin(t, s, r)
	first, err := h.Store.Status(ctx)
	if err != nil {
		t.Fatal(err)
	}
	p.reply = func(in memory.CurationInput) llm.GenerationResult {
		if in.Mode != "dream" {
			t.Fatal("ordinary research request became a lasting fact")
		}
		return llm.GenerationResult{Text: `{"facts":[],"documents":[],"done":true}`}
	}
	mustSubmit(t, a, "curator-cadence", "请你调研这个问题")
	if err = r.Flush(ctx); err != nil {
		t.Fatal(err)
	}
	if err = r.CurateMemory(ctx); err != nil {
		t.Fatal(err)
	}
	if p.calls != 0 {
		t.Fatal("default cadence prematurely inferred", p.calls)
	}
	pkg, err := h.Install(ctx, curatorVersionPackage(t, "1.2.0"))
	if err != nil {
		t.Fatal(err)
	}
	old, err := h.Store.Graph(ctx, first.Revision)
	if err != nil {
		t.Fatal(err)
	}
	graph := old.Graph
	graph.Bindings = append([]rsiplugin.Binding(nil), graph.Bindings...)
	graph.Bindings[0].PackageDigest = pkg.Digest
	next, err := h.Prepare(ctx, graph)
	if err != nil {
		t.Fatal(err)
	}
	policy, err := h.Store.Policy(ctx, "curator-owner-fixture")
	if err != nil {
		t.Fatal(err)
	}
	policy.ID = "curator-owner-cadence-12"
	for i := range policy.Cases {
		if policy.Cases[i].Contract == contract.Contract {
			var expected contract.Policy
			_ = json.Unmarshal(policy.Cases[i].Expected, &expected)
			expected.Instructions += "\nINDEPENDENT_CURATOR_METHOD_11"
			policy.Cases[i].Expected, _ = json.Marshal(expected)
		} else {
			policy.Cases[i].Expected = json.RawMessage(`{"min_changes":1,"max_wait_seconds":300}`)
		}
	}
	if err = h.Store.PutPolicy(ctx, policy); err != nil {
		t.Fatal(err)
	}
	if err = h.Propose(ctx, rsi.Candidate{ID: "curator-cadence", Baseline: first.Revision, Target: next.Revision, PolicyID: policy.ID, Goal: "independently tune cadence within Host bounds"}); err != nil {
		t.Fatal(err)
	}
	decision, err := h.Verify(ctx, "curator-cadence")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = h.Activate(ctx, rsi.Activation{OperationID: "curator-cadence", Expected: first.Revision, Target: next.Revision, DecisionID: decision}); err != nil {
		t.Fatal(err)
	}
	p.beforeReply = func(_ context.Context, req llm.GenerationRequest) {
		if !strings.HasPrefix(req.Messages[0].Text, memoryPolicySafety) || !strings.Contains(req.Messages[0].Text, "INDEPENDENT_CURATOR_METHOD_11") {
			t.Fatal("cadence mixed method versions")
		}
	}
	if err = r.CurateMemory(ctx); err != nil {
		t.Fatal(err)
	}
	if p.calls != 1 || scalar(t, s, `SELECT count(*) FROM memory_generation WHERE purpose='dream' AND status='completed'`) != 1 || scalar(t, s, `SELECT count(*) FROM task`) != 0 {
		t.Fatal("new cadence failed or created user work", p.calls)
	}
	if err = r.CurateMemory(ctx); err != nil || p.calls != 1 {
		t.Fatal("small cadence re-inferred unchanged corpus", p.calls, err)
	}
}

func TestSemanticCuratorScheduleMissingRunnerCannotRestoreNative(t *testing.T) {
	ctx := context.Background()
	s, r, _, _, _ := semanticRuntime(t)
	// Never-managed embeddings retain the original recipe.
	if _, _, enabled, err := r.dreamSchedulePolicy(ctx); err != nil || !enabled {
		t.Fatal("never-managed cadence unavailable", enabled, err)
	}
	h := installCuratorPlugin(t, s, r)
	status, err := h.Store.Status(ctx)
	if err != nil {
		t.Fatal(err)
	}
	r.plugins = nil
	if _, _, enabled, err := r.dreamSchedulePolicy(ctx); enabled || !errors.Is(err, rsi.ErrNotFound) {
		t.Fatal("missing runner restored native cadence", enabled, err)
	}
	if _, err = h.Store.Deactivate(ctx, "missing-runner-disable", status.Revision); err != nil {
		t.Fatal(err)
	}
	if _, _, enabled, err := r.dreamSchedulePolicy(ctx); enabled || !errors.Is(err, rsi.ErrNotFound) {
		t.Fatal("deactivated cadence restored without runner", enabled, err)
	}
}

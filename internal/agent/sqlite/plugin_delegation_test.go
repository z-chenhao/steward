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
	"time"

	"github.com/z-chenhao/assistant/internal/agent"
	conversationsqlite "github.com/z-chenhao/assistant/internal/conversation/sqlite"
	"github.com/z-chenhao/assistant/internal/plugin/delegation/contract"
	"github.com/z-chenhao/assistant/internal/rsi"
	"github.com/z-chenhao/assistant/pkg/rsiplugin"
	"testing"
)

// Reopen the persisted runtime without selecting a graph, repairing bindings,
// or touching queued work. The first subsequent action must be the real drain.
func reopenMethodRuntime(t *testing.T, s *Store, r *Runs, a agent.Actor, p *loopProvider) (*Store, *Runs, agent.Actor) {
	t.Helper()
	root := filepath.Dir(s.path)
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := Open(context.Background(), root)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { reopened.Close() })
	history, err := conversationsqlite.Open(context.Background(), root)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { history.Close() })
	next := reopened.Runs(history, a.Registry)
	catalog := rsi.NewCatalog(root)
	// Keep the same executable entry point; only persisted data can restore policy.
	catalog.Launcher = r.plugins.Catalog.Launcher
	next.ConfigurePlugins(catalog)
	a.Store, p.store = next, reopened
	return reopened, next, a
}

func installDelegationPlugin(t *testing.T, s *Store, r *Runs) rsi.Host {
	t.Helper()
	return installRuntimeFeature(t, s, r, "delegation", rsi.Policy{ID: "delegation-owner-fixture", AllowedContracts: []string{contract.Contract}, Cases: []rsi.Case{{InstanceID: "delegation", Contract: contract.Contract, Input: json.RawMessage(`{"depth":2,"descendants":3}`), Expected: json.RawMessage(`{"depth":2,"descendants":3,"instructions":"Delegation: pass a self-contained subtask and accessible Artifact references. A Child receives no Parent conversation. Aggregate its final result; delegation never creates a Task."}`)}}})
}
func TestDelegationPluginActualRootChildGrandchildAndFrozenCeilings(t *testing.T) {
	s, r, a, p := childFixture(t)
	installDelegationPlugin(t, s, r)
	p.respond = childPolicy
	mustSubmit(t, a, "managed-tree", "Main private conversation")
	mustDrain(t, a)
	if scalar(t, s, `SELECT count(*) FROM run WHERE worker_position='child' AND status='completed'`) != 2 || scalar(t, s, `SELECT count(*) FROM task`) != 1 {
		t.Fatal("delegation changed business lifecycle")
	}
	if scalar(t, s, `SELECT count(*) FROM delegation_policy_binding p JOIN run_tree t USING(root_run_id) JOIN rsi_run b ON b.run_id=p.root_run_id WHERE p.available=1 AND p.graph_revision=b.graph_revision AND p.depth=t.max_depth AND p.descendants=t.max_descendants`) != 1 {
		t.Fatal("method did not bind to frozen Host ceilings")
	}
	if scalar(t, s, `SELECT count(*) FROM rsi_call WHERE instance_id='delegation'`) != 0 {
		t.Fatal("pure method added another unknown effect ledger")
	}
	if scalar(t, s, `SELECT count(*) FROM run WHERE kind='worker' AND instr(checkpoint_json,'delegation_text')>0`) != 3 {
		t.Fatal("descendants lost frozen method provenance")
	}
	if err := r.Flush(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func delegationVersionPackage(t *testing.T, version string) string {
	t.Helper()
	source := t.TempDir()
	path, err := filepath.Abs("../../plugin/delegation/plugin.go")
	if err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	code := strings.Replace(string(raw), `Version: "1.0.1"`, `Version: "`+version+`"`, 1)
	if version == "1.1.0" {
		code = strings.Replace(code, `Depth: f.Depth, Descendants: f.Descendants`, `Depth: min(f.Depth,1), Descendants: min(f.Descendants,1)`, 1)
		code = strings.Replace(code, `Instructions: strings.TrimSuffix(instructions, "\n")`, `Instructions: strings.TrimSuffix(instructions, "\n")+" VERSION_1_1_DELEGATION."`, 1)
	} else if version == "1.2.0" {
		code = strings.Replace(code, `Depth: f.Depth, Descendants: f.Descendants`, `Depth: f.Depth+1, Descendants: f.Descendants+1`, 1)
	} else {
		code = strings.Replace(code, `"context"`, `"context";"os";"time"`, 1)
		code = strings.Replace(code, `return rsiplugin.JSONResult(ctx, contract.Policy`, `if f.Depth==8 {if err:=os.WriteFile("delegation-ready",[]byte("ready"),0600);err!=nil{return rsiplugin.Result{},err};for{if _,err:=os.Stat("resume");err==nil{break};select{case<-ctx.Done():return rsiplugin.Result{},ctx.Err();case<-time.After(time.Millisecond):}}};return rsiplugin.JSONResult(ctx, contract.Policy`, 1)
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
	if out, err := exec.Command("go", "build", "-overlay", file, "-o", filepath.Join(source, "plugin"), "../../../cmd/plugins/delegation").CombinedOutput(); err != nil {
		t.Fatal(string(out), err)
	}
	raw, err = os.ReadFile("../../../plugins/delegation/steward.plugin.json")
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

func activateDelegation(t *testing.T, h rsi.Host, id, baseline, target, policy string) {
	t.Helper()
	ctx := context.Background()
	if err := h.Propose(ctx, rsi.Candidate{ID: id, Baseline: baseline, Target: target, PolicyID: policy, Goal: "independent delegation methods beneath Host ceilings"}); err != nil {
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
func selectDelegationVersion(t *testing.T, h rsi.Host, baseline, version string) (string, string) {
	t.Helper()
	ctx := context.Background()
	pkg, err := h.Install(ctx, delegationVersionPackage(t, version))
	if err != nil {
		t.Fatal(err)
	}
	graph, err := h.Prepare(ctx, rsiplugin.Graph{Protocol: rsiplugin.Protocol, Bindings: []rsiplugin.Binding{{InstanceID: "delegation", PackageDigest: pkg.Digest, Config: json.RawMessage(`{}`), StateNamespace: "delegation", StateVersion: 1}}})
	if err != nil {
		t.Fatal(err)
	}
	policy, err := h.Store.Policy(ctx, "delegation-owner-fixture")
	if err != nil {
		t.Fatal(err)
	}
	policy.ID = "delegation-owner-" + version
	var expected contract.Policy
	if err = json.Unmarshal(policy.Cases[0].Expected, &expected); err != nil {
		t.Fatal(err)
	}
	if version == "1.1.0" {
		expected.Depth, expected.Descendants = 1, 1
		expected.Instructions += " VERSION_1_1_DELEGATION."
	} else if version == "1.2.0" {
		expected.Depth++
		expected.Descendants++
	}
	policy.Cases[0].Expected, _ = json.Marshal(expected)
	if err = h.Store.PutPolicy(ctx, policy); err != nil {
		t.Fatal(err)
	}
	activateDelegation(t, h, "delegation-upgrade-"+version, baseline, graph.Revision, policy.ID)
	return graph.Revision, pkg.Digest
}

func TestDelegationPluginIndependentUpgradeRetainedTreesRollbackAndDisable(t *testing.T) {
	ctx := context.Background()
	s, r, a, p := childFixture(t)
	r.ConfigurePlugins(rsi.NewCatalog(filepath.Dir(s.path)))
	p.respond = childPolicy
	queueRoot := func(key string) string {
		t.Helper()
		receipt := mustSubmit(t, a, key, "Delegate explicit work")
		if err := executeTaskRunID(t, a, r, receipt.RunID); err != nil {
			t.Fatal(err)
		}
		var id string
		if err := s.db.QueryRow(`SELECT run_id FROM run WHERE worker_position='root' AND status='queued' ORDER BY created_at DESC LIMIT 1`).Scan(&id); err != nil {
			t.Fatal(err)
		}
		return id
	}
	check := func(id string, children int, variant bool) {
		t.Helper()
		if scalar(t, s, `SELECT count(*) FROM run WHERE root_run_id=? AND worker_position='child'`, id) != children || scalar(t, s, `SELECT count(*) FROM run WHERE run_id=? AND status='completed'`, id) != 1 {
			t.Fatal("tree changed during independent method lifecycle", id, children)
		}
		got := scalar(t, s, `SELECT count(*) FROM run WHERE root_run_id=? AND instr(checkpoint_json,'VERSION_1_1_DELEGATION')>0`, id)
		if (variant && got != children+1) || (!variant && got != 0) {
			t.Fatal("Root and descendants lost selected method", id, got)
		}
		var depth, desc int
		if err := s.db.QueryRow(`SELECT max_depth,max_descendants FROM run_tree WHERE root_run_id=?`, id).Scan(&depth, &desc); err != nil || depth != 8 || desc != 4096 {
			t.Fatal("policy rewrote Host safety ceiling", depth, desc, err)
		}
	}
	legacy := queueRoot("delegation-native")
	h := installDelegationPlugin(t, s, r)
	first, err := h.Store.Status(ctx)
	if err != nil {
		t.Fatal(err)
	}
	mustDrain(t, a)
	check(legacy, 2, false)
	if scalar(t, s, `SELECT count(*) FROM delegation_policy_binding WHERE root_run_id=?`, legacy) != 0 {
		t.Fatal("historical tree retroactively acquired a policy")
	}
	old := queueRoot("delegation-old")
	second, pkg := selectDelegationVersion(t, h, first.Revision, "1.1.0")
	mustDrain(t, a)
	check(old, 2, false)
	current := queueRoot("delegation-new")
	mustDrain(t, a)
	check(current, 1, true)
	if scalar(t, s, `SELECT count(*) FROM delegation_policy_binding WHERE root_run_id=? AND depth=1 AND descendants=1 AND available=1`, current) != 1 {
		t.Fatal("new binary did not narrow delegation")
	}
	retained := queueRoot("delegation-retained")
	activateDelegation(t, h, "delegation-rollback", second, first.Revision, "delegation-owner-fixture")
	if err = h.Store.Retire(ctx, pkg); err != nil {
		t.Fatal(err)
	}
	s, r, a = reopenMethodRuntime(t, s, r, a, p)
	h = *r.plugins
	mustDrain(t, a)
	check(retained, 1, true)
	rolled := queueRoot("delegation-rolled")
	mustDrain(t, a)
	check(rolled, 2, false)
	if _, err = h.Store.Deactivate(ctx, "delegation-disable", first.Revision); err != nil {
		t.Fatal(err)
	}
	s, r, a = reopenMethodRuntime(t, s, r, a, p)
	disabled := queueRoot("delegation-disabled")
	mustDrain(t, a)
	check(disabled, 0, false)
	if scalar(t, s, `SELECT count(*) FROM delegation_policy_binding WHERE root_run_id=? AND graph_revision IS NULL AND available=0 AND depth=0 AND descendants=0`, disabled) != 1 || scalar(t, s, `SELECT count(*) FROM tool_invocation WHERE run_id=? AND json_extract(result_json,'$.error.code')='PROVIDER_UNAVAILABLE'`, disabled) != 1 {
		t.Fatal("missing managed method restored native Child admission")
	}
}

func TestDelegationPluginInvalidCeilingsFailOnlyTheRootBeforeModelWork(t *testing.T) {
	ctx := context.Background()
	s, r, a, p := childFixture(t)
	h := installDelegationPlugin(t, s, r)
	p.respond = childPolicy
	first, err := h.Store.Status(ctx)
	if err != nil {
		t.Fatal(err)
	}
	selectDelegationVersion(t, h, first.Revision, "1.2.0")
	mustSubmit(t, a, "delegation-invalid", "Delegate this explicit work")
	mustDrain(t, a)
	if scalar(t, s, `SELECT count(*) FROM run WHERE worker_position='root' AND status='failed' AND error_code='MODEL_INCOMPATIBLE'`) != 1 || scalar(t, s, `SELECT count(*) FROM task WHERE status='active'`) != 1 || scalar(t, s, `SELECT count(*) FROM run WHERE worker_position='child'`) != 0 || scalar(t, s, `SELECT count(*) FROM delegation_policy_binding`) != 0 || scalar(t, s, `SELECT count(*) FROM model_generation g JOIN run r USING(run_id) WHERE r.kind='worker'`) != 0 {
		t.Fatal("untrusted method expanded safety or kept retrying failed initialization")
	}
	before := scalar(t, s, `SELECT count(*) FROM run`)
	mustDrain(t, a)
	if scalar(t, s, `SELECT count(*) FROM run`) != before {
		t.Fatal("invalid method hot-retried a failed Root")
	}
}

func TestDelegationPluginMethodAndInitializedContextCommitTogether(t *testing.T) {
	ctx := context.Background()
	s, r, a, p := childFixture(t)
	installDelegationPlugin(t, s, r)
	p.respond = childPolicy
	main := mustSubmit(t, a, "delegation-initialize", "Delegate explicit work")
	if err := executeTaskRunID(t, a, r, main.RunID); err != nil {
		t.Fatal(err)
	}
	c := rootClaim(t, s, r)
	if _, err := s.db.Exec(`CREATE TRIGGER reject_delegation_policy BEFORE INSERT ON delegation_policy_binding BEGIN SELECT RAISE(ABORT,'injected method binding failure'); END`); err != nil {
		t.Fatal(err)
	}
	if err := r.Initialize(ctx, c); !errors.Is(err, agent.ErrStorage) {
		t.Fatal("initialization binding failure not exercised", err)
	}
	if scalar(t, s, `SELECT count(*) FROM run WHERE run_id=? AND json_extract(checkpoint_json,'$.context_ready')=1`, c.RunID()) != 0 || scalar(t, s, `SELECT count(*) FROM delegation_policy_binding`) != 0 || scalar(t, s, `SELECT count(*) FROM model_generation WHERE run_id=?`, c.RunID()) != 0 {
		t.Fatal("context, method or work partially committed")
	}
	if _, err := s.db.Exec(`DROP TRIGGER reject_delegation_policy`); err != nil {
		t.Fatal(err)
	}
	if err := r.Release(ctx, c); err != nil {
		t.Fatal(err)
	}
	next, err := r.Claim(ctx, c.RunID())
	if err != nil {
		t.Fatal(err)
	}
	if err = r.Initialize(ctx, next); err != nil {
		t.Fatal(err)
	}
	if err = r.Initialize(ctx, c); !errors.Is(err, agent.ErrClaimLost) {
		t.Fatal("old owner replaced initialized method", err)
	}
	if err = r.Release(ctx, next); err != nil {
		t.Fatal(err)
	}
	mustDrain(t, a)
	if scalar(t, s, `SELECT count(*) FROM delegation_policy_binding`) != 1 || scalar(t, s, `SELECT count(*) FROM run WHERE worker_position='child' AND status='completed'`) != 2 {
		t.Fatal("initialization recovery repeated or lost original tree")
	}
}

func TestDelegationPluginLateForgetRejectsInitializedMethodAndChildWork(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	s, r, a, p := childFixture(t)
	h := installDelegationPlugin(t, s, r)
	p.respond = childPolicy
	first, err := h.Store.Status(ctx)
	if err != nil {
		t.Fatal(err)
	}
	selectDelegationVersion(t, h, first.Revision, "1.3.0")
	main := mustSubmit(t, a, "delegation-private", "Delegate explicit private work")
	if err = executeTaskRunID(t, a, r, main.RunID); err != nil {
		t.Fatal(err)
	}
	c := rootClaim(t, s, r)
	defer r.Release(ctx, c)
	owner := idAgent(t, s)
	jobs := t.TempDir()
	t.Setenv("TMPDIR", jobs)
	done := make(chan struct{})
	var initErr error
	go func() { defer close(done); initErr = r.Initialize(ctx, c) }()
	defer func() { cancel(); <-done }()
	tick := time.NewTicker(5 * time.Millisecond)
	defer tick.Stop()
	marker := ""
wait:
	for {
		select {
		case <-done:
			t.Fatal("method exited before late authority boundary", initErr)
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		case <-tick.C:
			paths, _ := filepath.Glob(filepath.Join(jobs, "steward-rsi-*", "delegation-ready"))
			if len(paths) == 1 {
				marker = paths[0]
				break wait
			}
		}
	}
	if err = s.Transaction(ctx, func(tx *sql.Tx) error { return r.revokeMemory(ctx, tx, owner) }); err != nil {
		t.Fatal("pure method blocked privacy transaction", err)
	}
	if err = os.WriteFile(filepath.Join(filepath.Dir(marker), "resume"), []byte("continue"), 0600); err != nil {
		t.Fatal(err)
	}
	select {
	case <-done:
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	if !errors.Is(initErr, agent.ErrClaimLost) {
		t.Fatal("late authority loss misclassified", initErr)
	}
	if scalar(t, s, `SELECT count(*) FROM delegation_policy_binding`) != 0 || scalar(t, s, `SELECT count(*) FROM run WHERE worker_position='child'`) != 0 || scalar(t, s, `SELECT count(*) FROM model_generation WHERE run_id=?`, c.RunID()) != 0 || scalar(t, s, `SELECT count(*) FROM run WHERE run_id=? AND json_extract(checkpoint_json,'$.context_ready')=1`, c.RunID()) != 0 {
		t.Fatal("late method, context or Child work became accepted after Forget")
	}
}

func TestV57DelegationPolicyMigrationDoesNotOptHistoricalTreesIn(t *testing.T) {
	ctx := context.Background()
	s, _, a, p := childFixture(t)
	p.respond = childPolicy
	mustSubmit(t, a, "historical-tree", "Delegate original work")
	mustDrain(t, a)
	snapshot := func(store *Store) string {
		t.Helper()
		var raw string
		if err := store.db.QueryRow(`SELECT json_group_array(json_object('id',run_id,'checkpoint',checkpoint_json,'status',status,'version',version)) FROM (SELECT * FROM run ORDER BY run_id)`).Scan(&raw); err != nil {
			t.Fatal(err)
		}
		return raw
	}
	before := snapshot(s)
	var counters string
	if err := s.db.QueryRow(`SELECT json_object('depth',max_depth,'descendants',max_descendants,'tools',max_tool_calls,'turns',max_turns,'used_descendants',descendants,'used_tools',tool_calls,'version',version) FROM run_tree`).Scan(&counters); err != nil {
		t.Fatal(err)
	}
	if scalar(t, s, `SELECT count(*) FROM delegation_policy_binding`) != 0 {
		t.Fatal("native fixture already managed")
	}
	// Reconstruct the previous schema using the actual accepted native tree;
	// ancestry, snapshots, original checkpoints and FK-valid receipts stay exact.
	if _, err := s.db.Exec(removeComputerActionFixtureSchema + `DROP TABLE task_label_plan; DROP TABLE code_report_method_binding; DROP TABLE code_import_policy_binding; DROP TABLE shell_method_plan; DROP TABLE file_method_plan; DROP TABLE code_description_reply; DROP TABLE delegation_policy_binding; PRAGMA user_version=57`); err != nil {
		t.Fatal(err)
	}
	data := filepath.Dir(s.path)
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	next, err := Open(ctx, data)
	if err != nil {
		t.Fatal(err)
	}
	defer next.Close()
	if snapshot(next) != before || scalar(t, next, `SELECT count(*) FROM delegation_policy_binding`) != 0 {
		t.Fatal("upgrade rewrote or retroactively opted in historical trees")
	}
	var after string
	if err = next.db.QueryRow(`SELECT json_object('depth',max_depth,'descendants',max_descendants,'tools',max_tool_calls,'turns',max_turns,'used_descendants',descendants,'used_tools',tool_calls,'version',version) FROM run_tree`).Scan(&after); err != nil || after != counters {
		t.Fatal("upgrade changed frozen Host safety counters", after, counters, err)
	}
}

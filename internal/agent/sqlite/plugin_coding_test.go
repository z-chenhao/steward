package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/z-chenhao/assistant/internal/agent"
	"github.com/z-chenhao/assistant/internal/artifact"
	"github.com/z-chenhao/assistant/internal/coding"
	"github.com/z-chenhao/assistant/internal/evidence"
	"github.com/z-chenhao/assistant/internal/llm"
	"github.com/z-chenhao/assistant/internal/plugin/codingworkspace/contract"
	"github.com/z-chenhao/assistant/internal/rsi"
	"github.com/z-chenhao/assistant/internal/task"
	"github.com/z-chenhao/assistant/internal/tool"
	"github.com/z-chenhao/assistant/pkg/rsiplugin"
)

func installCodingPlugin(t *testing.T, s *Store, r *Runs) rsi.Host {
	t.Helper()
	return installRuntimeFeature(t, s, r, "coding-workspace", rsi.Policy{ID: "coding-owner-fixture", AllowedContracts: []string{contract.ImportContract, contract.ReportContract}, Cases: []rsi.Case{
		{InstanceID: "coding-workspace", Contract: contract.ImportContract, Input: json.RawMessage(`{"ceilings":{"entries":4096,"file_bytes":8388608,"tree_bytes":67108864}}`), Expected: json.RawMessage(`{"entries":4096,"file_bytes":8388608,"tree_bytes":67108864}`)},
		{InstanceID: "coding-workspace", Contract: contract.ReportContract, Input: json.RawMessage(`{"ceilings":{"commands":256,"command_bytes":4194304,"patch_bytes":8388608}}`), Expected: json.RawMessage(`{"commands":256,"command_bytes":4194304,"patch_bytes":8388608,"order":"oldest"}`)},
	}})
}
func TestCodingPluginActualImportAndReportPreserveSourceAndOriginalFacts(t *testing.T) {
	s, r, a, p, cfg := codeFixture(t)
	h := installCodingPlugin(t, s, r)
	status, err := h.Store.Status(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	p.respond = delegateToolWork(func(req llm.GenerationRequest) (llm.GenerationResult, error) {
		if req.Messages[len(req.Messages)-1].Role == "tool" {
			return llm.GenerationResult{Text: "original file observed"}, nil
		}
		return calls("builtin__read", `{"path":"file.txt"}`), nil
	})
	mustSubmit(t, a, "coding-method", "Inspect original source")
	mustDrain(t, a)
	var ref, dir, raw, revision string
	if err = s.db.QueryRow(`SELECT t.task_ref,e.directory,b.limits_json,b.graph_revision FROM task t JOIN task_code_environment e USING(task_id) JOIN code_import_policy_binding b USING(task_id)`).Scan(&ref, &dir, &raw, &revision); err != nil {
		t.Fatal(err)
	}
	if revision != status.Revision || !strings.Contains(raw, `"entries":4096`) {
		t.Fatal("import did not accept actual pure package", revision, raw)
	}
	source, err := os.ReadFile(filepath.Join(cfg.RepositorySource, "file.txt"))
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(dir, "file.txt"))
	if err != nil || string(data) != string(source) {
		t.Fatal("method changed original source", string(data), err)
	}
	report, err := s.CodeReport(context.Background(), ref, true)
	if err != nil || report.Diff == nil || report.Diff.Changed {
		t.Fatal("default report lost actual evidence", report, err)
	}
	artifact, err := s.SaveCodeReport(context.Background(), ref)
	if err != nil {
		t.Fatal(err)
	}
	var graph, recipe string
	if err = s.db.QueryRow(`SELECT graph_revision,recipe_json FROM code_report_method_binding WHERE artifact_ref=?`, artifact.Ref).Scan(&graph, &recipe); err != nil {
		t.Fatal(err)
	}
	if graph != status.Revision || !strings.Contains(recipe, `"order":"oldest"`) {
		t.Fatal("report lost accepted recipe", graph, recipe)
	}
	if scalar(t, s, `SELECT count(*) FROM rsi_call WHERE instance_id='coding-workspace'`) != 0 {
		t.Fatal("pure coding choices created second effect ledger")
	}
	for _, query := range []string{`UPDATE code_import_policy_binding SET scope_hash='drift'`, `DELETE FROM code_import_policy_binding`, `INSERT OR REPLACE INTO code_import_policy_binding SELECT * FROM code_import_policy_binding`, `UPDATE code_report_method_binding SET recipe_json='{}'`, `DELETE FROM code_report_method_binding`, `INSERT OR REPLACE INTO code_report_method_binding SELECT * FROM code_report_method_binding`} {
		if _, err = s.db.Exec(query); err == nil {
			t.Fatal("accepted coding method mutable", query)
		}
	}
}

func codingVersionPackage(t *testing.T, version string) string {
	t.Helper()
	source := t.TempDir()
	path, err := filepath.Abs("../../plugin/codingworkspace/plugin.go")
	if err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	code := strings.Replace(string(raw), `Version: "1.0.1"`, `Version: "`+version+`"`, 1)
	if version == "1.1.0" {
		code = strings.Replace(code, `return rsiplugin.JSONResult(ctx, facts.Ceilings)`, `facts.Ceilings.Entries=min(facts.Ceilings.Entries,64);return rsiplugin.JSONResult(ctx,facts.Ceilings)`, 1)
		code = strings.Replace(code, `Commands: facts.Ceilings.Commands`, `Commands: min(facts.Ceilings.Commands,1)`, 1)
		code = strings.Replace(code, `Order: "oldest"`, `Order: "recent"`, 1)
	}
	if version == "1.2.0" {
		code = strings.Replace(code, `return rsiplugin.JSONResult(ctx, facts.Ceilings)`, `facts.Ceilings.Entries++;return rsiplugin.JSONResult(ctx,facts.Ceilings)`, 1)
		code = strings.Replace(code, `Commands: facts.Ceilings.Commands`, `Commands: facts.Ceilings.Commands+1`, 1)
	}

	if version == "1.3.0" || version == "1.4.0" {
		code = strings.Replace(code, `"context"`, `"context";"os";"time"`, 1)
		pause := `if c.OperationID=="owner-read"{if err:=os.WriteFile("coding-ready",[]byte("ready"),0600);err!=nil{return rsiplugin.Result{},err};for{if _,err:=os.Stat("resume");err==nil{break};select{case<-ctx.Done():return rsiplugin.Result{},ctx.Err();case<-time.After(time.Millisecond):}}};`
		if version == "1.3.0" {
			code = strings.Replace(code, `return rsiplugin.JSONResult(ctx, contract.ReportRecipe`, pause+`return rsiplugin.JSONResult(ctx, contract.ReportRecipe`, 1)
		} else {
			code = strings.Replace(code, `return rsiplugin.JSONResult(ctx, facts.Ceilings)`, pause+`return rsiplugin.JSONResult(ctx, facts.Ceilings)`, 1)
		}
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
	if output, err := exec.Command("go", "build", "-overlay", file, "-o", filepath.Join(source, "plugin"), "../../../cmd/plugins/coding-workspace").CombinedOutput(); err != nil {
		t.Fatal(string(output), err)
	}
	raw, err = os.ReadFile("../../../plugins/coding-workspace/steward.plugin.json")
	if err != nil {
		t.Fatal(err)
	}
	var manifest rsiplugin.Manifest
	if err = rsiplugin.Decode(raw, &manifest); err != nil {
		t.Fatal(err)
	}
	manifest.Version = version
	if version == "1.2.0" {
		compact := strings.NewReplacer(" ", "", "\n", "", "\t", "")
		manifest.Provides[0].OutputSchema = json.RawMessage(strings.Replace(compact.Replace(string(manifest.Provides[0].OutputSchema)), `"maximum":4096`, `"maximum":4097`, 1))
		manifest.Provides[1].OutputSchema = json.RawMessage(strings.Replace(compact.Replace(string(manifest.Provides[1].OutputSchema)), `"maximum":256`, `"maximum":257`, 1))
	}
	manifest, err = rsi.Seal(source, manifest)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ = json.Marshal(manifest)
	if err = os.WriteFile(filepath.Join(source, "steward.plugin.json"), raw, 0600); err != nil {
		t.Fatal(err)
	}
	return source
}
func selectCodingVersion(t *testing.T, h rsi.Host, baseline, version string) (string, string) {
	t.Helper()
	ctx := context.Background()
	pkg, err := h.Install(ctx, codingVersionPackage(t, version))
	if err != nil {
		t.Fatal(err)
	}
	graph, err := h.Prepare(ctx, rsiplugin.Graph{Protocol: rsiplugin.Protocol, Bindings: []rsiplugin.Binding{{InstanceID: "coding-workspace", PackageDigest: pkg.Digest, Config: json.RawMessage(`{}`), StateNamespace: "coding-workspace", StateVersion: 1}}})
	if err != nil {
		t.Fatal(err)
	}
	policy, err := h.Store.Policy(ctx, "coding-owner-fixture")
	if err != nil {
		t.Fatal(err)
	}
	policy.ID = "coding-owner-" + version
	for i := range policy.Cases {
		if policy.Cases[i].Contract == contract.ImportContract {
			var value contract.ImportLimits
			if json.Unmarshal(policy.Cases[i].Expected, &value) != nil {
				t.Fatal("invalid import fixture")
			}
			if version == "1.1.0" {
				value.Entries = 64
			} else if version == "1.2.0" {
				value.Entries = 4097
			}
			policy.Cases[i].Expected, _ = json.Marshal(value)
		}
		if policy.Cases[i].Contract == contract.ReportContract {
			var value contract.ReportRecipe
			if json.Unmarshal(policy.Cases[i].Expected, &value) != nil {
				t.Fatal("invalid report fixture")
			}
			if version == "1.1.0" {
				value.Commands = 1
				value.Order = "recent"
			} else if version == "1.2.0" {
				value.Commands = 257
			}
			policy.Cases[i].Expected, _ = json.Marshal(value)
		}
	}
	if err = h.Store.PutPolicy(ctx, policy); err != nil {
		t.Fatal(err)
	}
	activateDelegation(t, h, "coding-upgrade-"+version, baseline, graph.Revision, policy.ID)
	return graph.Revision, pkg.Digest
}
func TestCodingPluginIndependentUpgradeKeepsTaskImportAndChangesReportRecipe(t *testing.T) {
	s, r, a, p, cfg := codeFixture(t)
	ctx := context.Background()
	model := modelInput()
	model.ContextWindow = 32768
	activateLoop(t, s, model)
	h := installCodingPlugin(t, s, r)
	first, err := h.Store.Status(ctx)
	if err != nil {
		t.Fatal(err)
	}
	rootResponse := delegateToolWork(func(req llm.GenerationRequest) (llm.GenerationResult, error) {
		if req.Messages[len(req.Messages)-1].Role == "tool" {
			return llm.GenerationResult{Text: "commands observed"}, nil
		}
		return llm.GenerationResult{ToolCalls: []llm.ToolCall{{ID: "one", Name: "builtin__bash", Arguments: `{"command":"printf FIRST"}`}, {ID: "two", Name: "builtin__bash", Arguments: `{"command":"printf SECOND"}`}, {ID: "three", Name: "builtin__bash", Arguments: `{"command":"printf THIRD"}`}}}, nil
	})
	appendRef := ""
	p.respond = func(req llm.GenerationRequest) (llm.GenerationResult, error) {
		if appendRef != "" && !isRoot(req) && !isFollowup(req) && req.Messages[len(req.Messages)-1].Role != "tool" {
			raw, _ := json.Marshal(task.ManageRequest{Operation: "append", TaskRef: appendRef, Instruction: "Continue existing source"})
			return calls(task.ManageName, string(raw)), nil
		}
		return rootResponse(req)
	}
	drain := func() {
		t.Helper()
		for i := 0; i < 8; i++ {
			mustDrain(t, a)
			pending, err := r.PendingApprovals(ctx)
			if err != nil {
				t.Fatal(err)
			}
			if len(pending) == 0 {
				return
			}
			for _, approval := range pending {
				if err = r.Decide(ctx, idAgent(t, s), approval.ID, approval.Hash, "approved"); err != nil {
					t.Fatal(err)
				}
			}
		}
		t.Fatal("approval drain did not settle")
	}
	mustSubmit(t, a, "coding-v1", "Prepare original source and validation")
	drain()
	var ref, dir, taskID, originalPlan string
	if err = s.db.QueryRow(`SELECT t.task_ref,t.task_id,e.directory,b.limits_json FROM task t JOIN task_code_environment e USING(task_id) JOIN code_import_policy_binding b USING(task_id)`).Scan(&ref, &taskID, &dir, &originalPlan); err != nil {
		t.Fatal(err)
	}
	old, err := s.CodeReport(ctx, ref, true)
	if err != nil || len(old.Commands) != 3 || old.CommandsTruncated {
		t.Fatal("old report window changed", old, err)
	}
	receipt, err := s.SaveCodeReport(ctx, ref)
	if err != nil {
		t.Fatal(err)
	}
	second, pkg := selectCodingVersion(t, h, first.Revision, "1.1.0")
	current, err := s.CodeReport(ctx, ref, true)
	if err != nil || len(current.Commands) != 1 || current.Commands[0].Command != "printf THIRD" || !current.CommandsTruncated {
		t.Fatal("new independent report not applied", current, err)
	}
	if err = os.WriteFile(filepath.Join(cfg.RepositorySource, "file.txt"), []byte("later source"), 0600); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(dir, "file.txt"), []byte("Task edits retained"), 0600); err != nil {
		t.Fatal(err)
	}
	appendRef = ref
	mustSubmit(t, a, "coding-append", "Continue this existing Task")
	drain()
	appendRef = ""
	if scalar(t, s, `SELECT count(*) FROM run r JOIN rsi_run b USING(run_id) WHERE r.task_id=? AND r.worker_position='root' AND r.status='completed' AND b.graph_revision=?`, taskID, second) != 1 || scalar(t, s, `SELECT count(*) FROM run WHERE task_id=? AND worker_position='root'`, taskID) != 2 {
		t.Fatal("append did not run a new Root on the new composition")
	}
	var planGraph, plan string
	if err = s.db.QueryRow(`SELECT graph_revision,limits_json FROM code_import_policy_binding WHERE task_id=?`, taskID).Scan(&planGraph, &plan); err != nil {
		t.Fatal(err)
	}
	if planGraph != first.Revision || plan != originalPlan {
		t.Fatal("new Root rebound old Task import", planGraph, plan)
	}
	if data, err := os.ReadFile(filepath.Join(dir, "file.txt")); err != nil || string(data) != "Task edits retained" {
		t.Fatal("upgrade recopied Task source", string(data), err)
	}
	original, err := s.Artifacts().Describe(ctx, artifact.Scope{UserID: s.OwnerID(), AgentID: idAgent(t, s), TaskID: taskID, Owner: true}, receipt.Ref)
	if err != nil || original.ContentHash != receipt.ContentHash {
		t.Fatal("old report Artifact changed", original, err)
	}
	if err = h.Store.Retire(ctx, pkg); !errors.Is(err, rsi.ErrConflict) {
		t.Fatal("active package retired", err)
	}
	// New complete imports honor the independently narrower recipe; no partial tree.
	for i := 0; i < 65; i++ {
		if err = os.WriteFile(filepath.Join(cfg.RepositorySource, fmt.Sprintf("extra-%d", i)), []byte("owner"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	mustSubmit(t, a, "coding-small", "Import this new source")
	drain()
	var failedDir, failedStatus string
	if err = s.db.QueryRow(`SELECT directory,status FROM task_code_environment WHERE task_id!=?`, taskID).Scan(&failedDir, &failedStatus); err != nil {
		t.Fatal(err)
	}
	if failedStatus != "failed" {
		t.Fatal("over-budget import claimed success", failedStatus)
	}
	if _, err = os.Stat(failedDir); !os.IsNotExist(err) {
		t.Fatal("partial import published", err)
	}
	activateDelegation(t, h, "coding-rollback", second, first.Revision, "coding-owner-fixture")
	if err = h.Store.Retire(ctx, pkg); err != nil {
		t.Fatal(err)
	}
	restored, err := s.CodeReport(ctx, ref, true)
	if err != nil || len(restored.Commands) != 6 || restored.CommandsTruncated {
		t.Fatal("report rollback lost original commands", restored, err)
	}
	if _, err = h.Store.Deactivate(ctx, "coding-disable", first.Revision); err != nil {
		t.Fatal(err)
	}
	if _, err = s.CodeReport(ctx, ref, true); !errors.Is(err, llm.ProviderUnavailable) {
		t.Fatal("deactivation restored native report", err)
	}
	reader, err := s.Artifacts().Open(ctx, artifact.Scope{UserID: s.OwnerID(), AgentID: idAgent(t, s), TaskID: taskID, Owner: true}, receipt.Ref)
	if err != nil {
		t.Fatal("old report reference lost after deactivation", err)
	}
	body, err := io.ReadAll(reader)
	reader.Close()
	if err != nil {
		t.Fatal(err)
	}
	var history coding.Report
	if json.Unmarshal(body, &history) != nil || len(history.Commands) != 3 || history.CommandsTruncated || history.Directory != "" || history.Diff == nil || history.Diff.Changed {
		t.Fatal("old Artifact reinterpreted under new methods", string(body))
	}
}
func TestCodingPluginReadonlyReportDoesNotReserveWriter(t *testing.T) {
	s, r, a, p, _ := codeFixture(t)
	installCodingPlugin(t, s, r)
	p.respond = delegateToolWork(func(llm.GenerationRequest) (llm.GenerationResult, error) {
		return llm.GenerationResult{Text: "prepared"}, nil
	})
	mustSubmit(t, a, "report-readonly", "Prepare source")
	mustDrain(t, a)
	var ref string
	if err := s.db.QueryRow(`SELECT task_ref FROM task`).Scan(&ref); err != nil {
		t.Fatal(err)
	}
	other, err := Open(context.Background(), filepath.Dir(s.path))
	if err != nil {
		t.Fatal(err)
	}
	defer other.Close()
	writer, err := other.db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer writer.Rollback()
	if _, err = writer.Exec(`UPDATE agent_config SET config_version=config_version+1`); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	report, err := s.CodeReport(ctx, ref, true)
	if err != nil || report.Diff == nil {
		t.Fatal("readonly report waited for writer", report, err)
	}
}

func TestV61CodingMethodsMigrationPreservesNativeWorkspaceAndReport(t *testing.T) {
	s, _, a, p, _ := codeFixture(t)
	ctx := context.Background()
	p.respond = delegateToolWork(func(llm.GenerationRequest) (llm.GenerationResult, error) {
		return llm.GenerationResult{Text: "original native source"}, nil
	})
	mustSubmit(t, a, "coding-native-history", "Prepare native source")
	mustDrain(t, a)
	var ref, dir string
	if err := s.db.QueryRow(`SELECT t.task_ref,e.directory FROM task t JOIN task_code_environment e USING(task_id)`).Scan(&ref, &dir); err != nil {
		t.Fatal(err)
	}
	receipt, err := s.SaveCodeReport(ctx, ref)
	if err != nil {
		t.Fatal(err)
	}
	snapshot := func(store *Store) string {
		t.Helper()
		var value string
		if err := store.db.QueryRow(`SELECT json_object('scope',e.scope_hash,'baseline',e.baseline_hash,'directory',e.directory,'state',e.status,'checkpoint',r.checkpoint_json,'version',r.version) FROM task_code_environment e JOIN run r USING(task_id) WHERE r.worker_position='root'`).Scan(&value); err != nil {
			t.Fatal(err)
		}
		return value
	}
	before := snapshot(s)
	if _, err = s.db.Exec(removeComputerActionFixtureSchema + `DROP TABLE task_label_plan; DROP TABLE code_report_method_binding; DROP TABLE code_import_policy_binding; PRAGMA user_version=61`); err != nil {
		t.Fatal(err)
	}
	dataRoot := filepath.Dir(s.path)
	if err = s.Close(); err != nil {
		t.Fatal(err)
	}
	next, err := Open(ctx, dataRoot)
	if err != nil {
		t.Fatal(err)
	}
	defer next.Close()
	if snapshot(next) != before || scalar(t, next, `SELECT count(*) FROM code_import_policy_binding WHERE managed=1 OR graph_revision IS NOT NULL OR limits_json IS NOT NULL`) != 0 || scalar(t, next, `SELECT count(*) FROM code_report_method_binding`) != 0 {
		t.Fatal("migration rewrote or opted in old work")
	}
	if data, err := os.ReadFile(filepath.Join(dir, "file.txt")); err != nil || string(data) != "dirty source" {
		t.Fatal("upgrade altered native workspace", string(data), err)
	}
	descriptor, err := next.Artifacts().Describe(ctx, artifact.Scope{UserID: next.OwnerID(), AgentID: idAgent(t, next), Owner: true}, receipt.Ref)
	if err != nil || descriptor.ContentHash != receipt.ContentHash {
		t.Fatal("upgrade lost original native report", descriptor, err)
	}
	report, err := next.CodeReport(ctx, ref, true)
	if err != nil || report.Diff == nil || report.Diff.Changed {
		t.Fatal("native report did not survive upgrade", report, err)
	}
}

func TestCodingPluginMechanicalPolicyCannotExpandHostImportOrReport(t *testing.T) {
	s, r, a, p, _ := codeFixture(t)
	ctx := context.Background()
	p.respond = delegateToolWork(func(llm.GenerationRequest) (llm.GenerationResult, error) {
		return llm.GenerationResult{Text: "source prepared"}, nil
	})
	mustSubmit(t, a, "coding-native-before-invalid", "Prepare original source")
	mustDrain(t, a)
	var ref string
	if err := s.db.QueryRow(`SELECT task_ref FROM task`).Scan(&ref); err != nil {
		t.Fatal(err)
	}
	h := installCodingPlugin(t, s, r)
	first, err := h.Store.Status(ctx)
	if err != nil {
		t.Fatal(err)
	}
	selectCodingVersion(t, h, first.Revision, "1.2.0")
	report, err := s.CodeReport(ctx, ref, true)
	if !errors.Is(err, llm.ModelIncompatible) || report.Directory != "" || len(report.Commands) != 0 || report.Diff != nil {
		t.Fatal("bad report limits exposed facts or passed Host", report, err)
	}
	mustSubmit(t, a, "coding-invalid-new-task", "Import another complete source")
	mustDrain(t, a)
	var dir, status, code string
	if err = s.db.QueryRow(`SELECT e.directory,r.status,coalesce(r.error_code,'') FROM task_code_environment e JOIN task t USING(task_id) JOIN run r USING(task_id) WHERE t.task_ref!=? AND r.worker_position='root'`, ref).Scan(&dir, &status, &code); err != nil {
		t.Fatal(err)
	}
	if status != "failed" || code != string(llm.ModelIncompatible) {
		t.Fatal("bad import policy reached business", status, code)
	}
	if _, err = os.Stat(dir); !os.IsNotExist(err) {
		t.Fatal("widened import copied source", err)
	}
	if scalar(t, s, `SELECT count(*) FROM code_import_policy_binding WHERE managed=1 AND graph_revision IS NOT NULL`) != 0 || scalar(t, s, `SELECT count(*) FROM code_report_method_binding`) != 0 {
		t.Fatal("invalid method became accepted")
	}
}

func waitCodingMethodMarker(t *testing.T, ctx context.Context, done <-chan struct{}, jobs string) string {
	t.Helper()
	tick := time.NewTicker(5 * time.Millisecond)
	defer tick.Stop()
	for {
		select {
		case <-done:
			t.Fatal("method ended before boundary")
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		case <-tick.C:
			paths, _ := filepath.Glob(filepath.Join(jobs, "steward-rsi-*", "coding-ready"))
			if len(paths) == 1 {
				return paths[0]
			}
		}
	}
}
func TestCodingPluginLateForgetRejectsImportBeforePublishing(t *testing.T) {
	for _, resume := range []bool{true, false} {
		name := "end-guard"
		if !resume {
			name = "transport-cancel"
		}
		t.Run(name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			s, r, a, p, _ := codeFixture(t)
			h := installCodingPlugin(t, s, r)
			first, err := h.Store.Status(ctx)
			if err != nil {
				t.Fatal(err)
			}
			selectCodingVersion(t, h, first.Revision, "1.4.0")
			p.respond = delegateToolWork(func(llm.GenerationRequest) (llm.GenerationResult, error) {
				return llm.GenerationResult{Text: "must not run"}, nil
			})
			receipt := mustSubmit(t, a, "coding-private-import", "Prepare private source")
			if err = executeTaskRunID(t, a, r, receipt.RunID); err != nil {
				t.Fatal(err)
			}
			var root, dir string
			if err = s.db.QueryRow(`SELECT r.run_id,e.directory FROM run r JOIN task_code_environment e USING(task_id) WHERE r.worker_position='root' AND r.status='queued'`).Scan(&root, &dir); err != nil {
				t.Fatal(err)
			}
			claim, err := r.Claim(ctx, root)
			if err != nil {
				t.Fatal(err)
			}
			owner := idAgent(t, s)
			jobs := t.TempDir()
			t.Setenv("TMPDIR", jobs)
			done := make(chan struct{})
			var initErr error
			go func() { defer close(done); initErr = r.Initialize(ctx, claim) }()
			defer func() { cancel(); <-done }()
			marker := waitCodingMethodMarker(t, ctx, done, jobs)
			if err = s.Transaction(ctx, func(tx *sql.Tx) error { return r.revokeMemory(ctx, tx, owner) }); err != nil {
				t.Fatal("pure method blocked Forget", err)
			}
			if resume {
				if err = os.WriteFile(filepath.Join(filepath.Dir(marker), "resume"), []byte("continue"), 0600); err != nil && !os.IsNotExist(err) {
					t.Fatal(err)
				}
			}
			select {
			case <-done:
			case <-ctx.Done():
				t.Fatal(ctx.Err())
			}
			if !errors.Is(initErr, agent.ErrClaimLost) {
				t.Fatal("lost privacy cause hidden by method transport", initErr)
			}
			if _, err = os.Stat(dir); !os.IsNotExist(err) {
				t.Fatal("revoked import published source", err)
			}
			if scalar(t, s, `SELECT count(*) FROM code_import_policy_binding WHERE graph_revision IS NOT NULL`) != 0 || scalar(t, s, `SELECT count(*) FROM model_generation WHERE run_id=?`, root) != 0 {
				t.Fatal("revoked method or model accepted")
			}
		})
	}
}
func TestCodingPluginLateForgetRejectsOwnerReportBeforeReturningFacts(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	s, r, a, p, _ := codeFixture(t)
	p.respond = delegateToolWork(func(llm.GenerationRequest) (llm.GenerationResult, error) {
		return llm.GenerationResult{Text: "prepared"}, nil
	})
	mustSubmit(t, a, "coding-owner-report", "Prepare original source")
	mustDrain(t, a)
	var ref string
	if err := s.db.QueryRow(`SELECT task_ref FROM task`).Scan(&ref); err != nil {
		t.Fatal(err)
	}
	h := installCodingPlugin(t, s, r)
	first, err := h.Store.Status(ctx)
	if err != nil {
		t.Fatal(err)
	}
	selectCodingVersion(t, h, first.Revision, "1.3.0")
	owner := idAgent(t, s)
	jobs := t.TempDir()
	t.Setenv("TMPDIR", jobs)
	done := make(chan struct{})
	var report coding.Report
	var readErr error
	go func() { defer close(done); report, readErr = s.CodeReport(ctx, ref, true) }()
	defer func() { cancel(); <-done }()
	marker := waitCodingMethodMarker(t, ctx, done, jobs)
	if err = s.Transaction(ctx, func(tx *sql.Tx) error { return r.revokeMemory(ctx, tx, owner) }); err != nil {
		t.Fatal("report method blocked Forget", err)
	}
	if err = os.WriteFile(filepath.Join(filepath.Dir(marker), "resume"), []byte("continue"), 0600); err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
	select {
	case <-done:
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	if !errors.Is(readErr, agent.ErrNotFound) || report.Directory != "" || len(report.Commands) != 0 || report.Diff != nil {
		t.Fatal("private report crossed Forget", report, readErr)
	}
	if scalar(t, s, `SELECT count(*) FROM code_report_method_binding`) != 0 || scalar(t, s, `SELECT count(*) FROM rsi_call WHERE instance_id='coding-workspace'`) != 0 {
		t.Fatal("pure revoked report wrote effects")
	}
}

func TestCodingPluginImportChoiceAndPublishCommitRecoverOriginalTask(t *testing.T) {
	for _, boundary := range []string{"choice", "publish"} {
		t.Run(boundary, func(t *testing.T) {
			s, r, a, p, cfg := codeFixture(t)
			ctx := context.Background()
			h := installCodingPlugin(t, s, r)
			selected, err := h.Store.Status(ctx)
			if err != nil {
				t.Fatal(err)
			}
			p.respond = delegateToolWork(func(llm.GenerationRequest) (llm.GenerationResult, error) {
				return llm.GenerationResult{Text: "prepared original Task"}, nil
			})
			trigger := `CREATE TRIGGER reject_import_accept BEFORE UPDATE ON code_import_policy_binding WHEN NEW.graph_revision IS NOT NULL BEGIN SELECT RAISE(ABORT,'injected import choice failure'); END`
			if boundary == "publish" {
				trigger = `CREATE TRIGGER reject_import_accept BEFORE UPDATE ON task_code_environment WHEN NEW.status='ready' BEGIN SELECT RAISE(ABORT,'injected publication acceptance failure'); END`
			}
			if _, err = s.db.Exec(trigger); err != nil {
				t.Fatal(err)
			}
			mustSubmit(t, a, "coding-fault", "Prepare a complete source")
			if err = a.Drain(ctx); !errors.Is(err, agent.ErrStorage) {
				t.Fatal("original acceptance fault not reached", err)
			}
			var taskID, root, dir string
			if err = s.db.QueryRow(`SELECT e.task_id,r.run_id,e.directory FROM task_code_environment e JOIN run r USING(task_id) WHERE r.worker_position='root'`).Scan(&taskID, &root, &dir); err != nil {
				t.Fatal(err)
			}
			accepted := scalar(t, s, `SELECT count(*) FROM code_import_policy_binding WHERE graph_revision IS NOT NULL`)
			if boundary == "choice" {
				if _, err = os.Stat(dir); !os.IsNotExist(err) || accepted != 0 {
					t.Fatal("unaccepted method copied files", err, accepted)
				}
			} else {
				if data, err := os.ReadFile(filepath.Join(dir, "file.txt")); err != nil || string(data) != "dirty source" || accepted != 1 {
					t.Fatal("published receipt lost original bytes", string(data), err, accepted)
				}
			}
			if err = os.WriteFile(filepath.Join(cfg.RepositorySource, "file.txt"), []byte("later owner source"), 0600); err != nil {
				t.Fatal(err)
			}
			if _, err = s.db.Exec(`DROP TRIGGER reject_import_accept`); err != nil {
				t.Fatal(err)
			}
			mustDrain(t, a)
			expected := "later owner source"
			if boundary == "publish" {
				expected = "dirty source"
			}
			if data, err := os.ReadFile(filepath.Join(dir, "file.txt")); err != nil || string(data) != expected {
				t.Fatal("recovery recopied or lost source", string(data), err)
			}
			if scalar(t, s, `SELECT count(*) FROM task_code_environment WHERE task_id=? AND directory=? AND status='ready'`, taskID, dir) != 1 || scalar(t, s, `SELECT count(*) FROM run WHERE run_id=? AND status='completed'`, root) != 1 || scalar(t, s, `SELECT count(*) FROM run WHERE task_id=? AND worker_position='root'`, taskID) != 1 {
				t.Fatal("recovery changed Task/Root identity")
			}
			var graph string
			if err = s.db.QueryRow(`SELECT graph_revision FROM code_import_policy_binding WHERE task_id=?`, taskID).Scan(&graph); err != nil || graph != selected.Revision {
				t.Fatal("recovery selected another import method", graph, err)
			}
			if scalar(t, s, `SELECT count(*) FROM rsi_call WHERE instance_id='coding-workspace'`) != 0 {
				t.Fatal("pure import introduced unknown replay")
			}
		})
	}
}
func TestCodingPluginArtifactCommitFailureKeepsMethodAndArtifactAtomic(t *testing.T) {
	s, r, a, p, _ := codeFixture(t)
	ctx := context.Background()
	installCodingPlugin(t, s, r)
	p.respond = delegateToolWork(func(llm.GenerationRequest) (llm.GenerationResult, error) {
		return llm.GenerationResult{Text: "prepared"}, nil
	})
	mustSubmit(t, a, "coding-artifact-fault", "Prepare original report")
	mustDrain(t, a)
	var ref string
	if err := s.db.QueryRow(`SELECT task_ref FROM task`).Scan(&ref); err != nil {
		t.Fatal(err)
	}
	before := scalar(t, s, `SELECT count(*) FROM artifact`)
	if _, err := s.db.Exec(`CREATE TRIGGER reject_report_accept BEFORE INSERT ON code_report_method_binding BEGIN SELECT RAISE(ABORT,'injected report receipt failure'); END`); err != nil {
		t.Fatal(err)
	}
	rejected, err := s.SaveCodeReport(ctx, ref)
	if err == nil || rejected.Ref != "" || scalar(t, s, `SELECT count(*) FROM artifact`) != before || scalar(t, s, `SELECT count(*) FROM code_report_method_binding`) != 0 {
		t.Fatal("method failure partially committed Artifact", rejected, err)
	}
	if _, err = s.db.Exec(`DROP TRIGGER reject_report_accept`); err != nil {
		t.Fatal(err)
	}
	accepted, err := s.SaveCodeReport(ctx, ref)
	if err != nil || accepted.Ref == "" || scalar(t, s, `SELECT count(*) FROM artifact`) != before+1 || scalar(t, s, `SELECT count(*) FROM code_report_method_binding`) != 1 {
		t.Fatal("retry lost original atomic acceptance", accepted, err)
	}
}
func TestCodingPluginLateForgetAndGuardianRejectReportArtifactCommit(t *testing.T) {
	for _, cause := range []string{"forget", "stop", "quarantine"} {
		t.Run(cause, func(t *testing.T) {
			s, r, a, p, _ := codeFixture(t)
			ctx := context.Background()
			h := installCodingPlugin(t, s, r)
			selected, err := h.Store.Status(ctx)
			if err != nil {
				t.Fatal(err)
			}
			p.respond = delegateToolWork(func(llm.GenerationRequest) (llm.GenerationResult, error) {
				return llm.GenerationResult{Text: "prepared"}, nil
			})
			mustSubmit(t, a, "coding-commit-fence", "Prepare original report")
			mustDrain(t, a)
			var ref, taskID string
			if err = s.db.QueryRow(`SELECT task_ref,task_id FROM task`).Scan(&ref, &taskID); err != nil {
				t.Fatal(err)
			}
			original, err := s.SaveCodeReport(ctx, ref)
			if err != nil {
				t.Fatal(err)
			}
			owner := idAgent(t, s)
			before := scalar(t, s, `SELECT count(*) FROM artifact`)
			s.MainBoundary = func(point string) {
				if point != "before_code_report_commit" {
					return
				}
				switch cause {
				case "forget":
					err = s.Transaction(ctx, func(tx *sql.Tx) error { return r.revokeMemory(ctx, tx, owner) })
				case "stop":
					err = h.Store.Stop(ctx, true)
				case "quarantine":
					err = h.Store.Quarantine(ctx, selected.Revision)
				}
				if err != nil {
					t.Fatal(err)
				}
			}
			rejected, err := s.SaveCodeReport(ctx, ref)
			expected := error(llm.ProviderUnavailable)
			if cause == "forget" {
				expected = agent.ErrNotFound
			}
			if !errors.Is(err, expected) || rejected.Ref != "" || scalar(t, s, `SELECT count(*) FROM artifact`) != before || scalar(t, s, `SELECT count(*) FROM code_report_method_binding`) != 1 {
				t.Fatal("revoked report accepted", rejected, err)
			}
			if cause == "forget" {
				// Forget revokes new evidence/report use. Explicit original
				// Artifact history keeps its own pre-existing owner authorization.
				if _, err := r.ReadEvidence(ctx, evidence.Request{Kind: "artifact", Ref: tool.ObjectID(original.Ref)}); !errors.Is(err, artifact.ErrNotFound) {
					t.Fatal("old report reentered evidence after Forget", err)
				}
			}
			scope := artifact.Scope{UserID: s.OwnerID(), AgentID: owner, TaskID: taskID, Owner: true}
			reader, err := s.Artifacts().Open(ctx, scope, original.Ref)
			if err != nil {
				t.Fatal("Guardian removed original fact Artifact", err)
			}
			data, err := io.ReadAll(reader)
			reader.Close()
			if err != nil || len(data) == 0 {
				t.Fatal("original report bytes unavailable", err)
			}
		})
	}
}

func TestCodingPluginQueuedVersionsNativeBoundaryRetirementAndDisabledImport(t *testing.T) {
	s, r, a, p, _ := codeFixture(t)
	ctx := context.Background()
	model := modelInput()
	model.ContextWindow = 32768
	activateLoop(t, s, model)
	r.ConfigurePlugins(rsi.NewCatalog(filepath.Dir(s.path)))
	p.respond = delegateToolWork(func(llm.GenerationRequest) (llm.GenerationResult, error) {
		return llm.GenerationResult{Text: "source prepared"}, nil
	})
	queue := func(key string) string {
		t.Helper()
		receipt := mustSubmit(t, a, key, "Prepare a fresh Task source")
		if err := executeTaskRunID(t, a, r, receipt.RunID); err != nil {
			t.Fatal(err)
		}
		var root string
		if err := s.db.QueryRow(`SELECT run_id FROM run WHERE worker_position='root' AND status='queued' ORDER BY created_at DESC LIMIT 1`).Scan(&root); err != nil {
			t.Fatal(err)
		}
		return root
	}
	native := queue("coding-queued-native")
	h := installCodingPlugin(t, s, r)
	first, err := h.Store.Status(ctx)
	if err != nil {
		t.Fatal(err)
	}
	old := queue("coding-queued-old")
	second, pkg := selectCodingVersion(t, h, first.Revision, "1.1.0")
	retained := queue("coding-queued-retained")
	activateDelegation(t, h, "coding-queue-rollback", second, first.Revision, "coding-owner-fixture")
	if err = h.Store.Retire(ctx, pkg); err != nil {
		t.Fatal(err)
	}
	mustDrain(t, a)
	check := func(root, graph string, entries int) {
		t.Helper()
		var managed bool
		var actual sql.NullString
		var raw sql.NullString
		var status string
		if err := s.db.QueryRow(`SELECT b.managed,b.graph_revision,b.limits_json,r.status FROM run r JOIN code_import_policy_binding b USING(task_id) WHERE r.run_id=?`, root).Scan(&managed, &actual, &raw, &status); err != nil {
			t.Fatal(err)
		}
		if status != "completed" || managed != (graph != "") || actual.String != graph {
			t.Fatal("queued execution changed import mode/version", root, status, managed, actual)
		}
		if graph == "" {
			if raw.Valid {
				t.Fatal("native queued source opted in")
			}
			return
		}
		var limits coding.ImportLimits
		if json.Unmarshal([]byte(raw.String), &limits) != nil || limits.Entries != entries {
			t.Fatal("queued import used latest recipe", raw)
		}
	}
	check(native, "", 0)
	check(old, first.Revision, 4096)
	check(retained, second, 64)
	if _, err = h.Store.Deactivate(ctx, "coding-queue-disable", first.Revision); err != nil {
		t.Fatal(err)
	}
	blocked := queue("coding-queued-disabled")
	mustDrain(t, a)
	var status, code, dir string
	var graph sql.NullString
	var managed bool
	if err = s.db.QueryRow(`SELECT r.status,coalesce(r.error_code,''),e.directory,b.managed,b.graph_revision FROM run r JOIN task_code_environment e USING(task_id) JOIN code_import_policy_binding b USING(task_id) WHERE r.run_id=?`, blocked).Scan(&status, &code, &dir, &managed, &graph); err != nil {
		t.Fatal(err)
	}
	if status != "failed" || code != string(llm.ProviderUnavailable) || !managed || graph.Valid {
		t.Fatal("deactivation restored native import", status, code, managed, graph)
	}
	if _, err = os.Stat(dir); !os.IsNotExist(err) {
		t.Fatal("disabled method published source", err)
	}
	if scalar(t, s, `SELECT count(*) FROM model_generation WHERE run_id=?`, blocked) != 0 {
		t.Fatal("disabled import reached model")
	}
}

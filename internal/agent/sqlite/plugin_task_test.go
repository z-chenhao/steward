package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/z-chenhao/assistant/internal/agent"
	"github.com/z-chenhao/assistant/internal/llm"
	"github.com/z-chenhao/assistant/internal/plugin/taskcontrol/contract"
	"github.com/z-chenhao/assistant/internal/rsi"
	"github.com/z-chenhao/assistant/internal/task"
	"github.com/z-chenhao/assistant/internal/tool"
	"github.com/z-chenhao/assistant/pkg/rsiplugin"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

func installTaskPlugin(t *testing.T, s *Store, r *Runs) rsi.Host {
	t.Helper()
	facts, _ := json.Marshal(contract.SearchFacts{Query: "", Limit: 2, Candidates: []contract.Candidate{{Text: "retained original", Status: "paused", UpdatedAt: "2026-01-01"}, {Text: "current target", Status: "active", UpdatedAt: "2026-01-01"}}})
	return installRuntimeFeature(t, s, r, "task-control", rsi.Policy{ID: "task-owner-fixture", AllowedContracts: []string{contract.SearchContract, contract.LabelContract}, AllowedRequests: []string{"input.read"}, Cases: []rsi.Case{
		{InstanceID: "task-control", Contract: contract.SearchContract, Input: json.RawMessage(`{"input_id":"facts"}`), Inputs: []rsi.InputData{{ID: "facts", Data: facts}}, Expected: json.RawMessage(`{"indices":[1,0]}`)},
		{InstanceID: "task-control", Contract: contract.LabelContract, Input: json.RawMessage(`{"text":"default   task\nbrief","explicit":false}`), Expected: json.RawMessage(`{"title":"default task brief"}`)},
		{InstanceID: "task-control", Contract: contract.LabelContract, Input: json.RawMessage(`{"text":"Chosen title","explicit":true}`), Expected: json.RawMessage(`{"title":"Chosen title"}`)},
	}})
}
func TestTaskPluginCannotRewriteExplicitTitleOrInventRows(t *testing.T) {
	for _, title := range []string{"", "fake title", " Chosen title", "Chosen\ntitle"} {
		if validTaskLabel(task.ManageRequest{Title: "Chosen title"}, title) {
			t.Fatal("explicit label changed", title)
		}
	}
	if !validTaskLabel(task.ManageRequest{Title: "Chosen   title"}, "Chosen title") {
		t.Fatal("native explicit normalization changed")
	}
	candidates := []task.Candidate{{Summary: task.Summary{TaskRef: "task:real"}}}
	for _, indices := range [][]int{{-1}, {1}, {0, 0}} {
		if _, err := taskSelection(candidates, 2, contract.SearchPlan{Indices: indices}); err == nil {
			t.Fatal("invented or duplicated Task", indices)
		}
	}
}
func TestTaskPluginDefaultLabelAndDurableReceipt(t *testing.T) {
	s, r, a, p := taskFixture(t)
	installTaskPlugin(t, s, r)
	p.respond = func(req llm.GenerationRequest) (llm.GenerationResult, error) {
		if isRoot(req) {
			return llm.GenerationResult{Text: "exact result"}, nil
		}
		if isFollowup(req) || req.Messages[len(req.Messages)-1].Role == "tool" {
			return llm.GenerationResult{Text: "Task observed"}, nil
		}
		return calls(task.ManageName, `{"operation":"create","task":"Inspect   the original evidence\nand deliver its result."}`), nil
	}
	receipt := mustSubmit(t, a, "task-managed", "perform this bounded task")
	mustDrain(t, a)
	var title, anchor string
	if err := s.db.QueryRow(`SELECT title,anchor_task FROM task`).Scan(&title, &anchor); err != nil || title != "Inspect the original evidence and deliver its result." || anchor != "Inspect   the original evidence\nand deliver its result." {
		t.Fatal("label policy rewrote original brief", title, anchor, err)
	}
	if scalar(t, s, `SELECT count(*) FROM task`) != 1 || scalar(t, s, `SELECT count(*) FROM task_label_plan p JOIN tool_invocation i USING(invocation_id) WHERE i.run_id=? AND p.title IS NOT NULL`, receipt.RunID) != 1 {
		t.Fatal("actual label package not used once")
	}
	before := scalar(t, s, `SELECT count(*) FROM run`)
	mustDrain(t, a)
	if scalar(t, s, `SELECT count(*) FROM run`) != before || scalar(t, s, `SELECT count(*) FROM task`) != 1 {
		t.Fatal("accepted task replay repeated work")
	}
}

func seedSearchTasks(t *testing.T, s *Store) {
	t.Helper()
	now := time.Now().UnixMicro()
	for _, test := range []struct {
		ID, Status, Text, Conversation, Owner string
		Created                               int64
	}{{"a", "active", "vendor renewal contract evidence", "", "", now}, {"b", "active", "coffee beans espresso", "", "", now}, {"c", "paused", "vendor renewal contract retained instructions", "", "", now}, {"hidden", "active", "secret old renewal", "", "", 1}, {"foreign-agent", "active", "secret foreign renewal", "", "another-owner", now}, {"foreign-conversation", "active", "secret other conversation renewal", "another-conversation", "", now}} {
		if _, err := s.db.Exec(`INSERT INTO task(task_id,task_ref,agent_id,conversation_id,title,anchor_task,anchor_artifact_refs,status,task_epoch,created_at,updated_at,version) SELECT ?,?,CASE WHEN ?='' THEN agent_id ELSE ? END,CASE WHEN ?='' THEN agent_id ELSE ? END,?,?,'[]',?,1,?,?,1 FROM agent_config WHERE singleton=1`, test.ID, "task:"+test.ID, test.Owner, test.Owner, test.Conversation, test.Conversation, test.ID, test.Text, "active", test.Created, now); err != nil {
			t.Fatal(err)
		}
		if test.Status != "active" {
			if _, err := s.db.Exec(`UPDATE task SET status=?,task_epoch=task_epoch+1,version=version+1 WHERE task_id=?`, test.Status, test.ID); err != nil {
				t.Fatal(err)
			}
		}
	}
}
func TestTaskPluginActualSearchKeepsScopeFiltersAndOriginalFacts(t *testing.T) {
	ctx := context.Background()
	calls := []llm.ToolCall{{ID: "all", Name: task.SearchName, Arguments: `{"scope":"all"}`}, {ID: "query", Name: task.SearchName, Arguments: `{"query":"renewal","scope":"all"}`}, {ID: "paused", Name: task.SearchName, Arguments: `{"status":["paused"],"scope":"all"}`}}
	s, r, c, executor, exchange := admitPolicyRun(t, task.Definitions(), calls, false, func(s *Store, r *Runs) {
		installTaskPlugin(t, s, r)
		if _, err := s.db.Exec(`INSERT INTO memory_privacy(agent_id,epoch,history_floor,updated_at) SELECT agent_id,1,0,2 FROM agent_config WHERE singleton=1`); err != nil {
			t.Fatal(err)
		}
	})
	defer r.Release(ctx, c)
	seedSearchTasks(t, s)
	results, err := executor.Execute(ctx, exchange, calls)
	if err != nil || len(results) != 3 {
		t.Fatal(results, err)
	}
	for i, result := range results {
		if result.Error != nil {
			t.Fatal(result)
		}
		var summaries []task.Summary
		if err = json.Unmarshal([]byte(result.Text), &summaries); err != nil {
			t.Fatal(err)
		}
		want := 3
		if i == 1 {
			want = 2
		}
		if i == 2 {
			want = 1
		}
		if len(summaries) != want {
			t.Fatal("query/filter scope changed", i, summaries)
		}
		for _, summary := range summaries {
			if strings.Contains(summary.TaskRef, "foreign") || strings.Contains(summary.TaskRef, "hidden") || summary.Title == "" || summary.GoalSummary == "" || summary.UpdatedAt == "" {
				t.Fatal("scope or original fields fabricated", summary)
			}
		}
		if i == 2 && summaries[0].Status != "paused" {
			t.Fatal("paused filter overridden", summaries)
		}
	}
	if scalar(t, s, `SELECT count(*) FROM rsi_call WHERE instance_id='task-control'`) != 0 {
		t.Fatal("pure discovery wrote extra Run operations")
	}
	again, err := executor.Execute(ctx, exchange, calls)
	if err != nil || !reflect.DeepEqual(results, again) {
		t.Fatal("accepted search reread changing corpus", again, err)
	}
}

func TestTaskPluginAdmittedSearchDoesNotReserveWriter(t *testing.T) {
	ctx := context.Background()
	calls := []llm.ToolCall{{ID: "read", Name: task.SearchName, Arguments: `{"scope":"all"}`}}
	s, r, c, _, exchange := admitPolicyRun(t, task.Definitions(), calls, false, func(s *Store, r *Runs) { installTaskPlugin(t, s, r) })
	defer r.Release(ctx, c)
	seedSearchTasks(t, s)
	ts := r.ToolStore(c).(*tools)
	invs, err := ts.Accept(ctx, exchange, calls, r.registry.Describe())
	if err != nil {
		t.Fatal(err)
	}
	inv, err := ts.Prepare(ctx, invs[0].ID)
	if err != nil {
		t.Fatal(err)
	}
	if err = ts.Start(ctx, inv.ID); err != nil {
		t.Fatal(err)
	}
	writer, err := Open(ctx, filepath.Dir(s.path))
	if err != nil {
		t.Fatal(err)
	}
	defer writer.Close()
	tx, err := writer.db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	readCtx, cancel := context.WithTimeout(ctx, time.Second)
	defer cancel()
	result, err := ts.ExecuteBuiltin(readCtx, inv)
	if err != nil || result.Error != nil || !strings.Contains(result.Text, "vendor renewal") {
		t.Fatal("pure Task discovery competed for WAL writer", result, err)
	}
}

func TestTaskPluginLabelReceiptRecoversBeforeTaskCommit(t *testing.T) {
	ctx := context.Background()
	s, r, a, p := taskFixture(t)
	h := installTaskPlugin(t, s, r)
	p.respond = func(req llm.GenerationRequest) (llm.GenerationResult, error) {
		if isRoot(req) {
			return llm.GenerationResult{Text: "exact result"}, nil
		}
		if isFollowup(req) || req.Messages[len(req.Messages)-1].Role == "tool" {
			return llm.GenerationResult{Text: "Task observed"}, nil
		}
		return calls(task.ManageName, `{"operation":"create","task":"Preserve the original brief and its one authorized Task."}`), nil
	}
	if _, err := s.db.Exec(`CREATE TRIGGER fail_task_after_label BEFORE INSERT ON task BEGIN SELECT RAISE(ABORT,'fixture task commit failure'); END`); err != nil {
		t.Fatal(err)
	}
	receipt := mustSubmit(t, a, "task-label-recovery", "perform one bounded Task")
	if err := a.Drain(ctx); err == nil {
		t.Fatal("Task transaction interruption not exercised")
	}
	if scalar(t, s, `SELECT count(*) FROM task`) != 0 || scalar(t, s, `SELECT count(*) FROM task_label_plan WHERE title IS NOT NULL`) != 1 {
		t.Fatal("label or Task partially committed")
	}
	if _, err := s.db.Exec(`DROP TRIGGER fail_task_after_label; CREATE TRIGGER fail_task_tool_finish BEFORE UPDATE OF status ON tool_invocation WHEN NEW.tool_name='builtin__task_manage' AND NEW.status='succeeded' BEGIN SELECT RAISE(ABORT,'fixture receipt failure'); END`); err != nil {
		t.Fatal(err)
	}
	if err := a.Drain(ctx); err == nil {
		t.Fatal("post-Task receipt interruption not exercised")
	}
	if scalar(t, s, `SELECT count(*) FROM task`) != 1 || scalar(t, s, `SELECT count(*) FROM task_invocation`) != 1 || scalar(t, s, `SELECT count(*) FROM task_label_plan WHERE title IS NOT NULL`) != 1 {
		t.Fatal("recovery repeated policy or Task")
	}
	if _, err := s.db.Exec(`DROP TRIGGER fail_task_tool_finish`); err != nil {
		t.Fatal(err)
	}
	c, err := r.Claim(ctx, receipt.RunID)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Release(ctx, c)
	var inv tool.Invocation
	var stored string
	if err = s.db.QueryRow(`SELECT i.invocation_id,t.receipt_json FROM tool_invocation i JOIN task_invocation t USING(invocation_id) WHERE i.run_id=?`, receipt.RunID).Scan(&inv.ID, &stored); err != nil {
		t.Fatal(err)
	}
	status, err := h.Store.Status(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err = h.Store.Quarantine(ctx, status.Revision); err != nil {
		t.Fatal(err)
	}
	result, err := r.ToolStore(c).(*tools).ExecuteBuiltin(ctx, inv)
	if err != nil || result.Error != nil || result.Text != stored || scalar(t, s, `SELECT count(*) FROM task`) != 1 {
		t.Fatal("accepted Task receipt depended on redispatching quarantined policy", result, err)
	}
}

// This is the schema63 crash state captured by the real CLI diagnostic: the
// label computation was admitted, but neither its result nor a Task committed.
// Recover through the original invocation, without reconciling the opaque call.
func TestTaskPluginInterruptedLegacyLabelRecoversWithoutRetryingOpaqueCall(t *testing.T) {
	testTaskPluginLegacyLabelRecovery(t, false)
}

func TestTaskPluginCompletedLegacyLabelKeepsOriginalChoice(t *testing.T) {
	testTaskPluginLegacyLabelRecovery(t, true)
}

func testTaskPluginLegacyLabelRecovery(t *testing.T, completed bool) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	s, r, a, p := taskFixture(t)
	host := installTaskPlugin(t, s, r)
	// Graceful shutdown drains already accepted tools. Keep the crash fixture
	// before Task acceptance even when the replacement pure adapter completes.
	if _, err := s.db.Exec(`CREATE TRIGGER hold_legacy_task_crash BEFORE INSERT ON task BEGIN SELECT RAISE(ABORT,'injected pre-Task crash'); END`); err != nil {
		t.Fatal(err)
	}
	p.respond = func(req llm.GenerationRequest) (llm.GenerationResult, error) {
		if isRoot(req) || isFollowup(req) || req.Messages[len(req.Messages)-1].Role == "tool" {
			return llm.GenerationResult{Text: "Original business result"}, nil
		}
		return calls(task.ManageName, `{"operation":"create","task":"Recover the original authorized Task."}`), nil
	}
	receipt := mustSubmit(t, a, "interrupted-title", "perform the requested Task")
	var key, hash string
	s.MainBoundary = func(name string) {
		if name != "after_tool_accept_commit" || key != "" {
			return
		}
		var id string
		if err := s.db.QueryRow(`SELECT invocation_id FROM tool_invocation WHERE run_id=?`, receipt.RunID).Scan(&id); err != nil {
			t.Fatal(err)
		}
		input, _ := json.Marshal(contract.LabelFacts{Text: "Recover the original authorized Task."})
		hash, _ = rsiplugin.Digest("steward.rsi.input/1", struct {
			Contract string
			Input    json.RawMessage
		}{contract.LabelContract, input})
		key = "task-label:" + id
		_, state, err := host.Store.BeginCall(ctx, receipt.RunID, "task-control", key, hash)
		if err != nil {
			t.Fatal(err)
		}
		if completed {
			if err = host.Store.FinishCall(ctx, receipt.RunID, "task-control", key, *state, nil, json.RawMessage(`{"title":"Historical accepted choice"}`)); err != nil {
				t.Fatal(err)
			}
		}
		cancel()
	}
	if err := a.Drain(ctx); !errors.Is(err, context.Canceled) {
		t.Fatal("interruption was not exercised", err)
	}
	if key == "" || scalar(t, s, `SELECT count(*) FROM task`) != 0 {
		t.Fatal("crash fixture did not preserve the pre-Task boundary")
	}
	s.MainBoundary = nil // Remove fault injection, then use the real recovery path.
	if _, err := s.db.Exec(`DROP TRIGGER hold_legacy_task_crash`); err != nil {
		t.Fatal(err)
	}
	if err := a.Drain(context.Background()); err != nil {
		t.Fatal("interrupted pure title blocked original Task recovery", err)
	}
	if scalar(t, s, `SELECT count(*) FROM task`) != 1 || scalar(t, s, `SELECT count(*) FROM run WHERE kind='worker'`) != 1 || scalar(t, s, `SELECT count(*) FROM task_invocation`) != 1 {
		t.Fatal("recovery duplicated original Task effects")
	}
	if completed {
		var title string
		if err := s.db.QueryRow(`SELECT title FROM task`).Scan(&title); err != nil || title != "Historical accepted choice" {
			t.Fatal("completed historical policy was replaced", title, err)
		}
	} else {
		if _, _, err := host.Store.BeginCall(context.Background(), receipt.RunID, "task-control", key, hash); !errors.Is(err, rsi.ErrUnknown) {
			t.Fatal("recovery retried or discarded opaque unknown call", err)
		}
	}
	before := scalar(t, s, `SELECT count(*) FROM run`)
	mustDrain(t, a)
	if scalar(t, s, `SELECT count(*) FROM run`) != before {
		t.Fatal("accepted Task replay repeated work")
	}
}

func taskVersionPackage(t *testing.T, version string) string {
	t.Helper()
	source := t.TempDir()
	path, err := filepath.Abs("../../plugin/taskcontrol/plugin.go")
	if err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	code := strings.Replace(string(raw), `Version: "1.0.1"`, `Version: "`+version+`"`, 1)
	if version == "1.1.0" {
		code = strings.Replace(code, `if len(r) > 80 {`, `if !f.Explicit {r=append([]rune("[v1.1] "),r...)};if len(r) > 80 {`, 1)
		code = strings.Replace(code, `return rsiplugin.JSONResult(ctx, Rank(f))`, `plan:=Rank(f);if f.Query==""{for i,j:=0,len(plan.Indices)-1;i<j;i,j=i+1,j-1{plan.Indices[i],plan.Indices[j]=plan.Indices[j],plan.Indices[i]}};return rsiplugin.JSONResult(ctx,plan)`, 1)
	} else {
		code = strings.Replace(code, `"context"`, `"context";"os";"time"`, 1)
		code = strings.Replace(code, `raw, err := rsiplugin.ReadInput`, `if c.Inputs[0].Size>1024 {if err:=os.WriteFile("task-ready",[]byte("ready"),0600);err!=nil{return rsiplugin.Result{},err};for {if _,err:=os.Stat("resume");err==nil{break};select{case<-ctx.Done():return rsiplugin.Result{},ctx.Err();case<-time.After(time.Millisecond):}}};raw, err := rsiplugin.ReadInput`, 1)
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
	if out, err := exec.Command("go", "build", "-overlay", file, "-o", filepath.Join(source, "plugin"), "../../../cmd/plugins/task-control").CombinedOutput(); err != nil {
		t.Fatal(string(out), err)
	}
	raw, err = os.ReadFile("../../../plugins/task-control/steward.plugin.json")
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
func selectTaskVersion(t *testing.T, h rsi.Host, source, id, baseline string, changed bool) (rsi.ResolvedGraph, string) {
	t.Helper()
	ctx := context.Background()
	pkg, err := h.Install(ctx, source)
	if err != nil {
		t.Fatal(err)
	}
	graph, err := h.Prepare(ctx, rsiplugin.Graph{Protocol: rsiplugin.Protocol, Bindings: []rsiplugin.Binding{{InstanceID: "task-control", PackageDigest: pkg.Digest, Config: json.RawMessage(`{}`), StateNamespace: "task-control", StateVersion: 1}}})
	if err != nil {
		t.Fatal(err)
	}
	policy, err := h.Store.Policy(ctx, "task-owner-fixture")
	if err != nil {
		t.Fatal(err)
	}
	policy.ID = id + "-owner"
	if changed {
		policy.Cases[0].Expected = json.RawMessage(`{"indices":[0,1]}`)
		policy.Cases[1].Expected = json.RawMessage(`{"title":"[v1.1] default task brief"}`)
	}
	if err = h.Store.PutPolicy(ctx, policy); err != nil {
		t.Fatal(err)
	}
	if err = h.Propose(ctx, rsi.Candidate{ID: id, Baseline: baseline, Target: graph.Revision, PolicyID: policy.ID, Goal: "independent Task discovery and labels"}); err != nil {
		t.Fatal(err)
	}
	decision, err := h.Verify(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = h.Activate(ctx, rsi.Activation{OperationID: id, Expected: baseline, Target: graph.Revision, DecisionID: decision}); err != nil {
		t.Fatal(err)
	}
	return graph, pkg.Digest
}

func TestTaskPluginSearchForgetBeforePrivateInputRelease(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	calls := []llm.ToolCall{{ID: "private-search", Name: task.SearchName, Arguments: `{"scope":"all"}`}}
	s, r, c, _, exchange := admitPolicyRun(t, task.Definitions(), calls, false, func(s *Store, r *Runs) {
		h := installTaskPlugin(t, s, r)
		first, err := h.Store.Status(ctx)
		if err != nil {
			t.Fatal(err)
		}
		selectTaskVersion(t, h, taskVersionPackage(t, "1.2.0"), "task-private-reader", first.Revision, false)
	})
	defer r.Release(ctx, c)
	seedSearchTasks(t, s)
	if _, err := s.db.Exec(`INSERT INTO task(task_id,task_ref,agent_id,conversation_id,title,anchor_task,anchor_artifact_refs,status,task_epoch,created_at,updated_at,version) SELECT 'private','task:private',agent_id,agent_id,'Private scoped task',?,'[]','active',1,?,?,1 FROM agent_config WHERE singleton=1`, strings.Repeat("PRIVATE_OWNER_TASK_ORIGINAL ", 150), time.Now().UnixMicro(), time.Now().UnixMicro()); err != nil {
		t.Fatal(err)
	}
	ts := r.ToolStore(c).(*tools)
	invs, err := ts.Accept(ctx, exchange, calls, r.registry.Describe())
	if err != nil {
		t.Fatal(err)
	}
	inv, err := ts.Prepare(ctx, invs[0].ID)
	if err != nil {
		t.Fatal(err)
	}
	if err = ts.Start(ctx, inv.ID); err != nil {
		t.Fatal(err)
	}
	jobs := t.TempDir()
	t.Setenv("TMPDIR", jobs)
	owner := idAgent(t, s)
	done := make(chan struct{})
	var result tool.Result
	var readErr error
	go func() { defer close(done); result, readErr = ts.ExecuteBuiltin(ctx, inv) }()
	defer func() { cancel(); <-done }()
	tick := time.NewTicker(5 * time.Millisecond)
	defer tick.Stop()
	marker := ""
wait:
	for {
		select {
		case <-done:
			t.Fatal("reader exited before private input boundary", result, readErr)
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		case <-tick.C:
			paths, _ := filepath.Glob(filepath.Join(jobs, "steward-rsi-*", "task-ready"))
			if len(paths) == 1 {
				marker = paths[0]
				break wait
			}
		}
	}
	if err = s.Transaction(ctx, func(tx *sql.Tx) error { return r.revokeMemory(ctx, tx, owner) }); err != nil {
		t.Fatal("pure reader blocked privacy commit", err)
	}
	if err = os.WriteFile(filepath.Join(filepath.Dir(marker), "resume"), []byte("continue"), 0600); err != nil {
		t.Fatal(err)
	}
	select {
	case <-done:
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	if !errors.Is(readErr, agent.ErrClaimLost) || result.Text != "" {
		t.Fatal("private Task corpus crossed Forget", result, readErr)
	}
	if scalar(t, s, `SELECT count(*) FROM tool_invocation WHERE invocation_id=? AND result_json IS NOT NULL`, inv.ID) != 0 {
		t.Fatal("late private result became accepted")
	}
}

func TestTaskPluginCorpusBudgetRejectsIncompleteSuccess(t *testing.T) {
	ctx := context.Background()
	calls := []llm.ToolCall{{ID: "bounded-search", Name: task.SearchName, Arguments: `{"scope":"all"}`}}
	s, r, c, executor, exchange := admitPolicyRun(t, task.Definitions(), calls, false, func(s *Store, r *Runs) { installTaskPlugin(t, s, r) })
	defer r.Release(ctx, c)
	seedSearchTasks(t, s)
	if err := s.Transaction(ctx, func(tx *sql.Tx) error {
		for i := 0; i < 513; i++ {
			if _, err := tx.Exec(`INSERT INTO task_instruction(instruction_id,task_id,task_epoch,source_message_id,instruction,artifact_refs,created_at) VALUES(?,'a',1,NULL,?,'[]',?)`, fmt.Sprintf("large-%04d", i), strings.Repeat("x", 16384), i+1); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	results, err := executor.Execute(ctx, exchange, calls)
	if err != nil || len(results) != 1 || results[0].Error == nil || results[0].Error.Code != "RESOURCE_LIMIT" || results[0].Text != "" {
		t.Fatal("oversized corpus returned partial success", results, err)
	}
	if scalar(t, s, `SELECT count(*) FROM task_instruction`) != 513 {
		t.Fatal("budget rejection changed original instructions")
	}
}

func TestTaskPluginIndependentUpgradeRollbackKeepsOriginalRequests(t *testing.T) {
	ctx := context.Background()
	s, r, a, p := taskFixture(t)
	r.ConfigurePlugins(rsi.NewCatalog(filepath.Dir(s.path)))
	brief := "Keep the complete original brief and its exact source."
	explicit := ""
	p.respond = func(req llm.GenerationRequest) (llm.GenerationResult, error) {
		if isRoot(req) {
			return llm.GenerationResult{Text: "exact business result"}, nil
		}
		if isFollowup(req) || req.Messages[len(req.Messages)-1].Role == "tool" {
			return llm.GenerationResult{Text: "Task observed"}, nil
		}
		raw, _ := json.Marshal(task.ManageRequest{Operation: "create", Task: brief, Title: explicit})
		return calls(task.ManageName, string(raw)), nil
	}
	legacy := mustSubmit(t, a, "task-native-version", "create this bounded Task")
	h := installTaskPlugin(t, s, r)
	first, err := h.Store.Status(ctx)
	if err != nil {
		t.Fatal(err)
	}
	mustDrain(t, a)
	if scalar(t, s, `SELECT count(*) FROM rsi_call WHERE run_id=?`, legacy.RunID) != 0 {
		t.Fatal("historical Run adopted a new method")
	}
	old := mustSubmit(t, a, "task-old-version", "create with the already selected method")
	second, pkg := selectTaskVersion(t, h, taskVersionPackage(t, "1.1.0"), "task-upgrade", first.Revision, true)
	mustDrain(t, a)
	labelFor := func(run string) string {
		t.Helper()
		var title, anchor string
		if err := s.db.QueryRow(`SELECT t.title,t.anchor_task FROM task t JOIN task_request q ON json_extract(q.receipt_json,'$.taskRef')=t.task_ref JOIN tool_invocation i ON i.invocation_id=q.invocation_id WHERE i.run_id=?`, run).Scan(&title, &anchor); err != nil || anchor != brief {
			t.Fatal("policy changed the original Task", title, anchor, err)
		}
		return title
	}
	if labelFor(old.RunID) != brief {
		t.Fatal("queued Main silently changed labels")
	}
	current := mustSubmit(t, a, "task-new-version", "create with the new method")
	mustDrain(t, a)
	if labelFor(current.RunID) != "[v1.1] "+brief {
		t.Fatal("new independent binary was not used")
	}
	if scalar(t, s, `SELECT count(DISTINCT request_hash) FROM task_request`) != 1 {
		t.Fatal("derived labels rewrote canonical accepted arguments")
	}
	explicit = "My explicit title"
	chosen := mustSubmit(t, a, "task-explicit-label", "keep my explicit title")
	mustDrain(t, a)
	if labelFor(chosen.RunID) != explicit {
		t.Fatal("plugin replaced an explicit title")
	}
	explicit = ""
	// An actual new Main query consumes the changed ranking, not a helper-only
	// verdict. Only these two Tasks remain active; original workflows completed.
	now := time.Now().UnixMicro()
	for _, id := range []string{"rank-a", "rank-b"} {
		if _, err = s.db.Exec(`INSERT INTO task(task_id,task_ref,agent_id,conversation_id,title,anchor_task,anchor_artifact_refs,status,task_epoch,created_at,updated_at,version) SELECT ?,?,agent_id,agent_id,?,'original search anchor','[]','active',1,?,?,1 FROM agent_config WHERE singleton=1`, id, "task:"+id, id, now, now); err != nil {
			t.Fatal(err)
		}
	}
	createPolicy := p.respond
	p.respond = func(req llm.GenerationRequest) (llm.GenerationResult, error) {
		if req.Messages[len(req.Messages)-1].Role == "tool" {
			return llm.GenerationResult{Text: "search observed"}, nil
		}
		return calls(task.SearchName, `{"scope":"all","status":["active"]}`), nil
	}
	query := mustSubmit(t, a, "task-new-rank", "browse existing active Tasks")
	mustDrain(t, a)
	var resultRaw string
	if err = s.db.QueryRow(`SELECT result_json FROM tool_invocation WHERE run_id=? AND tool_name=?`, query.RunID, task.SearchName).Scan(&resultRaw); err != nil {
		t.Fatal(err)
	}
	var result tool.Result
	var rows []task.Summary
	if json.Unmarshal([]byte(resultRaw), &result) != nil || json.Unmarshal([]byte(result.Text), &rows) != nil || len(rows) != 2 || rows[0].TaskRef != "task:rank-b" {
		t.Fatal("new rank bypassed actual tool or invented facts", result, rows)
	}
	p.respond = createPolicy
	retained := mustSubmit(t, a, "task-retained-label", "retain this already selected method")
	if err = h.Propose(ctx, rsi.Candidate{ID: "task-rollback", Baseline: second.Revision, Target: first.Revision, PolicyID: "task-owner-fixture", Goal: "retain old Tasks while rolling back new selection"}); err != nil {
		t.Fatal(err)
	}
	decision, err := h.Verify(ctx, "task-rollback")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = h.Activate(ctx, rsi.Activation{OperationID: "task-rollback", Expected: second.Revision, Target: first.Revision, DecisionID: decision}); err != nil {
		t.Fatal(err)
	}
	if err = h.Store.Retire(ctx, pkg); err != nil {
		t.Fatal(err)
	}
	s, r, a = reopenMethodRuntime(t, s, r, a, p)
	h = *r.plugins
	mustDrain(t, a)
	if labelFor(retained.RunID) != "[v1.1] "+brief {
		t.Fatal("retired binary lost a pending Main")
	}
	rolled := mustSubmit(t, a, "task-rolled-label", "use rolled back method")
	mustDrain(t, a)
	if labelFor(rolled.RunID) != brief {
		t.Fatal("rollback changed original requests")
	}
	if _, err = h.Store.Deactivate(ctx, "task-cold-disable", first.Revision); err != nil {
		t.Fatal(err)
	}
	s, _, a = reopenMethodRuntime(t, s, r, a, p)
	count := scalar(t, s, `SELECT count(*) FROM task`)
	disabled := mustSubmit(t, a, "task-disabled-cold", "create after disabled restart")
	mustDrain(t, a)
	if scalar(t, s, `SELECT count(*) FROM task`) != count || scalar(t, s, `SELECT count(*) FROM tool_invocation WHERE run_id=? AND json_extract(result_json,'$.error.code')='PROVIDER_UNAVAILABLE'`, disabled.RunID) != 1 {
		t.Fatal("cold startup restored native Task labels")
	}
}

func TestTaskPluginDeactivationKeepsHostCancellationAndDeniesNewLabels(t *testing.T) {
	ctx := context.Background()
	s, r, a := controlPolicyFixture(t, nil, "ongoing", true)
	controlMain(t, r, a, "task-before-disable", "create")
	status, err := r.plugins.Store.Status(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = r.plugins.Store.Deactivate(ctx, "task-disable", status.Revision); err != nil {
		t.Fatal(err)
	}
	controlMain(t, r, a, "task-cancel-disabled", "cancel")
	if scalar(t, s, `SELECT count(*) FROM task WHERE status='cancelled'`) != 1 {
		t.Fatal("missing label policy blocked Host cancellation")
	}
	controlMain(t, r, a, "task-create-disabled", "create")
	if scalar(t, s, `SELECT count(*) FROM task`) != 1 || scalar(t, s, `SELECT count(*) FROM tool_invocation WHERE tool_name='builtin__task_manage' AND json_extract(result_json,'$.error.code')='PROVIDER_UNAVAILABLE'`) != 1 {
		t.Fatal("deactivation resurrected native Task creation")
	}
}

func TestTaskPluginLargeCorpusSearchesRetainedInstructionsBeyondOneFrame(t *testing.T) {
	ctx := context.Background()
	calls := []llm.ToolCall{{ID: "all-instructions", Name: task.SearchName, Arguments: `{"query":"continuationneedle","scope":"all","limit":1}`}}
	s, r, c, executor, exchange := admitPolicyRun(t, task.Definitions(), calls, false, func(s *Store, r *Runs) { installTaskPlugin(t, s, r) })
	defer r.Release(ctx, c)
	seedSearchTasks(t, s)
	if err := s.Transaction(ctx, func(tx *sql.Tx) error {
		for i := 0; i < 100; i++ {
			text := strings.Repeat("padding ", 2048)
			if i == 99 {
				text = "continuationneedle " + text[19:]
			}
			if _, err := tx.Exec(`INSERT INTO task_instruction(instruction_id,task_id,task_epoch,source_message_id,instruction,artifact_refs,created_at) VALUES(?,'a',1,NULL,?,'[]',?)`, fmt.Sprintf("page-%04d", i), text, i+1); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	results, err := executor.Execute(ctx, exchange, calls)
	if err != nil || len(results) != 1 || results[0].Error != nil {
		t.Fatal(results, err)
	}
	var rows []task.Summary
	if json.Unmarshal([]byte(results[0].Text), &rows) != nil || len(rows) != 1 || rows[0].TaskRef != "task:a" || rows[0].GoalSummary != "vendor renewal contract evidence" || strings.Contains(results[0].Text, "continuationneedle") || strings.Contains(results[0].Text, "padding") {
		t.Fatal("large read dropped final instruction or rewrote original summary", results, rows)
	}
}

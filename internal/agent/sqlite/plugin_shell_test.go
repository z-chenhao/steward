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
	"time"

	"github.com/z-chenhao/assistant/internal/agent"
	"github.com/z-chenhao/assistant/internal/computer"
	"github.com/z-chenhao/assistant/internal/computer/environment"
	"github.com/z-chenhao/assistant/internal/llm"
	"github.com/z-chenhao/assistant/internal/plugin/shellpolicy/contract"
	"github.com/z-chenhao/assistant/internal/rsi"
	"github.com/z-chenhao/assistant/internal/tool"
	"github.com/z-chenhao/assistant/internal/tool/codemode"
	"github.com/z-chenhao/assistant/pkg/rsiplugin"
)

func installShellPlugin(t *testing.T, s *Store, r *Runs) rsi.Host {
	t.Helper()
	return installRuntimeFeature(t, s, r, "shell-tools", rsi.Policy{ID: "shell-owner-fixture", AllowedContracts: []string{contract.Contract}, Cases: []rsi.Case{
		{InstanceID: "shell-tools", Contract: contract.Contract, Input: json.RawMessage(`{"ceilings":{"timeout_seconds":120,"output_bytes":48000}}`), Expected: json.RawMessage(`{"timeout_seconds":60,"output_bytes":48000}`)},
		{InstanceID: "shell-tools", Contract: contract.Contract, Input: json.RawMessage(`{"ceilings":{"timeout_seconds":120,"output_bytes":48000},"requested_timeout":5}`), Expected: json.RawMessage(`{"timeout_seconds":5,"output_bytes":48000}`)},
	}})
}

func shellVersionPackage(t *testing.T, version string) string {
	t.Helper()
	source := t.TempDir()
	path, err := filepath.Abs("../../plugin/shellpolicy/plugin.go")
	if err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	code := strings.Replace(string(raw), `Version: "1.0.1"`, `Version: "`+version+`"`, 1)
	needle := `return rsiplugin.JSONResult(ctx, contract.Limits`
	switch version {
	case "1.1.0":
		code = strings.Replace(code, needle, `timeout=min(timeout,30);f.Ceilings.OutputBytes=min(f.Ceilings.OutputBytes,1024);`+needle, 1)
	case "1.2.0":
		code = strings.Replace(code, needle, `timeout=f.Ceilings.TimeoutSeconds+1;`+needle, 1)
	case "1.3.0":
		code = strings.Replace(code, needle, `timeout=f.Ceilings.TimeoutSeconds;`+needle, 1)
	case "1.4.0":
		code = strings.Replace(code, `"context"`, `"context";"os";"time"`, 1)
		code = strings.Replace(code, needle, `if f.RequestedTimeout!=nil&&*f.RequestedTimeout==7{if err:=os.WriteFile("shell-ready",[]byte("ready"),0600);err!=nil{return rsiplugin.Result{},err};for{if _,err:=os.Stat("resume");err==nil{break};select{case<-ctx.Done():return rsiplugin.Result{},ctx.Err();case<-time.After(time.Millisecond):}}};`+needle, 1)
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
	if out, err := exec.Command("go", "build", "-overlay", file, "-o", filepath.Join(source, "plugin"), "../../../cmd/plugins/shell-tools").CombinedOutput(); err != nil {
		t.Fatal(string(out), err)
	}
	raw, err = os.ReadFile("../../../plugins/shell-tools/steward.plugin.json")
	if err != nil {
		t.Fatal(err)
	}
	var m rsiplugin.Manifest
	if err = rsiplugin.Decode(raw, &m); err != nil {
		t.Fatal(err)
	}
	m.Version = version
	if version == "1.2.0" {
		compact := strings.NewReplacer(" ", "", "\n", "", "\t", "").Replace(string(m.Provides[0].OutputSchema))
		m.Provides[0].OutputSchema = json.RawMessage(strings.Replace(compact, `"maximum":120`, `"maximum":121`, 1))
	}
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

func selectShellVersion(t *testing.T, h rsi.Host, baseline, version string) (string, string) {
	t.Helper()
	ctx := context.Background()
	pkg, err := h.Install(ctx, shellVersionPackage(t, version))
	if err != nil {
		t.Fatal(err)
	}
	graph, err := h.Prepare(ctx, rsiplugin.Graph{Protocol: rsiplugin.Protocol, Bindings: []rsiplugin.Binding{{InstanceID: "shell-tools", PackageDigest: pkg.Digest, Config: json.RawMessage(`{}`), StateNamespace: "shell-tools", StateVersion: 1}}})
	if err != nil {
		t.Fatal(err)
	}
	policy, err := h.Store.Policy(ctx, "shell-owner-fixture")
	if err != nil {
		t.Fatal(err)
	}
	policy.ID = "shell-owner-" + version
	for i := range policy.Cases {
		var expected contract.Limits
		if err = json.Unmarshal(policy.Cases[i].Expected, &expected); err != nil {
			t.Fatal(err)
		}
		switch version {
		case "1.1.0":
			expected.TimeoutSeconds = min(expected.TimeoutSeconds, 30)
			expected.OutputBytes = 1024
		case "1.2.0":
			expected.TimeoutSeconds = 121
		case "1.3.0":
			expected.TimeoutSeconds = 120
		}
		policy.Cases[i].Expected, _ = json.Marshal(expected)
	}
	if err = h.Store.PutPolicy(ctx, policy); err != nil {
		t.Fatal(err)
	}
	activateDelegation(t, h, "shell-upgrade-"+version, baseline, graph.Revision, policy.ID)
	return graph.Revision, pkg.Digest
}

func TestShellPluginIndependentUpgradeRetainedCommandsRollbackAndDisable(t *testing.T) {
	s, r, a, p, _ := codeFixture(t)
	ctx := context.Background()
	model := modelInput()
	model.ContextWindow = 32768
	activateLoop(t, s, model)
	r.ConfigurePlugins(rsi.NewCatalog(filepath.Dir(s.path)))
	p.respond = delegateToolWork(func(req llm.GenerationRequest) (llm.GenerationResult, error) {
		if req.Messages[len(req.Messages)-1].Role == "tool" {
			return llm.GenerationResult{Text: "command observed"}, nil
		}
		return calls("builtin__bash", `{"command":"printf '%02048d' 0"}`), nil
	})
	queue := func(key string) string {
		t.Helper()
		receipt := mustSubmit(t, a, key, "Execute original command")
		if err := executeTaskRunID(t, a, r, receipt.RunID); err != nil {
			t.Fatal(err)
		}
		var id string
		if err := s.db.QueryRow(`SELECT run_id FROM run WHERE worker_position='root' AND status='queued' ORDER BY created_at DESC LIMIT 1`).Scan(&id); err != nil {
			t.Fatal(err)
		}
		return id
	}
	drain := func() {
		t.Helper()
		mustDrain(t, a)
		pending, err := r.PendingApprovals(ctx)
		if err != nil {
			t.Fatal(err)
		}
		for _, item := range pending {
			if err = r.Decide(ctx, idAgent(t, s), item.ID, item.Hash, "approved"); err != nil {
				t.Fatal(err)
			}
		}
		mustDrain(t, a)
	}
	check := func(id string, size int, managed bool) {
		t.Helper()
		var raw string
		if err := s.db.QueryRow(`SELECT result_json FROM tool_invocation WHERE run_id=? AND tool_name='builtin__bash'`, id).Scan(&raw); err != nil {
			t.Fatal(err)
		}
		var result tool.Result
		var value struct {
			Output    string
			Truncated bool
		}
		if json.Unmarshal([]byte(raw), &result) != nil || result.Error != nil || json.Unmarshal([]byte(result.Text), &value) != nil || len(value.Output) != size || value.Truncated != (size == 1024) {
			t.Fatal("retained shell recipe drift", id, raw, size)
		}
		count := scalar(t, s, `SELECT count(*) FROM shell_method_plan p JOIN tool_invocation i USING(invocation_id) WHERE i.run_id=?`, id)
		if managed != (count == 1) {
			t.Fatal("migration boundary changed", id, count, managed)
		}
	}
	legacy := queue("shell-native")
	h := installShellPlugin(t, s, r)
	first, err := h.Store.Status(ctx)
	if err != nil {
		t.Fatal(err)
	}
	drain()
	check(legacy, 2048, false)
	old := queue("shell-old")
	second, pkg := selectShellVersion(t, h, first.Revision, "1.1.0")
	drain()
	check(old, 2048, true)
	current := queue("shell-new")
	drain()
	check(current, 1024, true)
	retained := queue("shell-retained")
	activateDelegation(t, h, "shell-rollback", second, first.Revision, "shell-owner-fixture")
	if err = h.Store.Retire(ctx, pkg); err != nil {
		t.Fatal(err)
	}
	drain()
	check(retained, 1024, true)
	rolled := queue("shell-rolled")
	drain()
	check(rolled, 2048, true)
	if _, err = h.Store.Deactivate(ctx, "shell-disable", first.Revision); err != nil {
		t.Fatal(err)
	}
	disabled := queue("shell-disabled")
	drain()
	if scalar(t, s, `SELECT count(*) FROM tool_invocation WHERE run_id=? AND tool_name='builtin__bash' AND json_extract(result_json,'$.error.code')='PROVIDER_UNAVAILABLE'`, disabled) != 1 || scalar(t, s, `SELECT count(*) FROM shell_method_plan p JOIN tool_invocation i USING(invocation_id) WHERE i.run_id=?`, disabled) != 0 {
		t.Fatal("disabled shell recipe restored native execution")
	}
}

func TestShellPluginCannotExpandHostOrExplicitUserDeadline(t *testing.T) {
	for _, version := range []string{"1.2.0", "1.3.0"} {
		t.Run(version, func(t *testing.T) {
			s, r, a, p, _ := codeFixture(t)
			h := installShellPlugin(t, s, r)
			ctx := context.Background()
			first, err := h.Store.Status(ctx)
			if err != nil {
				t.Fatal(err)
			}
			selectShellVersion(t, h, first.Revision, version)
			p.respond = delegateToolWork(func(req llm.GenerationRequest) (llm.GenerationResult, error) {
				if req.Messages[len(req.Messages)-1].Role == "tool" {
					return llm.GenerationResult{Text: "invalid limits observed"}, nil
				}
				return calls("builtin__bash", `{"command":"touch MUST_NOT_RUN","timeout":1}`), nil
			})
			mustSubmit(t, a, "bad-shell-recipe", "Run only within the exact deadline")
			mustDrain(t, a)
			approvePolicy(t, s, r)
			mustDrain(t, a)
			var directory string
			if err := s.db.QueryRow(`SELECT directory FROM task_code_environment`).Scan(&directory); err != nil {
				t.Fatal(err)
			}
			if _, err := os.Stat(filepath.Join(directory, "MUST_NOT_RUN")); !os.IsNotExist(err) || scalar(t, s, `SELECT count(*) FROM shell_method_plan`) != 0 {
				t.Fatal("widened recipe reached execution", err)
			}
			if scalar(t, s, `SELECT count(*) FROM tool_invocation WHERE tool_name='builtin__bash' AND json_extract(result_json,'$.error.code')='PROVIDER_UNAVAILABLE'`) != 1 {
				t.Fatal("bad recipe not rejected by independent Host ceiling")
			}
		})
	}
}

func TestShellPluginOriginalCommandExactApprovalAndExitFacts(t *testing.T) {
	s, r, a, p, _ := codeFixture(t)
	installShellPlugin(t, s, r)
	command := `printf 'original bytes' > marker; printf 'visible output'; exit 7`
	p.respond = delegateToolWork(func(req llm.GenerationRequest) (llm.GenerationResult, error) {
		if req.Messages[len(req.Messages)-1].Role == "tool" {
			return llm.GenerationResult{Text: "command observed"}, nil
		}
		raw, _ := json.Marshal(map[string]any{"command": command, "timeout": 2})
		return calls("builtin__bash", string(raw)), nil
	})
	mustSubmit(t, a, "managed-shell", "Execute one exact command")
	mustDrain(t, a)
	var directory string
	if err := s.db.QueryRow(`SELECT directory FROM task_code_environment`).Scan(&directory); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(directory, "marker")); !os.IsNotExist(err) || scalar(t, s, `SELECT count(*) FROM shell_method_plan`) != 0 {
		t.Fatal("recipe or effect preceded concrete approval", err)
	}
	approvePolicy(t, s, r)
	mustDrain(t, a)
	var raw string
	if err := s.db.QueryRow(`SELECT result_json FROM tool_invocation WHERE tool_name='builtin__bash'`).Scan(&raw); err != nil {
		t.Fatal(err)
	}
	var result tool.Result
	var value struct {
		Command, Output string
		Exit            int  `json:"exit_code"`
		TimedOut        bool `json:"timed_out"`
		Truncated       bool
	}
	if json.Unmarshal([]byte(raw), &result) != nil || result.Error != nil || json.Unmarshal([]byte(result.Text), &value) != nil || value.Command != command || value.Output != "visible output" || value.Exit != 7 || value.TimedOut || value.Truncated {
		t.Fatal("shell recipe changed command or outcome facts", raw)
	}
	if data, err := os.ReadFile(filepath.Join(directory, "marker")); err != nil || string(data) != "original bytes" {
		t.Fatal("command ran in the wrong environment", string(data), err)
	}
	if scalar(t, s, `SELECT count(*) FROM shell_method_plan p JOIN tool_invocation i USING(invocation_id) JOIN rsi_run b USING(run_id) WHERE p.graph_revision=b.graph_revision AND p.timeout_seconds=2 AND p.output_bytes=48000 AND i.status='succeeded' AND i.attempt_count=1`) != 1 || scalar(t, s, `SELECT count(*) FROM rsi_call WHERE instance_id='shell-tools'`) != 0 {
		t.Fatal("recipe not frozen before original effect receipt")
	}
	for _, sql := range []string{`UPDATE shell_method_plan SET timeout_seconds=120`, `DELETE FROM shell_method_plan`, `INSERT OR REPLACE INTO shell_method_plan SELECT * FROM shell_method_plan`} {
		if _, err := s.db.Exec(sql); err == nil {
			t.Fatal("accepted shell recipe mutable", sql)
		}
	}
}

func TestShellPluginRealCodeModeSuspendsAndReusesOneApprovedCommand(t *testing.T) {
	s, r, a, p, _ := codeFixture(t)
	defs := []tool.Definition{codemode.Definition()}
	for _, spec := range a.Registry.Describe() {
		d, _ := a.Registry.Resolve(spec.Name)
		defs = append(defs, d)
	}
	registry, err := tool.NewRegistry(defs)
	if err != nil {
		t.Fatal(err)
	}
	a.Registry, r.registry = registry, registry
	r.ConfigureJavaScript(realCodeRuntime(t))
	installShellPlugin(t, s, r)
	p.respond = delegateToolWork(func(req llm.GenerationRequest) (llm.GenerationResult, error) {
		if req.Messages[len(req.Messages)-1].Role == "tool" {
			return llm.GenerationResult{Text: "cell observed"}, nil
		}
		return calls(codemode.ToolName, `{"source":"return await tools.builtin__bash({command:\"printf once >> marker; printf 'original output'\"});"}`), nil
	})
	mustSubmit(t, a, "shell-cell", "Execute one approved command through the cell")
	mustDrain(t, a)
	if scalar(t, s, `SELECT count(*) FROM shell_method_plan`) != 0 {
		t.Fatal("cell bypassed shell approval")
	}
	approvePolicy(t, s, r)
	mustDrain(t, a)
	var directory string
	if err := s.db.QueryRow(`SELECT directory FROM task_code_environment`).Scan(&directory); err != nil {
		t.Fatal(err)
	}
	if data, err := os.ReadFile(filepath.Join(directory, "marker")); err != nil || string(data) != "once" {
		t.Fatal("VM replay repeated or lost command", string(data), err)
	}
	if scalar(t, s, `SELECT count(*) FROM shell_method_plan`) != 1 || scalar(t, s, `SELECT count(*) FROM tool_invocation WHERE tool_name='builtin__bash' AND status='succeeded' AND attempt_count=1`) != 1 {
		t.Fatal("shell command or recipe repeated")
	}
	var raw string
	if err := s.db.QueryRow(`SELECT result_json FROM tool_invocation WHERE tool_name=?`, codemode.ToolName).Scan(&raw); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(raw, "original output") {
		t.Fatal("cell lost original shell result", raw)
	}
	if err := r.Flush(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func TestShellPluginLateForgetRejectsApprovedCommandBeforeDispatch(t *testing.T) {
	for _, resume := range []bool{true, false} {
		name := "end-guard"
		if !resume {
			name = "transport-cancel"
		}
		t.Run(name, func(t *testing.T) { testShellPluginLateForget(t, resume) })
	}
}
func testShellPluginLateForget(t *testing.T, resume bool) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err = os.Chmod(root, 0700); err != nil {
		t.Fatal(err)
	}
	cfg := environment.Config{Root: root}
	calls := []llm.ToolCall{{ID: "private-command", Name: "builtin__bash", Arguments: `{"command":"printf PRIVATE_COMMAND_CANARY > MUST_NOT_RUN","timeout":7}`}}
	s, r, _, e, exchange := admitPolicyRun(t, environment.Definitions(environment.ResourceHash(cfg)), calls, false, func(s *Store, r *Runs) {
		r.ConfigureLocalEnvironment(cfg)
		h := installShellPlugin(t, s, r)
		status, err := h.Store.Status(ctx)
		if err != nil {
			t.Fatal(err)
		}
		selectShellVersion(t, h, status.Revision, "1.4.0")
	})
	if _, err = e.Execute(ctx, exchange, calls); !errors.Is(err, tool.ErrSuspended) {
		t.Fatal("command bypassed approval", err)
	}
	approvePolicy(t, s, r)
	owner := idAgent(t, s)
	jobs := t.TempDir()
	t.Setenv("TMPDIR", jobs)
	done := make(chan struct{})
	var readErr error
	go func() { defer close(done); _, readErr = e.Execute(ctx, exchange, calls) }()
	defer func() { cancel(); <-done }()
	tick := time.NewTicker(5 * time.Millisecond)
	defer tick.Stop()
	marker := ""
wait:
	for {
		select {
		case <-done:
			t.Fatal("command method exited before privacy boundary", readErr)
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		case <-tick.C:
			paths, _ := filepath.Glob(filepath.Join(jobs, "steward-rsi-*", "shell-ready"))
			if len(paths) == 1 {
				marker = paths[0]
				break wait
			}
		}
	}
	if err = s.Transaction(ctx, func(tx *sql.Tx) error { return r.revokeMemory(ctx, tx, owner) }); err != nil {
		t.Fatal("pure shell method blocked Forget", err)
	}
	// The existing transport watcher can cancel the child before this marker
	// write. Resume only while the process still owns its scratch directory.
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
	if !errors.Is(readErr, agent.ErrClaimLost) {
		t.Fatal("late shell privacy loss misclassified", readErr)
	}
	if scalar(t, s, `SELECT count(*) FROM tool_invocation WHERE tool_name='builtin__bash' AND status='succeeded'`) != 0 || scalar(t, s, `SELECT count(*) FROM shell_method_plan`) != 0 {
		t.Fatal("late shell choice or effect became accepted")
	}
	if _, err := os.Stat(filepath.Join(root, "MUST_NOT_RUN")); !os.IsNotExist(err) {
		t.Fatal("revoked approved command executed", err)
	}

}

func TestV60ShellMethodMigrationPreservesNativeCommandAndScope(t *testing.T) {
	s, r, a, p, _ := codeFixture(t)
	ctx := context.Background()
	p.respond = delegateToolWork(func(req llm.GenerationRequest) (llm.GenerationResult, error) {
		if req.Messages[len(req.Messages)-1].Role == "tool" {
			return llm.GenerationResult{Text: "native command observed"}, nil
		}
		return calls("builtin__bash", `{"command":"printf 'historical bytes' > native"}`), nil
	})
	mustSubmit(t, a, "native-shell-history", "Create original file")
	mustDrain(t, a)
	approvePolicy(t, s, r)
	mustDrain(t, a)
	snapshot := func(store *Store) string {
		t.Helper()
		var raw string
		if err := store.db.QueryRow(`SELECT json_object('tool',i.result_json,'attempts',i.attempt_count,'checkpoint',r.checkpoint_json,'version',r.version,'directory',e.directory,'scope',e.scope_hash) FROM tool_invocation i JOIN run r USING(run_id) JOIN task_code_environment e USING(task_id) WHERE i.tool_name='builtin__bash'`).Scan(&raw); err != nil {
			t.Fatal(err)
		}
		return raw
	}
	before := snapshot(s)
	var directory string
	if err := s.db.QueryRow(`SELECT directory FROM task_code_environment`).Scan(&directory); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec(removeComputerActionFixtureSchema + `DROP TABLE task_label_plan; DROP TABLE code_report_method_binding; DROP TABLE code_import_policy_binding; DROP TABLE shell_method_plan; PRAGMA user_version=60`); err != nil {
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
	if snapshot(next) != before || scalar(t, next, `SELECT count(*) FROM shell_method_plan`) != 0 {
		t.Fatal("upgrade rewrote original effects or opted in old calls")
	}
	if raw, err := os.ReadFile(filepath.Join(directory, "native")); err != nil || string(raw) != "historical bytes" {
		t.Fatal("historical workspace changed", string(raw), err)
	}
}

func TestShellPluginChoiceAndToolCommitFailuresDoNotReplayCommands(t *testing.T) {
	for _, boundary := range []string{"choice", "receipt"} {
		t.Run(boundary, func(t *testing.T) {
			s, r, a, p, _ := codeFixture(t)
			installShellPlugin(t, s, r)
			p.respond = delegateToolWork(func(req llm.GenerationRequest) (llm.GenerationResult, error) {
				if req.Messages[len(req.Messages)-1].Role == "tool" {
					return llm.GenerationResult{Text: "uncertainty observed"}, nil
				}
				return calls("builtin__bash", `{"command":"printf 'original effect' >> one-effect"}`), nil
			})
			mustSubmit(t, a, "shell-crash", "Perform one exact command")
			mustDrain(t, a)
			var directory string
			if err := s.db.QueryRow(`SELECT directory FROM task_code_environment`).Scan(&directory); err != nil {
				t.Fatal(err)
			}
			trigger := `CREATE TRIGGER reject_shell_accept BEFORE INSERT ON shell_method_plan BEGIN SELECT RAISE(ABORT,'injected shell choice failure'); END`
			if boundary == "receipt" {
				trigger = `CREATE TRIGGER reject_shell_accept BEFORE UPDATE ON tool_invocation WHEN OLD.tool_name='builtin__bash' AND NEW.status='succeeded' BEGIN SELECT RAISE(ABORT,'injected shell receipt failure'); END`
			}
			if _, err := s.db.Exec(trigger); err != nil {
				t.Fatal(err)
			}
			approvePolicy(t, s, r)
			if err := a.Drain(context.Background()); !errors.Is(err, agent.ErrStorage) {
				t.Fatal("failure boundary not exercised", err)
			}
			path := filepath.Join(directory, "one-effect")
			if boundary == "choice" {
				if _, err := os.Stat(path); !os.IsNotExist(err) || scalar(t, s, `SELECT count(*) FROM shell_method_plan`) != 0 {
					t.Fatal("uncommitted choice performed a write", err)
				}
			} else {
				if data, err := os.ReadFile(path); err != nil || string(data) != "original effect" || scalar(t, s, `SELECT count(*) FROM shell_method_plan`) != 1 {
					t.Fatal("effect or accepted method missing", string(data), err)
				}
			}
			if _, err := s.db.Exec(`DROP TRIGGER reject_shell_accept`); err != nil {
				t.Fatal(err)
			}
			mustDrain(t, a)
			if scalar(t, s, `SELECT count(*) FROM tool_invocation WHERE tool_name='builtin__bash' AND status='unknown' AND attempt_count=1`) != 1 {
				t.Fatal("unprovable command effect retried or claimed completion")
			}
			if boundary == "choice" {
				if _, err := os.Stat(path); !os.IsNotExist(err) {
					t.Fatal("recovery executed a previously uncommitted command", err)
				}
			} else if data, err := os.ReadFile(path); err != nil || string(data) != "original effect" {
				t.Fatal("recovery repeated an uncertain command", string(data), err)
			}
		})
	}
}

type managedShellGuest struct {
	environmentGuest
	local     environment.Local
	commands  int
	malformed bool
}

func (g *managedShellGuest) EnvironmentResourceHash() string { return "guest-shell-scope" }
func (g *managedShellGuest) ExecuteEnvironmentShell(ctx context.Context, i computer.Instance, c environment.ShellCommand) tool.Result {
	g.commands++
	g.seen = i
	result := g.local.ExecuteShell(ctx, c)
	if g.unknown && result.Error == nil {
		return tool.Failure("UNKNOWN_SIDE_EFFECT")
	}
	if g.malformed && result.Error == nil {
		return tool.Result{Text: `{"command":"printf once >> result.txt; printf guest-output","output":"guest-output","timed_out":false,"truncated":false}`}
	}
	return result
}
func TestShellPluginGuestKeepsOriginalApprovalEpochAndUnknownLifecycle(t *testing.T) {
	for _, mode := range []string{"completed", "unknown", "malformed"} {
		t.Run(mode, func(t *testing.T) {
			ctx := context.Background()
			calls := []llm.ToolCall{{ID: "command", Name: "builtin__bash", Arguments: `{"command":"printf once >> result.txt; printf guest-output"}`}}
			s, r, _, e, exchange := admitPolicyRun(t, environment.Definitions("guest-shell-scope"), calls, false, func(s *Store, r *Runs) { installShellPlugin(t, s, r) })
			root, err := filepath.EvalSymlinks(t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			if err = os.Chmod(root, 0700); err != nil {
				t.Fatal(err)
			}
			guest := &managedShellGuest{local: environment.Local{Config: environment.Config{Root: root}}, malformed: mode == "malformed"}
			guest.unknown = mode == "unknown"
			r.ConfigureComputer(guest, computer.Compatibility{ProviderVersion: "fixture"}, computer.RestorationManifest{Version: 1})
			if _, err = e.Execute(ctx, exchange, calls); !errors.Is(err, tool.ErrSuspended) {
				t.Fatal(err)
			}
			if guest.ensures != 0 || guest.commands != 0 {
				t.Fatal("unapproved Guest work", guest)
			}
			approvePolicy(t, s, r)
			results, err := e.Execute(ctx, exchange, calls)
			if err != nil || len(results) != 1 {
				t.Fatal(results, err)
			}
			if mode != "completed" && (results[0].Error == nil || results[0].Error.Code != "UNKNOWN_SIDE_EFFECT") {
				t.Fatal("uncertain Guest command became completion", results)
			}
			if mode == "completed" && (results[0].Error != nil || !strings.Contains(results[0].Text, "guest-output")) {
				t.Fatal(results)
			}
			if guest.calls != 0 || guest.commands != 1 || guest.seen != guest.instance || guest.checkpoints != 1 || guest.destroys != 1 {
				t.Fatal("native fallback or detached Computer lifecycle", guest)
			}
			path := filepath.Join(root, "result.txt")
			if data, err := os.ReadFile(path); err != nil || string(data) != "once" {
				t.Fatal("wrong original command effect", string(data), err)
			}
			if scalar(t, s, `SELECT count(*) FROM shell_method_plan`) != 1 || scalar(t, s, `SELECT count(*) FROM code_execution_state WHERE claim_token IS NULL AND state IN ('completed','failed')`) != 1 {
				t.Fatal("method not accepted or native claim still live")
			}
			if _, err = e.Execute(ctx, exchange, calls); err != nil || guest.commands != 1 {
				t.Fatal("completed/unknown command reexecuted", err, guest)
			}
			if data, err := os.ReadFile(path); err != nil || string(data) != "once" {
				t.Fatal("unknown command repeated effect", string(data), err)
			}
		})
	}
}

func TestShellPluginChangedLocalScopeCannotExecuteApprovedCommand(t *testing.T) {
	ctx := context.Background()
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	other, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	for _, dir := range []string{root, other} {
		if err = os.Chmod(dir, 0700); err != nil {
			t.Fatal(err)
		}
	}
	if err = os.WriteFile(filepath.Join(other, "secret"), []byte("OUT_OF_FROZEN_SCOPE"), 0600); err != nil {
		t.Fatal(err)
	}
	cfg := environment.Config{Root: root}
	calls := []llm.ToolCall{{ID: "command", Name: "builtin__bash", Arguments: `{"command":"cat secret; touch MUST_NOT_RUN"}`}}
	s, r, _, e, exchange := admitPolicyRun(t, environment.Definitions(environment.ResourceHash(cfg)), calls, false, func(s *Store, r *Runs) { r.ConfigureLocalEnvironment(cfg); installShellPlugin(t, s, r) })
	if _, err = e.Execute(ctx, exchange, calls); !errors.Is(err, tool.ErrSuspended) {
		t.Fatal(err)
	}
	approvePolicy(t, s, r)
	r.ConfigureLocalEnvironment(environment.Config{Root: other})
	results, err := e.Execute(ctx, exchange, calls)
	if err != nil || len(results) != 1 || results[0].Error == nil || results[0].Error.Code != "PERMISSION_DENIED" || strings.Contains(results[0].Text, "OUT_OF_FROZEN_SCOPE") {
		t.Fatal("scope drift dispatched original command", results, err)
	}
	for _, dir := range []string{root, other} {
		if _, err := os.Stat(filepath.Join(dir, "MUST_NOT_RUN")); !os.IsNotExist(err) {
			t.Fatal("scope drift executed command", err)
		}
	}
	if scalar(t, s, `SELECT count(*) FROM shell_method_plan`) != 0 {
		t.Fatal("scope drift accepted method")
	}

}

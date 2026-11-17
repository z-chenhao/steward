package sqlite

import (
	"bytes"
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
	"github.com/z-chenhao/assistant/internal/plugin/filetools/contract"
	"github.com/z-chenhao/assistant/internal/rsi"
	"github.com/z-chenhao/assistant/internal/tool"
	"github.com/z-chenhao/assistant/internal/tool/codemode"
	"github.com/z-chenhao/assistant/pkg/rsiplugin"
)

func installFilePlugin(t *testing.T, s *Store, r *Runs) rsi.Host {
	t.Helper()
	return installRuntimeFeature(t, s, r, "file-tools", rsi.Policy{ID: "file-owner-fixture", AllowedContracts: []string{contract.WindowContract, contract.EditContract}, AllowedRequests: []string{"input.read"}, Cases: []rsi.Case{
		{InstanceID: "file-tools", Contract: contract.WindowContract, Input: json.RawMessage(`{"input_id":"file","offset":1,"limit":2,"output_bytes":48000,"as_artifact":false}`), Inputs: []rsi.InputData{{ID: "file", Data: []byte("first\nsecond\n")}}, Expected: json.RawMessage(`{"artifact":false,"start":0,"end":13,"next_offset":3}`)},
		{InstanceID: "file-tools", Contract: contract.EditContract, Input: json.RawMessage(`{"input_id":"file","old_text":"OLD"}`), Inputs: []rsi.InputData{{ID: "file", Data: []byte("prefix OLD tail")}}, Expected: json.RawMessage(`{"start":7}`)},
	}})
}

type managedFileGuest struct {
	environmentGuest
	local              environment.Local
	snapshots, applies int
}

func (g *managedFileGuest) EnvironmentResourceHash() string { return "guest-file-scope" }
func (g *managedFileGuest) SnapshotEnvironmentFile(ctx context.Context, i computer.Instance, path string) (environment.FileSnapshot, *tool.Error) {
	g.snapshots++
	g.seen = i
	return g.local.SnapshotFile(ctx, path)
}
func (g *managedFileGuest) ApplyEnvironmentFile(ctx context.Context, i computer.Instance, c environment.FileChange) tool.Result {
	g.applies++
	g.seen = i
	result := g.local.ApplyFile(ctx, c)
	if g.unknown && result.Error == nil {
		return tool.Failure("UNKNOWN_SIDE_EFFECT")
	}
	return result
}

func TestFilePluginGuestKeepsOriginalComputerApprovalAndUnknownLifecycle(t *testing.T) {
	for _, unknown := range []bool{false, true} {
		name := "completed"
		if unknown {
			name = "unknown"
		}
		t.Run(name, func(t *testing.T) {
			ctx := context.Background()
			calls := []llm.ToolCall{{ID: "write", Name: "builtin__write", Arguments: `{"path":"result.txt","content":"one guest effect"}`}}
			s, r, _, e, exchange := admitPolicyRun(t, environment.Definitions("guest-file-scope"), calls, false, func(s *Store, r *Runs) { installFilePlugin(t, s, r) })
			root, err := filepath.EvalSymlinks(t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			if err = os.Chmod(root, 0700); err != nil {
				t.Fatal(err)
			}
			guest := &managedFileGuest{local: environment.Local{Config: environment.Config{Root: root}}}
			guest.unknown = unknown
			r.ConfigureComputer(guest, computer.Compatibility{ProviderVersion: "fixture"}, computer.RestorationManifest{Version: 1})
			if _, err = e.Execute(ctx, exchange, calls); !errors.Is(err, tool.ErrSuspended) {
				t.Fatal(err)
			}
			if guest.ensures != 0 || guest.snapshots != 0 || guest.applies != 0 {
				t.Fatal("unapproved Guest work", guest)
			}
			approvePolicy(t, s, r)
			results, err := e.Execute(ctx, exchange, calls)
			if err != nil || len(results) != 1 {
				t.Fatal(results, err)
			}
			if unknown && (results[0].Error == nil || results[0].Error.Code != "UNKNOWN_SIDE_EFFECT") {
				t.Fatal("uncertain Guest effect became completion", results)
			}
			if !unknown && results[0].Error != nil {
				t.Fatal(results)
			}
			if guest.calls != 0 || guest.snapshots != 1 || guest.applies != 1 || guest.seen != guest.instance || guest.checkpoints != 1 || guest.destroys != 1 {
				t.Fatal("native fallback or detached Computer lifecycle", guest)
			}
			if data, err := os.ReadFile(filepath.Join(root, "result.txt")); err != nil || string(data) != "one guest effect" {
				t.Fatal("Guest exact bytes missing", string(data), err)
			}
			if scalar(t, s, `SELECT count(*) FROM file_method_plan`) != 1 || scalar(t, s, `SELECT count(*) FROM code_execution_state WHERE claim_token IS NULL AND state IN ('completed','failed')`) != 1 {
				t.Fatal("missing accepted method or live native claim")
			}
			if _, err = e.Execute(ctx, exchange, calls); err != nil || guest.applies != 1 || guest.snapshots != 1 {
				t.Fatal("completed/unknown effect reexecuted", err, guest)
			}
		})
	}
}

func TestV59FileMethodMigrationPreservesNativeEffectsAndScope(t *testing.T) {
	s, r, a, p, _ := codeFixture(t)
	ctx := context.Background()
	p.respond = delegateToolWork(func(req llm.GenerationRequest) (llm.GenerationResult, error) {
		if req.Messages[len(req.Messages)-1].Role == "tool" {
			return llm.GenerationResult{Text: "native write observed"}, nil
		}
		return calls("builtin__write", `{"path":"native","content":"historical bytes"}`), nil
	})
	mustSubmit(t, a, "native-file-history", "Create original file")
	mustDrain(t, a)
	approvePolicy(t, s, r)
	mustDrain(t, a)
	snapshot := func(store *Store) string {
		t.Helper()
		var raw string
		if err := store.db.QueryRow(`SELECT json_object('tool',i.result_json,'attempts',i.attempt_count,'checkpoint',r.checkpoint_json,'version',r.version,'directory',e.directory,'scope',e.scope_hash) FROM tool_invocation i JOIN run r USING(run_id) JOIN task_code_environment e USING(task_id) WHERE i.tool_name='builtin__write'`).Scan(&raw); err != nil {
			t.Fatal(err)
		}
		return raw
	}
	before := snapshot(s)
	var directory string
	if err := s.db.QueryRow(`SELECT directory FROM task_code_environment`).Scan(&directory); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec(removeComputerActionFixtureSchema + `DROP TABLE task_label_plan; DROP TABLE code_report_method_binding; DROP TABLE code_import_policy_binding; DROP TABLE shell_method_plan; DROP TABLE file_method_plan; PRAGMA user_version=59`); err != nil {
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
	if snapshot(next) != before || scalar(t, next, `SELECT count(*) FROM file_method_plan`) != 0 {
		t.Fatal("upgrade rewrote original effects or opted in old calls")
	}
	if raw, err := os.ReadFile(filepath.Join(directory, "native")); err != nil || string(raw) != "historical bytes" {
		t.Fatal("historical workspace changed", string(raw), err)
	}
}

func TestFilePluginChoiceAndToolCommitFailuresDoNotReplayMutations(t *testing.T) {
	for _, boundary := range []string{"choice", "receipt"} {
		t.Run(boundary, func(t *testing.T) {
			s, r, a, p, _ := codeFixture(t)
			installFilePlugin(t, s, r)
			p.respond = delegateToolWork(func(req llm.GenerationRequest) (llm.GenerationResult, error) {
				if req.Messages[len(req.Messages)-1].Role == "tool" {
					return llm.GenerationResult{Text: "uncertainty observed"}, nil
				}
				return calls("builtin__write", `{"path":"one-effect","content":"original effect"}`), nil
			})
			mustSubmit(t, a, "file-crash", "Perform one exact write")
			mustDrain(t, a)
			var directory string
			if err := s.db.QueryRow(`SELECT directory FROM task_code_environment`).Scan(&directory); err != nil {
				t.Fatal(err)
			}
			trigger := `CREATE TRIGGER reject_file_accept BEFORE INSERT ON file_method_plan BEGIN SELECT RAISE(ABORT,'injected file choice failure'); END`
			if boundary == "receipt" {
				trigger = `CREATE TRIGGER reject_file_accept BEFORE UPDATE ON tool_invocation WHEN OLD.tool_name='builtin__write' AND NEW.status='succeeded' BEGIN SELECT RAISE(ABORT,'injected file receipt failure'); END`
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
				if _, err := os.Stat(path); !os.IsNotExist(err) || scalar(t, s, `SELECT count(*) FROM file_method_plan`) != 0 {
					t.Fatal("uncommitted choice performed a write", err)
				}
			} else {
				if data, err := os.ReadFile(path); err != nil || string(data) != "original effect" || scalar(t, s, `SELECT count(*) FROM file_method_plan`) != 1 {
					t.Fatal("effect or accepted method missing", string(data), err)
				}
			}
			if _, err := s.db.Exec(`DROP TRIGGER reject_file_accept`); err != nil {
				t.Fatal(err)
			}
			mustDrain(t, a)
			if scalar(t, s, `SELECT count(*) FROM tool_invocation WHERE tool_name='builtin__write' AND status='unknown' AND attempt_count=1`) != 1 {
				t.Fatal("unprovable file effect retried or claimed completion")
			}
			if boundary == "choice" {
				if _, err := os.Stat(path); !os.IsNotExist(err) {
					t.Fatal("recovery wrote a previously uncommitted change", err)
				}
			}
		})
	}
}

func TestFilePluginLateForgetRejectsReadBeforeEvidenceAcceptance(t *testing.T) {
	for _, resume := range []bool{true, false} {
		name := "end-guard"
		if !resume {
			name = "transport-cancel"
		}
		t.Run(name, func(t *testing.T) { testFilePluginLateForget(t, resume) })
	}
}
func testFilePluginLateForget(t *testing.T, resume bool) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err = os.Chmod(root, 0700); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(root, "private"), []byte("PRIVATE_FILE_HOLD\noriginal evidence"), 0600); err != nil {
		t.Fatal(err)
	}
	cfg := environment.Config{Root: root}
	calls := []llm.ToolCall{{ID: "private-read", Name: "builtin__read", Arguments: `{"path":"private"}`}}
	s, r, _, e, exchange := admitPolicyRun(t, environment.Definitions(environment.ResourceHash(cfg)), calls, false, func(s *Store, r *Runs) {
		r.ConfigureLocalEnvironment(cfg)
		h := installFilePlugin(t, s, r)
		status, err := h.Store.Status(ctx)
		if err != nil {
			t.Fatal(err)
		}
		selectFileVersion(t, h, status.Revision, "1.3.0")
	})
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
			t.Fatal("read exited before privacy boundary", readErr)
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		case <-tick.C:
			paths, _ := filepath.Glob(filepath.Join(jobs, "steward-rsi-*", "file-ready"))
			if len(paths) == 1 {
				marker = paths[0]
				break wait
			}
		}
	}
	if err = s.Transaction(ctx, func(tx *sql.Tx) error { return r.revokeMemory(ctx, tx, owner) }); err != nil {
		t.Fatal("pure file method blocked Forget", err)
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
		t.Fatal("late privacy loss misclassified", readErr)
	}
	if scalar(t, s, `SELECT count(*) FROM tool_invocation WHERE tool_name='builtin__read' AND status='succeeded'`) != 0 || scalar(t, s, `SELECT count(*) FROM file_method_plan`) != 0 {
		t.Fatal("late file evidence became accepted")
	}
}

func TestFilePluginFullInputCrossesFramesWithoutExpandingEvidence(t *testing.T) {
	for _, binary := range []bool{false, true} {
		name := "text"
		if binary {
			name = "binary"
		}
		t.Run(name, func(t *testing.T) {
			s, r, a, p, cfg := codeFixture(t)
			data := []byte(strings.Repeat("line\n", 300000))
			if binary {
				data = bytes.Repeat([]byte{255}, 8<<20)
			}
			if err := os.WriteFile(filepath.Join(cfg.RepositorySource, "file.txt"), data, 0600); err != nil {
				t.Fatal(err)
			}
			installFilePlugin(t, s, r)
			p.respond = delegateToolWork(func(req llm.GenerationRequest) (llm.GenerationResult, error) {
				if req.Messages[len(req.Messages)-1].Role == "tool" {
					return llm.GenerationResult{Text: "bounded read observed"}, nil
				}
				return calls("builtin__read", `{"path":"file.txt","limit":1}`), nil
			})
			mustSubmit(t, a, "large-file", "Read a bounded file window")
			mustDrain(t, a)
			var raw string
			if err := s.db.QueryRow(`SELECT result_json FROM tool_invocation WHERE tool_name='builtin__read'`).Scan(&raw); err != nil {
				t.Fatal(err)
			}
			var result tool.Result
			if json.Unmarshal([]byte(raw), &result) != nil || result.Error != nil {
				t.Fatal("legal full input was rejected", raw)
			}
			if binary {
				if len(result.ArtifactRefs) != 1 {
					t.Fatal("binary bytes were not retained as scoped artifact", raw)
				}
			} else {
				var read struct {
					Text      string
					Next      int `json:"next_offset"`
					Truncated bool
				}
				if json.Unmarshal([]byte(result.Text), &read) != nil || read.Text != "line\n" || read.Next != 2 || !read.Truncated {
					t.Fatal("complete input expanded or lost evidence bounds", raw)
				}
			}
			if scalar(t, s, `SELECT count(*) FROM rsi_call WHERE instance_id='file-tools'`) != 0 {
				t.Fatal("readonly bytes entered a second effect ledger")
			}
		})
	}
}

func TestFilePluginChangedLocalScopeCannotReleaseBytes(t *testing.T) {
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
	calls := []llm.ToolCall{{ID: "read", Name: "builtin__read", Arguments: `{"path":"secret"}`}}
	_, r, _, e, exchange := admitPolicyRun(t, environment.Definitions(environment.ResourceHash(cfg)), calls, false, func(s *Store, r *Runs) { r.ConfigureLocalEnvironment(cfg); installFilePlugin(t, s, r) })
	r.ConfigureLocalEnvironment(environment.Config{Root: other})
	results, err := e.Execute(ctx, exchange, calls)
	if err != nil || len(results) != 1 || results[0].Error == nil || results[0].Error.Code != "PERMISSION_DENIED" || strings.Contains(results[0].Text, "OUT_OF_FROZEN_SCOPE") {
		t.Fatal("scope drift released file bytes", results, err)
	}
}

func fileVersionPackage(t *testing.T, version string) string {
	t.Helper()
	source := t.TempDir()
	path, err := filepath.Abs("../../plugin/filetools/plugin.go")
	if err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	code := strings.Replace(string(raw), `Version: "1.0.1"`, `Version: "`+version+`"`, 1)
	needle := `return rsiplugin.JSONResult(ctx, selectWindow(data, f))`
	if version == "1.1.0" {
		code = strings.Replace(code, needle, `f.Limit=min(f.Limit,1);`+needle, 1)
	} else if version == "1.2.0" {
		code = strings.Replace(code, needle, `p:=selectWindow(data,f);if !p.Artifact{p.Start++};return rsiplugin.JSONResult(ctx,p)`, 1)
	} else if version == "1.3.0" {
		code = strings.Replace(code, `"context"`, `"context";"os";"time"`, 1)
		code = strings.Replace(code, needle, `if bytes.Contains(data,[]byte("PRIVATE_FILE_HOLD")){if err:=os.WriteFile("file-ready",[]byte("ready"),0600);err!=nil{return rsiplugin.Result{},err};for{if _,err:=os.Stat("resume");err==nil{break};select{case<-ctx.Done():return rsiplugin.Result{},ctx.Err();case<-time.After(time.Millisecond):}}};`+needle, 1)
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
	if out, err := exec.Command("go", "build", "-overlay", file, "-o", filepath.Join(source, "plugin"), "../../../cmd/plugins/file-tools").CombinedOutput(); err != nil {
		t.Fatal(string(out), err)
	}
	raw, err = os.ReadFile("../../../plugins/file-tools/steward.plugin.json")
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

func selectFileVersion(t *testing.T, h rsi.Host, baseline, version string) (string, string) {
	t.Helper()
	ctx := context.Background()
	pkg, err := h.Install(ctx, fileVersionPackage(t, version))
	if err != nil {
		t.Fatal(err)
	}
	graph, err := h.Prepare(ctx, rsiplugin.Graph{Protocol: rsiplugin.Protocol, Bindings: []rsiplugin.Binding{{InstanceID: "file-tools", PackageDigest: pkg.Digest, Config: json.RawMessage(`{}`), StateNamespace: "file-tools", StateVersion: 1}}})
	if err != nil {
		t.Fatal(err)
	}
	policy, err := h.Store.Policy(ctx, "file-owner-fixture")
	if err != nil {
		t.Fatal(err)
	}
	policy.ID = "file-owner-" + version
	var expected contract.Window
	if err = json.Unmarshal(policy.Cases[0].Expected, &expected); err != nil {
		t.Fatal(err)
	}
	if version == "1.1.0" {
		expected.End, expected.NextOffset = 6, 2
	} else if version == "1.2.0" {
		expected.Start++
	}
	policy.Cases[0].Expected, _ = json.Marshal(expected)
	if err = h.Store.PutPolicy(ctx, policy); err != nil {
		t.Fatal(err)
	}
	activateDelegation(t, h, "file-upgrade-"+version, baseline, graph.Revision, policy.ID)
	return graph.Revision, pkg.Digest
}

func TestFilePluginIndependentUpgradeRetainedReadsRollbackAndDisable(t *testing.T) {
	s, r, a, p, cfg := codeFixture(t)
	ctx := context.Background()
	if err := os.WriteFile(filepath.Join(cfg.RepositorySource, "file.txt"), []byte("first\nsecond\n"), 0600); err != nil {
		t.Fatal(err)
	}
	r.ConfigurePlugins(rsi.NewCatalog(filepath.Dir(s.path)))
	p.respond = delegateToolWork(func(req llm.GenerationRequest) (llm.GenerationResult, error) {
		if req.Messages[len(req.Messages)-1].Role == "tool" {
			return llm.GenerationResult{Text: "read observed"}, nil
		}
		return calls("builtin__read", `{"path":"file.txt"}`), nil
	})
	queue := func(key string) string {
		t.Helper()
		receipt := mustSubmit(t, a, key, "Read the requested file")
		if err := executeTaskRunID(t, a, r, receipt.RunID); err != nil {
			t.Fatal(err)
		}
		var id string
		if err := s.db.QueryRow(`SELECT run_id FROM run WHERE worker_position='root' AND status='queued' ORDER BY created_at DESC LIMIT 1`).Scan(&id); err != nil {
			t.Fatal(err)
		}
		return id
	}
	check := func(id, expected string) {
		t.Helper()
		var raw string
		if err := s.db.QueryRow(`SELECT result_json FROM tool_invocation WHERE run_id=? AND tool_name='builtin__read'`, id).Scan(&raw); err != nil {
			t.Fatal(err)
		}
		var result tool.Result
		var read struct{ Text string }
		if json.Unmarshal([]byte(raw), &result) != nil || result.Error != nil || json.Unmarshal([]byte(result.Text), &read) != nil || read.Text != expected {
			t.Fatal("retained file method changed", id, raw, expected)
		}
	}
	legacy := queue("file-native")
	h := installFilePlugin(t, s, r)
	first, err := h.Store.Status(ctx)
	if err != nil {
		t.Fatal(err)
	}
	mustDrain(t, a)
	check(legacy, "first\nsecond\n")
	old := queue("file-old")
	second, pkg := selectFileVersion(t, h, first.Revision, "1.1.0")
	mustDrain(t, a)
	check(old, "first\nsecond\n")
	current := queue("file-new")
	mustDrain(t, a)
	check(current, "first\n")
	retained := queue("file-retained")
	activateDelegation(t, h, "file-rollback", second, first.Revision, "file-owner-fixture")
	if err = h.Store.Retire(ctx, pkg); err != nil {
		t.Fatal(err)
	}
	mustDrain(t, a)
	check(retained, "first\n")
	rolled := queue("file-rolled")
	mustDrain(t, a)
	check(rolled, "first\nsecond\n")
	if _, err = h.Store.Deactivate(ctx, "file-disable", first.Revision); err != nil {
		t.Fatal(err)
	}
	disabled := queue("file-disabled")
	mustDrain(t, a)
	if scalar(t, s, `SELECT count(*) FROM tool_invocation WHERE run_id=? AND tool_name='builtin__read' AND json_extract(result_json,'$.error.code')='PROVIDER_UNAVAILABLE'`, disabled) != 1 {
		t.Fatal("disabled file module restored native read")
	}
}

func TestFilePluginInvalidByteWindowCannotBecomeEvidence(t *testing.T) {
	s, r, a, p, _ := codeFixture(t)
	ctx := context.Background()
	h := installFilePlugin(t, s, r)
	status, err := h.Store.Status(ctx)
	if err != nil {
		t.Fatal(err)
	}
	selectFileVersion(t, h, status.Revision, "1.2.0")
	p.respond = delegateToolWork(func(req llm.GenerationRequest) (llm.GenerationResult, error) {
		if req.Messages[len(req.Messages)-1].Role == "tool" {
			return llm.GenerationResult{Text: "denial observed"}, nil
		}
		return calls("builtin__read", `{"path":"file.txt"}`), nil
	})
	mustSubmit(t, a, "file-bad-window", "Read original bytes")
	mustDrain(t, a)
	if scalar(t, s, `SELECT count(*) FROM tool_invocation WHERE tool_name='builtin__read' AND json_extract(result_json,'$.error.code')='PROVIDER_UNAVAILABLE' AND json_extract(result_json,'$.text')=''`) != 1 {
		t.Fatal("candidate coordinates fabricated or skipped evidence")
	}
}

func TestFilePluginActualEditKeepsSourceAndDirtyBytes(t *testing.T) {
	s, r, a, p, cfg := codeFixture(t)
	before := "前缀\nOLD\nuser dirty tail\n"
	if err := os.WriteFile(filepath.Join(cfg.RepositorySource, "file.txt"), []byte(before), 0700); err != nil {
		t.Fatal(err)
	}
	installFilePlugin(t, s, r)
	fallback := 0
	r.localExecute = func(context.Context, environment.Config, string, string) tool.Result {
		fallback++
		return tool.Failure("PROVIDER_UNAVAILABLE")
	}
	p.respond = delegateToolWork(func(req llm.GenerationRequest) (llm.GenerationResult, error) {
		if req.Messages[len(req.Messages)-1].Role == "tool" {
			return llm.GenerationResult{Text: "edit observed"}, nil
		}
		raw, _ := json.Marshal(map[string]string{"path": "file.txt", "before_hash": digest(before), "old_text": "OLD", "new_text": "NEW"})
		return calls("builtin__edit", string(raw)), nil
	})
	mustSubmit(t, a, "managed-edit", "Replace one exact occurrence")
	mustDrain(t, a)
	approvePolicy(t, s, r)
	mustDrain(t, a)
	var directory string
	if err := s.db.QueryRow(`SELECT directory FROM task_code_environment`).Scan(&directory); err != nil {
		t.Fatal(err)
	}
	if data, err := os.ReadFile(filepath.Join(directory, "file.txt")); err != nil || string(data) != "前缀\nNEW\nuser dirty tail\n" {
		t.Fatal("managed edit did not apply accepted span", string(data), err)
	}
	if data, err := os.ReadFile(filepath.Join(cfg.RepositorySource, "file.txt")); err != nil || string(data) != before {
		t.Fatal("source repository changed", string(data), err)
	}
	if fallback != 0 || scalar(t, s, `SELECT count(*) FROM file_method_plan WHERE span_start=? AND source_hash=?`, len("前缀\n"), digest(before)) != 1 {
		t.Fatal("native fallback or missing accepted span", fallback)
	}
}

func TestFilePluginRealCodeModeUsesOriginalReadReceiptAndOneApprovedEdit(t *testing.T) {
	s, r, a, p, cfg := codeFixture(t)
	if err := os.WriteFile(filepath.Join(cfg.RepositorySource, "file.txt"), []byte("prefix OLD dirty tail\n"), 0600); err != nil {
		t.Fatal(err)
	}
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
	installFilePlugin(t, s, r)
	p.respond = delegateToolWork(func(req llm.GenerationRequest) (llm.GenerationResult, error) {
		if req.Messages[len(req.Messages)-1].Role == "tool" {
			return llm.GenerationResult{Text: "script observed"}, nil
		}
		source := `const original=await tools.builtin__read({path:"file.txt"});await tools.builtin__edit({path:"file.txt",before_hash:original.hash,old_text:"OLD",new_text:"NEW"});return await tools.builtin__read({path:"file.txt"});`
		raw, _ := json.Marshal(map[string]string{"source": source})
		return calls(codemode.ToolName, string(raw)), nil
	})
	mustSubmit(t, a, "managed-file-cell", "Read then perform the exact edit")
	mustDrain(t, a)
	if scalar(t, s, `SELECT count(*) FROM file_method_plan`) != 0 {
		t.Fatal("VM bypassed approval")
	}
	approvePolicy(t, s, r)
	mustDrain(t, a)
	if scalar(t, s, `SELECT count(*) FROM tool_invocation WHERE tool_name IN ('builtin__read','builtin__edit') AND status='succeeded' AND attempt_count=1`) != 3 || scalar(t, s, `SELECT count(*) FROM file_method_plan`) != 1 {
		t.Fatal("VM reconstruction repeated or lost file effects")
	}
	var raw string
	if err := s.db.QueryRow(`SELECT result_json FROM tool_invocation WHERE tool_name=?`, codemode.ToolName).Scan(&raw); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(raw, "prefix NEW dirty tail") {
		t.Fatal("script did not read actual committed bytes", raw)
	}
}

func TestFilePluginActualTaskReadUsesPureWindowAndOriginalFacts(t *testing.T) {
	s, r, a, p, cfg := codeFixture(t)
	if err := os.WriteFile(filepath.Join(cfg.RepositorySource, "file.txt"), []byte("first\r\n第二行\nlast"), 0600); err != nil {
		t.Fatal(err)
	}
	installFilePlugin(t, s, r)
	p.respond = delegateToolWork(func(req llm.GenerationRequest) (llm.GenerationResult, error) {
		if req.Messages[len(req.Messages)-1].Role == "tool" {
			return llm.GenerationResult{Text: "read observed"}, nil
		}
		return calls("builtin__read", `{"path":"file.txt","offset":2,"limit":1}`), nil
	})
	mustSubmit(t, a, "managed-file-read", "Read one authorized line")
	mustDrain(t, a)
	var raw string
	if err := s.db.QueryRow(`SELECT result_json FROM tool_invocation WHERE tool_name='builtin__read'`).Scan(&raw); err != nil {
		t.Fatal(err)
	}
	var result tool.Result
	var read struct {
		Path, Text, Hash string
		Offset           int
		Next             int `json:"next_offset"`
		Truncated        bool
	}
	if json.Unmarshal([]byte(raw), &result) != nil || result.Error != nil || json.Unmarshal([]byte(result.Text), &read) != nil || read.Path != "file.txt" || read.Text != "第二行\n" || read.Offset != 2 || read.Next != 3 || !read.Truncated {
		t.Fatal("managed read changed original fields", raw)
	}
	if scalar(t, s, `SELECT count(*) FROM rsi_call WHERE instance_id='file-tools'`) != 0 || scalar(t, s, `SELECT count(*) FROM file_method_plan`) != 0 {
		t.Fatal("pure read wrote another effect ledger")
	}
}

func TestFilePluginActualWriteAcceptsPlanOnlyAfterExactApproval(t *testing.T) {
	s, r, a, p, _ := codeFixture(t)
	installFilePlugin(t, s, r)
	p.respond = delegateToolWork(func(req llm.GenerationRequest) (llm.GenerationResult, error) {
		if req.Messages[len(req.Messages)-1].Role == "tool" {
			return llm.GenerationResult{Text: "write observed"}, nil
		}
		return calls("builtin__write", `{"path":"new.txt","content":"approved original bytes"}`), nil
	})
	mustSubmit(t, a, "managed-file-write", "Create exactly the requested file")
	mustDrain(t, a)
	if scalar(t, s, `SELECT count(*) FROM file_method_plan`) != 0 || scalar(t, s, `SELECT count(*) FROM tool_invocation WHERE tool_name='builtin__write' AND status='waiting' AND attempt_count=0`) != 1 {
		t.Fatal("file plan or effect preceded approval")
	}
	var directory string
	if err := s.db.QueryRow(`SELECT directory FROM task_code_environment`).Scan(&directory); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(directory, "new.txt")); !os.IsNotExist(err) {
		t.Fatal("write happened before approval", err)
	}
	approvePolicy(t, s, r)
	if err := a.Drain(context.Background()); err != nil {
		for cause := err; cause != nil; cause = errors.Unwrap(cause) {
			t.Logf("%T: %v", cause, cause)
		}
		t.Fatal(err)
	}
	if data, err := os.ReadFile(filepath.Join(directory, "new.txt")); err != nil || string(data) != "approved original bytes" {
		t.Fatal("wrong scope or approved content changed", string(data), err)
	}
	if scalar(t, s, `SELECT count(*) FROM file_method_plan p JOIN tool_invocation i USING(invocation_id) JOIN rsi_run b USING(run_id) WHERE p.graph_revision=b.graph_revision AND p.span_start IS NULL AND p.source_hash IS NULL AND i.status='succeeded' AND i.attempt_count=1`) != 1 {
		t.Fatal("choice did not freeze before original successful file receipt")
	}
	for _, sql := range []string{`UPDATE file_method_plan SET driver_hash='changed'`, `DELETE FROM file_method_plan`, `INSERT OR REPLACE INTO file_method_plan SELECT * FROM file_method_plan`} {
		if _, err := s.db.Exec(sql); err == nil {
			t.Fatal("file choice mutable", sql)
		}
	}
	if err := r.Flush(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func TestFilePluginWindowRejectsFabricatedOrSkippedEvidence(t *testing.T) {
	data := []byte("first\n第二行\nlast")
	f := contract.WindowFacts{Offset: 2, Limit: 1, OutputBytes: 48000}
	good := contract.Window{Start: 6, End: 16, NextOffset: 3}
	// The second line is nine UTF-8 bytes plus a newline.
	if !validFileWindow(data, f, good) {
		t.Fatal("valid source coordinates rejected")
	}
	for _, p := range []contract.Window{{Start: 0, End: 6, NextOffset: 3}, {Start: 6, End: len(data), NextOffset: 4}, {Start: 6, End: 15, NextOffset: 3}, {Start: 6, End: 16, NextOffset: 4}, {Start: 6, End: 6, NextOffset: 3}} {
		if validFileWindow(data, f, p) {
			t.Fatal("untrusted coordinates became evidence", p)
		}
	}
	if !strings.Contains(string(data[good.Start:good.End]), "第二行") {
		t.Fatal("fixture wrong")
	}
}

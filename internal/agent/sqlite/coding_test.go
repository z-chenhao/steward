package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/z-chenhao/assistant/internal/agent"
	"github.com/z-chenhao/assistant/internal/coding"
	"github.com/z-chenhao/assistant/internal/computer/environment"
	"github.com/z-chenhao/assistant/internal/computer/sandbox"
	"github.com/z-chenhao/assistant/internal/llm"
	"github.com/z-chenhao/assistant/internal/rsi"
	"github.com/z-chenhao/assistant/internal/task"
	"github.com/z-chenhao/assistant/internal/tool"
	"golang.org/x/sys/unix"
)

func codeFixture(t *testing.T) (*Store, *Runs, agent.Actor, *loopProvider, environment.Config) {
	t.Helper()
	s, r, a, p := taskFixture(t)
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	source := filepath.Join(root, "source")
	if err := os.MkdirAll(filepath.Join(source, ".git"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(source, "file.txt"), []byte("dirty source"), 0600); err != nil {
		t.Fatal(err)
	}
	cfg := environment.Config{Root: filepath.Join(root, "execution"), RepositorySource: source}
	echo, _ := tool.FixtureRegistry().Resolve("mcp__fixture__echo")
	defs := append(task.Definitions(), echo, agent.AgentToolDefinition())
	defs = append(defs, environment.Definitions(environment.ResourceHash(cfg))...)
	a.Registry, err = tool.NewRegistry(defs)
	if err != nil {
		t.Fatal(err)
	}
	r = s.Runs(r.history, a.Registry)
	r.ConfigureLocalEnvironment(cfg)
	a.Store = r
	return s, r, a, p, cfg
}

func TestTaskCodePreparationAndFailureStayScoped(t *testing.T) {
	s, _, a, p, cfg := codeFixture(t)
	ctx := context.Background()
	p.respond = func(req llm.GenerationRequest) (llm.GenerationResult, error) {
		if isRoot(req) || isFollowup(req) || req.Messages[len(req.Messages)-1].Role == "tool" {
			return llm.GenerationResult{Text: "observed"}, nil
		}
		return calls(task.ManageName, `{"operation":"create","task":"prepare code"}`), nil
	}
	mustSubmit(t, a, "one", "first task")
	mustDrain(t, a)
	var ref string
	if err := s.db.QueryRow(`SELECT task_ref FROM task`).Scan(&ref); err != nil {
		t.Fatal(err)
	}
	w, err := s.CodeReport(ctx, ref, false)
	if err != nil || w.Status != "ready" {
		t.Fatal(w, err)
	}
	// New source failure affects only the next Task's Root; the existing copy
	// and original Task remain available and the Actor still drains follow-ups.
	if err := os.Remove(filepath.Join(cfg.RepositorySource, ".git")); err != nil {
		t.Fatal(err)
	}
	mustSubmit(t, a, "two", "second task")
	mustDrain(t, a)
	if scalar(t, s, `SELECT count(*) FROM task_code_environment WHERE status='ready'`) != 1 || scalar(t, s, `SELECT count(*) FROM task_code_environment WHERE status='failed'`) != 1 || scalar(t, s, `SELECT count(*) FROM run WHERE kind='worker' AND status='failed'`) != 1 {
		t.Fatal("failed preparation escaped Task boundary")
	}
	other, err := Open(ctx, filepath.Dir(s.path))
	if err != nil {
		t.Fatal(err)
	}
	defer other.Close()
	again, err := other.CodeReport(ctx, ref, false)
	if err != nil || again.Directory != w.Directory || again.BaselineHash != w.BaselineHash {
		t.Fatal("restart lost prepared directory", again, err)
	}
}

func TestTaskNativeDispatchUsesItsFrozenDirectory(t *testing.T) {
	s, r, a, p, cfg := codeFixture(t)
	// Only the external execution peer is replaced. Task create, frozen tools,
	// approvals, invocation dispatch and directory preparation are production.
	seen := ""
	r.localExecute = func(ctx context.Context, got environment.Config, name, args string) tool.Result {
		seen = got.Root
		if got.Root == cfg.Root {
			return tool.Failure("PERMISSION_DENIED")
		}
		if name == "builtin__bash" {
			const command = "test -f result.txt && exit 7"
			var request struct {
				Command string `json:"command"`
			}
			if json.Unmarshal([]byte(args), &request) != nil || request.Command != command {
				return tool.Failure("INVALID_ARGUMENT")
			}
			cmd, err := sandbox.Command(ctx, "/bin/bash", []string{"-c", command}, nil, got.Root, got.Root)
			if err != nil {
				return tool.Failure("PROVIDER_UNAVAILABLE")
			}
			cmd.Dir, cmd.Env = got.Root, []string{"PATH=/usr/bin:/bin"}
			started, _ := sandbox.Run(cmd, unix.SIGTERM)
			if !started || cmd.ProcessState == nil {
				return tool.Failure("PROVIDER_UNAVAILABLE")
			}
			return tool.Result{Text: fmt.Sprintf(`{"exit_code":%d}`, cmd.ProcessState.ExitCode())}
		}
		if name != "builtin__write" {
			return tool.Failure("INVALID_ARGUMENT")
		}
		var input struct {
			Path    string `json:"path"`
			Content string `json:"content"`
		}
		if json.Unmarshal([]byte(args), &input) != nil {
			return tool.Failure("INVALID_ARGUMENT")
		}
		if err := os.WriteFile(filepath.Join(got.Root, input.Path), []byte(input.Content), 0600); err != nil {
			return tool.Failure("PROVIDER_UNAVAILABLE")
		}
		return tool.Result{Text: `{"bytes":8}`}
	}
	p.respond = func(req llm.GenerationRequest) (llm.GenerationResult, error) {
		last := req.Messages[len(req.Messages)-1]
		if isRoot(req) {
			if last.Role == "tool" {
				if strings.Contains(last.Text, `"bytes":8`) {
					return calls("builtin__bash", `{"command":"test -f result.txt && exit 7"}`), nil
				}
				return llm.GenerationResult{Text: "done"}, nil
			}
			return calls("builtin__write", `{"path":"result.txt","content":"approved"}`), nil
		}
		if isFollowup(req) || last.Role == "tool" {
			return llm.GenerationResult{Text: "accepted"}, nil
		}
		return calls(task.ManageName, `{"operation":"create","task":"code work"}`), nil
	}
	mustSubmit(t, a, "write", "start code task")
	mustDrain(t, a)
	if seen != "" {
		t.Fatal("write dispatched before approval")
	}
	approvePolicy(t, s, r)
	mustDrain(t, a)
	var ref string
	if err := s.db.QueryRow(`SELECT task_ref FROM task`).Scan(&ref); err != nil {
		t.Fatal(err)
	}
	waiting, err := s.CodeReport(context.Background(), ref, false)
	if err != nil || len(waiting.Commands) != 1 || waiting.Commands[0].Attempts != 0 || waiting.Commands[0].Status != "waiting" || string(waiting.Commands[0].Result) != "null" {
		t.Fatal("unapproved command appeared executed", waiting, err)
	}
	pending, err := r.PendingApprovals(context.Background())
	if err != nil || len(pending) != 1 || pending[0].Tool != "builtin__bash" {
		t.Fatal(pending, err)
	}
	if err = r.Decide(context.Background(), idAgent(t, s), pending[0].ID, pending[0].Hash, "approved"); err != nil {
		t.Fatal(err)
	}
	mustDrain(t, a)
	executed, err := s.CodeReport(context.Background(), ref, false)
	if err != nil || len(executed.Commands) != 1 || executed.Commands[0].Attempts != 1 || executed.Commands[0].Status != "succeeded" {
		t.Fatal("actual command receipt was not retained", executed, err)
	}
	var outcome struct {
		Text string `json:"text"`
	}
	var bash struct {
		ExitCode int `json:"exit_code"`
	}
	if json.Unmarshal(executed.Commands[0].Result, &outcome) != nil || json.Unmarshal([]byte(outcome.Text), &bash) != nil || bash.ExitCode != 7 {
		t.Fatal("actual nonzero exit was not retained", outcome, bash)
	}

	if seen == "" || !strings.HasSuffix(seen, string(filepath.Separator)+"work") {
		t.Fatal("scoped directory not supplied", seen)
	}
	if _, err := os.Stat(filepath.Join(cfg.Root, "result.txt")); !os.IsNotExist(err) {
		t.Fatal("shared root polluted")
	}
	raw, err := os.ReadFile(filepath.Join(seen, "result.txt"))
	if err != nil || string(raw) != "approved" {
		t.Fatal(string(raw), err)
	}
	if scalar(t, s, `SELECT count(*) FROM tool_invocation WHERE tool_name='builtin__write' AND status='succeeded' AND attempt_count=1`) != 1 {
		t.Fatal("approved action not committed once")
	}
}

func TestTaskChildInheritsWorkScopeAndOwnerReportIsTaskBound(t *testing.T) {
	testTaskChildCodeScope(t, false)
}
func TestCodingPluginChildInheritsWorkScopeAndOwnerReportIsTaskBound(t *testing.T) {
	testTaskChildCodeScope(t, true)
}
func testTaskChildCodeScope(t *testing.T, managed bool) {
	t.Helper()
	s, r, a, p, cfg := codeFixture(t)
	if managed {
		installCodingPlugin(t, s, r)
	}
	ctx := context.Background()
	for _, name := range []string{"AGENTS.md", "README.md"} {
		if err := os.WriteFile(filepath.Join(cfg.RepositorySource, name), []byte("PRIVATE_PROJECT_DOC_CONTENT"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	seen := map[string]string{}
	var mu sync.Mutex
	r.localExecute = func(_ context.Context, got environment.Config, name, raw string) tool.Result {
		var request struct {
			Path string `json:"path"`
		}
		if name != "builtin__read" || json.Unmarshal([]byte(raw), &request) != nil {
			return tool.Failure("INVALID_ARGUMENT")
		}
		content, err := os.ReadFile(filepath.Join(got.Root, "file.txt"))
		if err != nil || string(content) != "dirty source" {
			return tool.Failure("PROVIDER_UNAVAILABLE")
		}
		mu.Lock()
		seen[request.Path] = got.Root
		mu.Unlock()
		if request.Path == "root-probe" {
			if err := os.Remove(filepath.Join(got.Root, "README.md")); err != nil {
				t.Fatal(err)
			}
		}
		return tool.Result{Text: request.Path}
	}
	p.respond = func(req llm.GenerationRequest) (llm.GenerationResult, error) {
		last := req.Messages[len(req.Messages)-1]
		contexts := 0
		for _, message := range req.Messages {
			if message.Source == nil || message.Source.Kind != "execution_environment" {
				continue
			}
			contexts++
			var payload struct {
				Content struct {
					CWD  string   `json:"cwd"`
					Docs []string `json:"docs"`
				} `json:"content"`
			}
			_, body, _ := strings.Cut(message.Text, "\n")
			if err := json.Unmarshal([]byte(body), &payload); err != nil {
				t.Fatal(err)
			}
			var directory string
			if err := s.db.QueryRow(`SELECT directory FROM task_code_environment`).Scan(&directory); err != nil {
				t.Fatal(err)
			}
			wantDocs := []string{"AGENTS.md", "README.md"}
			if strings.Contains(req.Messages[0].Text, "You are a Subagent Worker") {
				wantDocs = wantDocs[:1]
			}
			if message.Role != "user" || message.Origin != "runtime" || !message.Source.Protected || payload.Content.CWD != directory || payload.Content.CWD == cfg.Root || !slices.Equal(payload.Content.Docs, wantDocs) {
				t.Fatal("environment is not scoped and frozen per Worker", payload, message)
			}
		}
		if isRoot(req) && contexts != 1 || !isRoot(req) && contexts != 0 || strings.Contains(req.Messages[0].Text, "PRIVATE_PROJECT_DOC_CONTENT") {
			t.Fatal("environment duplicated, leaked to Main, or imported document contents", contexts)
		}
		if isRoot(req) {
			if last.Role == "tool" {
				if strings.Contains(last.Text, "root-probe") {
					return calls(agent.RunToolName, `{"task":"child probe"}`), nil
				}
				return llm.GenerationResult{Text: "done"}, nil
			}
			if last.Text == "child probe" {
				return calls("builtin__read", `{"path":"child-probe"}`), nil
			}
			return calls("builtin__read", `{"path":"root-probe"}`), nil
		}
		if isFollowup(req) || last.Role == "tool" {
			return llm.GenerationResult{Text: "reported"}, nil
		}
		return calls(task.ManageName, `{"operation":"create","task":"verify isolated child"}`), nil
	}
	mustSubmit(t, a, "scope", "verify isolated child")
	mustDrain(t, a)
	var ref string
	if err := s.db.QueryRow(`SELECT task_ref FROM task`).Scan(&ref); err != nil {
		t.Fatal(err)
	}
	w, err := s.CodeReport(ctx, ref, false)
	if err != nil || seen["root-probe"] != w.Directory || seen["child-probe"] != w.Directory || scalar(t, s, `SELECT count(*) FROM run WHERE worker_position='child' AND status='completed'`) != 1 {
		t.Fatal("Child or Root used a different scope", seen, w, err)
	}
	result, err := s.SaveCodeReport(ctx, ref)
	if err != nil || scalar(t, s, `SELECT count(*) FROM artifact JOIN task USING(task_id) WHERE artifact_ref=? AND task_ref=? AND visibility='task'`, result.Ref, ref) != 1 {
		t.Fatal("report Artifact not bound to original Task", result, err)
	}
	agentID := idAgent(t, s)
	s.MainBoundary = func(point string) {
		if point == "before_code_report_return" {
			if err := r.write(ctx, func(tx *sql.Tx) error { return r.revokeMemory(ctx, tx, agentID) }); err != nil {
				t.Fatal(err)
			}
		}
	}
	if report, err := s.CodeReport(ctx, ref, true); !errors.Is(err, agent.ErrNotFound) || report.Directory != "" || report.Diff != nil || len(report.Commands) != 0 {
		t.Fatal("late workspace report crossed Forget", report, err)
	}
}

func TestTaskCodeFrozenScopeMismatchFailsOnlyAcceptedWorker(t *testing.T) {
	s, r, a, p, cfg := codeFixture(t)
	p.respond = func(req llm.GenerationRequest) (llm.GenerationResult, error) {
		if isRoot(req) {
			t.Error("incompatible Worker reached provider")
		}
		if isFollowup(req) || req.Messages[len(req.Messages)-1].Role == "tool" {
			return llm.GenerationResult{Text: "reported"}, nil
		}
		return calls(task.ManageName, `{"operation":"create","task":"code scope drift"}`), nil
	}
	receipt := mustSubmit(t, a, "drift", "start Task")
	if err := executeTaskRunID(t, a, r, receipt.RunID); err != nil {
		t.Fatal(err)
	}
	cfg.RepositorySource += "-different"
	r.ConfigureLocalEnvironment(cfg)
	mustDrain(t, a)
	if scalar(t, s, `SELECT count(*) FROM run WHERE worker_position='root' AND status='failed' AND error_code='MODEL_INCOMPATIBLE'`) != 1 || scalar(t, s, `SELECT count(*) FROM task_update WHERE kind='failed'`) != 1 {
		t.Fatal("scope drift did not report a committed isolated failure")
	}
}

func TestCodePRHandoffFencesWatchRefreshAndForgetAtArtifactCommit(t *testing.T) {
	codePRHandoffFences(t, false)
}
func TestArtifactWorkbenchPRHandoffRetainsAssociationAndFencesJointCommit(t *testing.T) {
	codePRHandoffFences(t, true)
}

func codePRHandoffFences(t *testing.T, managed bool) {
	t.Helper()
	boundaries := []string{"refresh", "forget"}
	if managed {
		boundaries = []string{"valid", "refresh", "forget", "stop", "quarantine"}
	}
	for _, boundary := range boundaries {
		t.Run(boundary, func(t *testing.T) {
			ctx := context.Background()
			s, r, a, p, _ := codeFixture(t)
			var host rsi.Host
			var graph string
			if managed {
				host, graph, _ = artifactCodingComposition(t, s, r)
			}
			p.respond = func(req llm.GenerationRequest) (llm.GenerationResult, error) {
				if isRoot(req) || isFollowup(req) || req.Messages[len(req.Messages)-1].Role == "tool" {
					return llm.GenerationResult{Text: "prepared"}, nil
				}
				return calls(task.ManageName, `{"operation":"create","task":"review local code"}`), nil
			}
			mustSubmit(t, a, "code", "prepare code")
			mustDrain(t, a)
			var ref string
			if err := s.db.QueryRow(`SELECT task_ref FROM task`).Scan(&ref); err != nil {
				t.Fatal(err)
			}
			cfg := githubConfig()
			watchBatch(t, s, cfg, "")
			bindTask(t, s, cfg, ref)
			agentID := idAgent(t, s)
			checked := false
			s.MainBoundary = func(point string) {
				if point != "before_code_report_commit" {
					return
				}
				checked = true
				if boundary == "valid" {
					return
				}
				if boundary == "stop" {
					if err := host.Store.Stop(ctx, true); err != nil {
						t.Fatal(err)
					}
					return
				}
				if boundary == "quarantine" {
					if err := host.Store.Quarantine(ctx, graph); err != nil {
						t.Fatal(err)
					}
					return
				}
				if boundary == "refresh" {
					watchBatch(t, s, cfg, "") // same head, new durable version
				} else if err := r.write(ctx, func(tx *sql.Tx) error { return r.revokeMemory(ctx, tx, agentID) }); err != nil {
					t.Fatal(err)
				}
			}
			result, err := s.SaveCodePRReport(ctx, ref, cfg, githubBatch().HeadSHA)
			if boundary == "valid" {
				if err != nil || result.MethodGraph != graph || result.SemanticKind != "file" || result.MediaType != "application/json" || result.Name != "code-review.json" {
					t.Fatal("actual PR handoff bypassed Artifact recipe", result, err)
				}
				var report coding.Report
				if err := json.Unmarshal([]byte(readArtifact(t, s, artifactOwner(t, s), result.Ref)), &report); err != nil || report.PR == nil || report.PR.Kind != "owner_requested_association" || report.PR.HeadSHA != githubBatch().HeadSHA || report.PR.Repository != cfg.Repository {
					t.Fatal("original observed association was changed", report.PR, err)
				}
				if scalar(t, s, `SELECT count(*) FROM code_report_method_binding WHERE artifact_ref=? AND graph_revision=?`, result.Ref, graph) != 1 {
					t.Fatal("joint method receipt missing")
				}
				return
			}
			want := agent.ErrConflict
			if boundary == "forget" {
				want = agent.ErrNotFound
			}
			if boundary == "stop" || boundary == "quarantine" {
				want = llm.ProviderUnavailable
			}
			if !checked || !errors.Is(err, want) || result.Ref != "" || scalar(t, s, `SELECT count(*) FROM artifact`) != 0 {
				t.Fatal("report crossed an invalidated binding", checked, result, err)
			}
			if managed && scalar(t, s, `SELECT count(*) FROM code_report_method_binding`) != 0 {
				t.Fatal("revoked handoff left an uncommitted method receipt")
			}
		})
	}
}

package sqlite

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/z-chenhao/assistant/internal/computer"
	"github.com/z-chenhao/assistant/internal/computer/environment"
	"github.com/z-chenhao/assistant/internal/llm"
	"github.com/z-chenhao/assistant/internal/task"
	"github.com/z-chenhao/assistant/internal/tool"
)

type environmentGuest struct {
	lifecycleFake
	calls           int
	seen            computer.Instance
	unknown         bool
	name, arguments string
}

func (g *environmentGuest) WorkingDirectory() string { return "/Users/fixture/StewardWorkspace" }

func TestWorkerGuestContextUsesGuestDirectoryWithoutStartingComputer(t *testing.T) {
	_, r, a, p := taskFixture(t)
	defs := environment.Definitions("guest-fixture")
	for _, spec := range a.Registry.Describe() {
		definition, _ := a.Registry.Resolve(spec.Name)
		defs = append(defs, definition)
	}
	registry, err := tool.NewRegistry(defs)
	if err != nil {
		t.Fatal(err)
	}
	r.registry, a.Registry = registry, registry
	hostDirectory := t.TempDir()
	if err := os.WriteFile(filepath.Join(hostDirectory, "README.md"), []byte("Host-only documentation"), 0600); err != nil {
		t.Fatal(err)
	}
	r.ConfigureLocalEnvironment(environment.Config{Root: hostDirectory})
	guest := &environmentGuest{}
	r.ConfigureComputer(guest, computer.Compatibility{ProviderVersion: "fixture"}, computer.RestorationManifest{Version: 1})
	observed := false
	p.respond = func(req llm.GenerationRequest) (llm.GenerationResult, error) {
		if isRoot(req) {
			observed = true
			found := 0
			for _, message := range req.Messages {
				if message.Source != nil && message.Source.Kind == "execution_environment" {
					found++
					if !strings.Contains(message.Text, guest.WorkingDirectory()) || strings.Contains(message.Text, `"docs"`) || strings.Contains(message.Text, hostDirectory) {
						t.Fatal("Guest context exposed Host environment", message.Text)
					}
				}
			}
			if found != 1 {
				t.Fatal("missing or duplicated Guest environment", found)
			}
			return llm.GenerationResult{Text: "Task result"}, nil
		}
		if isFollowup(req) || req.Messages[len(req.Messages)-1].Role == "tool" {
			return llm.GenerationResult{Text: "Delivered"}, nil
		}
		return calls(task.ManageName, `{"operation":"create","task":"Inspect the execution context"}`), nil
	}
	mustSubmit(t, a, "guest-context", "Delegate a task")
	mustDrain(t, a)
	if !observed || guest.calls != 0 || guest.ensures != 0 {
		t.Fatal("context assembly started a Computer", observed, guest)
	}
}

func (g *environmentGuest) ExecuteEnvironment(_ context.Context, i computer.Instance, name, args string) tool.Result {
	g.calls++
	g.seen = i
	g.name, g.arguments = name, args
	if g.unknown {
		return tool.Failure("UNKNOWN_SIDE_EFFECT")
	}
	return tool.Result{Text: `{"path":"result.txt","hash":"fixture"}`}
}

func TestNativeGuestDispatchLoadsFrozenActionFromAcceptedInvocation(t *testing.T) {
	ctx := context.Background()
	calls := []llm.ToolCall{{ID: "frozen-write", Name: "builtin__write", Arguments: `{"path":"result.txt","content":"approved"}`}}
	_, r, _, executor, exchange := admitPolicy(t, environment.Definitions("guest-fixture"), calls)
	guest := &environmentGuest{}
	r.ConfigureComputer(guest, computer.Compatibility{ProviderVersion: "fixture"}, computer.RestorationManifest{Version: 1})
	if _, err := executor.Execute(ctx, exchange, calls); !errors.Is(err, tool.ErrSuspended) {
		t.Fatal(err)
	}
	approvePolicy(t, r.host, r)
	invs, err := executor.Store.Accept(ctx, exchange, calls, executor.Registry.Describe())
	if err != nil {
		t.Fatal(err)
	}
	if err = executor.Store.Start(ctx, invs[0].ID); err != nil {
		t.Fatal(err)
	}
	// A process-local caller cannot redirect the exact approved durable action.
	forged := invs[0]
	forged.Call.Name = "builtin__bash"
	forged.Call.Arguments = `{"command":"touch unapproved"}`
	result, err := executor.Store.(tool.HostStore).ExecuteBuiltin(ctx, forged)
	if err != nil || result.Error != nil {
		t.Fatal(result, err)
	}
	if guest.name != calls[0].Name || guest.arguments != calls[0].Arguments {
		t.Fatal("accepted action was substituted", guest.name, guest.arguments)
	}
	if _, err = executor.Store.(tool.OutputStore).FinishResult(ctx, invs[0].ID, result); err != nil {
		t.Fatal(err)
	}
}

func TestNativeGuestUsesDurableComputerAndExactApprovalWithoutJavaScript(t *testing.T) {
	for _, unknown := range []bool{false, true} {
		t.Run(map[bool]string{false: "completed", true: "unknown"}[unknown], func(t *testing.T) {
			ctx := context.Background()
			calls := []llm.ToolCall{{ID: "native-write", Name: "builtin__write", Arguments: `{"path":"result.txt","content":"result"}`}}
			s, r, _, executor, exchange := admitPolicy(t, environment.Definitions("guest-fixture"), calls)
			guest := &environmentGuest{unknown: unknown}
			r.ConfigureComputer(guest, computer.Compatibility{ProviderVersion: "fixture"}, computer.RestorationManifest{Version: 1})
			if _, err := executor.Execute(ctx, exchange, calls); !errors.Is(err, tool.ErrSuspended) {
				t.Fatal(err)
			}
			if guest.calls != 0 || guest.ensures != 0 || scalar(t, s, `SELECT count(*) FROM code_execution`) != 0 {
				t.Fatal("unapproved Guest dispatch")
			}
			approvePolicy(t, s, r)
			result, err := executor.Execute(ctx, exchange, calls)
			if err != nil || len(result) != 1 {
				t.Fatal(result, err)
			}
			if unknown && (result[0].Error == nil || result[0].Error.Code != "UNKNOWN_SIDE_EFFECT") {
				t.Fatal(result)
			}
			if !unknown && result[0].Error != nil {
				t.Fatal(result)
			}
			if guest.calls != 1 || guest.seen != guest.instance || guest.checkpoints != 1 || guest.destroys != 1 {
				t.Fatal("different Computer or missing durable disposal", guest)
			}
			if scalar(t, s, `SELECT count(*) FROM tool_invocation`) != 1 || scalar(t, s, `SELECT count(*) FROM code_execution_state WHERE claim_token IS NULL AND state IN ('completed','failed')`) != 1 {
				t.Fatal("second invocation or live native claim")
			}
			if r.javascript != nil {
				t.Fatal("native tools require a JavaScript runtime")
			}
			if _, err = executor.Execute(ctx, exchange, calls); err != nil || guest.calls != 1 {
				t.Fatal("completed or unknown effect re-executed", err, guest.calls)
			}
		})
	}
}

func TestRemovedWorkspaceToolsCannotReplayUnderNativeTools(t *testing.T) {
	registry, err := tool.NewRegistry(environment.Definitions("native-v1"))
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"read", "edit", "git", "search", "test"} {
		legacy := tool.Spec{Name: "builtin__workspace_" + name, Version: 1, Permission: "read_only", ReplayPolicy: "safe", InputSchema: `{"type":"object"}`}
		if _, ok := registry.Resolve(legacy.Name); ok {
			t.Fatal("legacy alias", name)
		}
		if _, err := registry.Select([]tool.Spec{legacy}); !errors.Is(err, llm.ModelIncompatible) {
			t.Fatal("legacy snapshot redirected", err)
		}
	}
}

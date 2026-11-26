package sqlite

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/z-chenhao/assistant/internal/agent"
	"github.com/z-chenhao/assistant/internal/llm"
	"github.com/z-chenhao/assistant/internal/tool"
)

func TestTaskCodeIndependentWorkspacesDoNotConsumeEachOthersConflictSlot(t *testing.T) {
	s, r, a, p, cfg := codeFixture(t)
	r.ConfigureToolLimits(ToolLimits{Global: 32, Agent: 32, Run: 32, Provider: 32, Tool: 32})
	p.respond = delegateToolWork(func(req llm.GenerationRequest) (llm.GenerationResult, error) {
		if req.Messages[len(req.Messages)-1].Role == "tool" {
			return llm.GenerationResult{Text: "command observed"}, nil
		}
		return calls("builtin__bash", `{"command":"printf ready > started.txt; while ! test -f release.txt; do sleep 0.01; done; printf independent > result.txt"}`), nil
	})
	for _, key := range []string{"parallel-first", "parallel-second"} {
		mustSubmit(t, a, key, "Run work in an independent task")
		mustDrain(t, a)
	}
	// Start the execution deadline after task setup and drain complete so slow
	// race-instrumented CI runners do not expire ctx before tool slots start.
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	approvals, err := r.PendingApprovals(ctx)
	if err != nil || len(approvals) != 2 {
		t.Fatal("expected two independently approved commands", approvals, err)
	}
	for _, approval := range approvals {
		if err := r.Decide(ctx, idAgent(t, s), approval.ID, approval.Hash, "approved"); err != nil {
			t.Fatal(err)
		}
	}
	type pending struct{ run, invocation, directory string }
	rows, err := s.db.Query(`SELECT i.run_id,i.invocation_id,e.directory FROM tool_invocation i JOIN run r USING(run_id) JOIN task_code_environment e USING(task_id) WHERE i.tool_name='builtin__bash' ORDER BY i.invocation_id`)
	if err != nil {
		t.Fatal(err)
	}
	var commands []pending
	for rows.Next() {
		var next pending
		if err := rows.Scan(&next.run, &next.invocation, &next.directory); err != nil {
			t.Fatal(err)
		}
		commands = append(commands, next)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	rows.Close()
	if len(commands) != 2 || commands[0].directory == commands[1].directory {
		t.Fatal("tasks did not receive independent writable directories", commands)
	}
	stores := make([]tool.Store, 0, 2)
	for _, command := range commands {
		claim, err := r.Claim(ctx, command.run)
		if err != nil {
			t.Fatal(err)
		}
		defer r.Release(ctx, claim)
		store := r.ToolStore(claim)
		if err := store.Start(ctx, command.invocation); err != nil {
			t.Fatalf("independent task blocked while its peer owns a tool slot: %v", err)
		}
		stores = append(stores, store)
	}
	if scalar(t, s, `SELECT count(*) FROM tool_invocation WHERE tool_name='builtin__bash' AND status='running'`) != 2 {
		t.Fatal("independent commands did not simultaneously own their slots")
	}
	type completion struct {
		index  int
		result tool.Result
		err    error
	}
	completed := make(chan completion, len(commands))
	var executing sync.WaitGroup
	executing.Add(len(commands))
	for index, command := range commands {
		go func() {
			defer executing.Done()
			result, err := stores[index].(tool.HostStore).ExecuteBuiltin(ctx, tool.Invocation{ID: command.invocation})
			completed <- completion{index, result, err}
		}()
	}
	defer func() {
		cancel()
		executing.Wait()
	}()
	for _, command := range commands {
		for {
			if data, err := os.ReadFile(filepath.Join(command.directory, "started.txt")); err == nil && string(data) == "ready" {
				break
			}
			select {
			case <-ctx.Done():
				t.Fatal("both real sandbox processes must run before either is released", ctx.Err())
			case <-time.After(5 * time.Millisecond):
			}
		}
	}
	for _, command := range commands {
		if err := os.WriteFile(filepath.Join(command.directory, "release.txt"), nil, 0600); err != nil {
			t.Fatal(err)
		}
	}
	for range commands {
		outcome := <-completed
		index, result, err := outcome.index, outcome.result, outcome.err
		command := commands[index]
		if err != nil || result.Error != nil {
			t.Fatal(result, err)
		}
		var output struct {
			Exit int `json:"exit_code"`
		}
		if json.Unmarshal([]byte(result.Text), &output) != nil || output.Exit != 0 {
			t.Fatal("actual sandbox command failed", result)
		}
		if err := stores[index].Finish(ctx, command.invocation, result); err != nil {
			t.Fatal(err)
		}
		data, err := os.ReadFile(filepath.Join(command.directory, "result.txt"))
		if err != nil || string(data) != "independent" {
			t.Fatal("command did not write its own task", string(data), err)
		}
	}
	for _, original := range []string{cfg.Root, cfg.RepositorySource} {
		if _, err := os.Stat(filepath.Join(original, "result.txt")); !os.IsNotExist(err) {
			t.Fatal("parallel command polluted original environment", original, err)
		}
	}
}

func TestTaskCodeSharedWorkspaceStillSerializesApprovedCommands(t *testing.T) {
	t.Run("one-run", func(t *testing.T) { testTaskCodeSharedWorkspace(t, false) })
	t.Run("root-and-child", func(t *testing.T) { testTaskCodeSharedWorkspace(t, true) })
}

func testTaskCodeSharedWorkspace(t *testing.T, child bool) {
	t.Helper()
	s, r, a, p, _ := codeFixture(t)
	ctx := context.Background()
	p.respond = delegateToolWork(func(req llm.GenerationRequest) (llm.GenerationResult, error) {
		if req.Messages[len(req.Messages)-1].Role == "tool" {
			return llm.GenerationResult{Text: "command observed"}, nil
		}
		batch := []llm.ToolCall{
			{ID: "first", Name: "builtin__bash", Arguments: `{"command":"printf first > shared.txt"}`},
			{ID: "second", Name: "builtin__bash", Arguments: `{"command":"printf second > shared.txt"}`},
		}
		if child {
			if strings.Contains(req.Messages[0].Text, "You are a Subagent Worker") {
				batch = batch[1:]
			} else {
				batch[1] = llm.ToolCall{ID: "spawn", Name: agent.RunToolName, Arguments: `{"task":"Write the second result in our shared task workspace"}`}
			}
		}
		return llm.GenerationResult{ToolCalls: batch}, nil
	})
	mustSubmit(t, a, "shared-workspace", "Run two approved commands in the same task")
	mustDrain(t, a)
	approvals, err := r.PendingApprovals(ctx)
	if err != nil || len(approvals) != 2 {
		t.Fatal(approvals, err)
	}
	for _, approval := range approvals {
		if err := r.Decide(ctx, idAgent(t, s), approval.ID, approval.Hash, "approved"); err != nil {
			t.Fatal(err)
		}
	}
	var run, secondRun, first, second string
	if err := s.db.QueryRow(`SELECT run_id,invocation_id FROM tool_invocation WHERE provider_call_id='first' AND tool_name='builtin__bash'`).Scan(&run, &first); err != nil {
		t.Fatal(err)
	}
	if err := s.db.QueryRow(`SELECT run_id,invocation_id FROM tool_invocation WHERE provider_call_id='second' AND tool_name='builtin__bash'`).Scan(&secondRun, &second); err != nil {
		t.Fatal(err)
	}
	claim, err := r.Claim(ctx, run)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Release(ctx, claim)
	store := r.ToolStore(claim)
	secondStore := store
	if child {
		if secondRun == run {
			t.Fatal("regression did not create an independent Child Run")
		}
		secondClaim, err := r.Claim(ctx, secondRun)
		if err != nil {
			t.Fatal(err)
		}
		defer r.Release(ctx, secondClaim)
		secondStore = r.ToolStore(secondClaim)
	}
	if err := store.Start(ctx, first); err != nil {
		t.Fatal(err)
	}
	if err := secondStore.Start(ctx, second); !errors.Is(err, tool.ErrResource) {
		t.Fatal("same task acquired two conflicting workspace slots", err)
	}
	result, err := store.(tool.HostStore).ExecuteBuiltin(ctx, tool.Invocation{ID: first})
	if err != nil || result.Error != nil {
		t.Fatal(result, err)
	}
	if err := store.Finish(ctx, first, result); err != nil {
		t.Fatal(err)
	}
	if err := secondStore.Start(ctx, second); err != nil {
		t.Fatal("finishing the prior command did not release this workspace", err)
	}
}

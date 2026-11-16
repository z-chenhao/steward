package sqlite

import (
	"context"
	"testing"

	"github.com/z-chenhao/assistant/internal/computer"
	"github.com/z-chenhao/assistant/internal/computer/environment"
	"github.com/z-chenhao/assistant/internal/llm"
	"github.com/z-chenhao/assistant/internal/tool"
)

type preparingEnvironment struct {
	lifecycleFake
	ready    bool
	prepares int
}

func (*preparingEnvironment) WorkingDirectory() string { return "/Users/fixture/StewardWorkspace" }

func (p *preparingEnvironment) Prepare(context.Context) error {
	p.prepares++
	if !p.ready {
		return computer.ErrPreparing
	}
	return nil
}
func (*preparingEnvironment) ExecuteEnvironment(context.Context, computer.Instance, string, string) tool.Result {
	return tool.Result{Text: `{"text":"ready"}`}
}

func TestDefaultDesktopPreparationSuspendsAndResumesSameInvocation(t *testing.T) {
	ctx := context.Background()
	calls := []llm.ToolCall{{ID: "read", Name: "builtin__read", Arguments: `{"path":"note.txt"}`}}
	s, r, _, executor, exchange := admitPolicy(t, environment.Definitions("default-preparation"), calls)
	provider := &preparingEnvironment{}
	r.ConfigureComputer(provider, computer.Compatibility{ProviderVersion: "fixture"}, computer.RestorationManifest{Version: 1})
	if _, err := executor.Execute(ctx, exchange, calls); err != tool.ErrSuspended {
		t.Fatal("preparation did not suspend", err)
	}
	if provider.ensures != 0 || scalar(t, s, `SELECT count(*) FROM computer`) != 0 || scalar(t, s, `SELECT count(*) FROM tool_invocation WHERE status='waiting' AND wait_reason='resource' AND attempt_count=0`) != 1 {
		t.Fatal("unprepared desktop acquired a VM or started a tool")
	}
	provider.ready = true
	results, err := executor.Execute(ctx, exchange, calls)
	if err != nil || len(results) != 1 || results[0].Error != nil {
		t.Fatal(results, err)
	}
	if provider.ensures != 1 || scalar(t, s, `SELECT count(*) FROM tool_invocation`) != 1 || scalar(t, s, `SELECT count(*) FROM tool_invocation WHERE status='succeeded' AND attempt_count=1`) != 1 {
		t.Fatal("resume duplicated invocation or VM")
	}
}

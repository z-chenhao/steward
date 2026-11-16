package sqlite

import (
	"context"
	"testing"

	"github.com/z-chenhao/assistant/internal/automation"
	"github.com/z-chenhao/assistant/internal/computer"
	"github.com/z-chenhao/assistant/internal/computer/environment"
	"github.com/z-chenhao/assistant/internal/evidence"
	"github.com/z-chenhao/assistant/internal/llm"
	"github.com/z-chenhao/assistant/internal/memory"
	"github.com/z-chenhao/assistant/internal/recall"
	"github.com/z-chenhao/assistant/internal/skill"
	"github.com/z-chenhao/assistant/internal/tool"
	"github.com/z-chenhao/assistant/internal/tool/codemode"
)

// Main remains the actual task owner; only the delegated Root runs the supplied
// execution policy. This keeps execution regressions on the production route.
func delegateToolWork(execute func(llm.GenerationRequest) (llm.GenerationResult, error)) func(llm.GenerationRequest) (llm.GenerationResult, error) {
	return func(req llm.GenerationRequest) (llm.GenerationResult, error) {
		if isRoot(req) {
			return execute(req)
		}
		if isFollowup(req) || req.Messages[len(req.Messages)-1].Role == "tool" {
			return llm.GenerationResult{Text: "Task observed"}, nil
		}
		return calls("builtin__task_manage", `{"operation":"create","task":"Execute the requested tool work and return evidence."}`), nil
	}
}

func TestMainAndWorkerUseOnlyCodeModeWithinTheirFrozenRole(t *testing.T) {
	testCodeModeRoles(t, false)
}
func TestSkillPluginCodeModeKeepsMainAndWorkerRoleBoundaries(t *testing.T) {
	testCodeModeRoles(t, true)
}
func testCodeModeRoles(t *testing.T, managed bool, configure ...func(*Store, *Runs)) {
	defs := append(environment.Definitions("role-test"), computer.Definitions("role-test")...)
	defs = append(defs, codemode.Definition(), memory.Definition(), recall.Definition(recall.UnconfiguredIdentity), evidence.Definition())
	defs = append(defs, automation.Definitions()...)
	skills := skill.EmptyDefinitions()
	skills[0].Spec.PromptContext = []tool.PromptContext{{Kind: "skill_index", Source: "skills:role-test", Text: `[{"name":"research","description":"FROZEN_SKILL_INDEX","scope":"agent","version":"frozen"}]`}}
	defs = append(defs, skills...)
	s, r, a, p := childFixture(t, defs...)
	r.ConfigureJavaScript(realCodeRuntime(t))
	configured := []tool.Definition{}
	for _, spec := range r.registry.Describe() {
		d, _ := r.registry.Resolve(spec.Name)
		d.Spec.CodeModeOnly = spec.Name != codemode.ToolName
		configured = append(configured, d)
	}
	registry, err := tool.NewRegistry(configured)
	if err != nil {
		t.Fatal(err)
	}
	r.registry = registry
	a.Registry = registry
	if managed {
		installSkillPlugin(t, s, r)
	}
	for _, setup := range configure {
		setup(s, r)
	}
	mainSeen, workerSeen := false, false
	p.respond = func(req llm.GenerationRequest) (llm.GenerationResult, error) {
		if len(req.Tools) != 1 || req.Tools[0].Name != codemode.ToolName {
			t.Fatalf("non-Code-Mode tool declared: %+v", req.Tools)
		}
		if isRoot(req) {
			workerSeen = true
			if req.Messages[len(req.Messages)-1].Role == "tool" {
				return llm.GenerationResult{Text: "worker evidence"}, nil
			}
			if managed {
				return calls(codemode.ToolName, `{"source":"if (ALL_TOOLS.some(t=>t.name==='builtin__task_manage'||t.name==='builtin__evidence_read')) throw Error('Main authority leaked');const methods=await tools.builtin__skill_list({query:'research'});if(!JSON.stringify(methods).includes('FROZEN_SKILL_INDEX'))throw Error('frozen methods missing');return await tools.mcp__fixture__echo({text:'worker evidence'});"}`), nil
			}
			return calls(codemode.ToolName, `{"source":"if (ALL_TOOLS.some(t=>t.name==='builtin__task_manage'||t.name==='builtin__evidence_read')) throw Error('Main authority leaked');return await tools.mcp__fixture__echo({text:'worker evidence'});"}`), nil
		}
		mainSeen = true
		if isFollowup(req) || req.Messages[len(req.Messages)-1].Role == "tool" {
			return llm.GenerationResult{Text: "Task accepted and results inspected"}, nil
		}
		return calls(codemode.ToolName, `{"source":"if (ALL_TOOLS.some(t=>['builtin__bash','builtin__read','builtin__edit','builtin__write','builtin__agent_run','builtin__computer_invoke'].includes(t.name))) throw Error('Worker authority leaked');await tools.builtin__skill_list({});return await tools.builtin__task_manage({operation:'create',task:'Obtain worker evidence using available tools.'});"}`), nil
	}
	mustSubmit(t, a, "code-only", "Do this business work")
	mustDrain(t, a)
	if !mainSeen || !workerSeen || scalar(t, s, `SELECT count(*) FROM task WHERE status='completed'`) != 1 {
		t.Fatal("Code Mode did not complete Main -> Task -> Worker")
	}
	if scalar(t, s, `SELECT count(*) FROM tool_invocation WHERE tool_name='builtin__task_manage' AND status='succeeded' AND attempt_count=1`) != 1 {
		t.Fatal("Main script did not use the ordinary Host Task executor")
	}
	if scalar(t, s, `SELECT count(*) FROM code_execution`) != 2 {
		t.Fatal("Main or Worker skipped Code Mode")
	}
	p.respond = func(req llm.GenerationRequest) (llm.GenerationResult, error) {
		if req.Messages[len(req.Messages)-1].Role == "tool" {
			return llm.GenerationResult{Text: "Rejected direct call"}, nil
		}
		return calls("builtin__task_manage", `{"forged":"must be rejected before schema validation"}`), nil
	}
	mustSubmit(t, a, "code-only-forged", "Reject direct bypass")
	mustDrain(t, a)
	if scalar(t, s, `SELECT count(*) FROM tool_invocation WHERE tool_name='builtin__task_manage' AND attempt_count=0 AND json_extract(result_json,'$.error.code')='NOT_FOUND'`) != 1 {
		t.Fatal("native call bypassed the Code Mode entry")
	}
}

func TestUnconfiguredMemoryFailsWithoutMutationOrAutomaticRecall(t *testing.T) {
	ctx := context.Background()
	defs := []tool.Definition{memory.Definition(), recall.Definition(recall.UnconfiguredIdentity)}
	calls := []llm.ToolCall{
		{ID: "remember", Name: memory.ManageName, Arguments: `{"operation":"remember","content":"I prefer tea"}`},
		{ID: "forget", Name: memory.ManageName, Arguments: `{"operation":"forget","memory_ref":"memory:missing"}`},
		{ID: "search", Name: recall.ToolName, Arguments: `{"query":"tea"}`},
	}
	s, r, _, e, exchange := admitPolicy(t, defs, calls)
	out, err := e.Execute(ctx, exchange, calls)
	if err != nil || len(out) != 3 {
		t.Fatal(out, err)
	}
	for _, result := range out {
		if result.Error == nil || result.Error.Code != "PROVIDER_UNAVAILABLE" {
			t.Fatal(result)
		}
	}
	for _, table := range []string{"memory_mutation", "memory_privacy", "run_recall", "approval"} {
		if scalar(t, s, "SELECT count(*) FROM "+table) != 0 {
			t.Fatal("unconfigured call changed state", table)
		}
	}
	if r.recall != nil {
		t.Fatal("implicit recall provider")
	}
}

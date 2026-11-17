package sqlite

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/z-chenhao/assistant/internal/agent"
	"github.com/z-chenhao/assistant/internal/llm"
	"github.com/z-chenhao/assistant/internal/task"
	"github.com/z-chenhao/assistant/internal/tool"
	"github.com/z-chenhao/assistant/internal/tool/codemode"
)

// Historical specs omit CodeModeOnly; recovery must retain their loaded declarations.
func largeToolCatalog() []tool.Definition {
	defs := []tool.Definition{tool.SearchDefinition(), codemode.Definition()}
	for i := 0; i < 500; i++ {
		defs = append(defs, tool.Definition{Spec: tool.Spec{Name: fmt.Sprintf("mcp__catalog__lookup_%03d", i), Description: "Lookup a catalog record", NamespaceDescription: "Find catalog records", Provider: "mcp:catalog", Version: 1, Exposure: tool.CodeMode, Permission: "read_only", ReplayPolicy: "safe", InputSchema: fmt.Sprintf(`{"type":"object","properties":{"text":{"type":"string","description":"SCHEMA_SENTINEL_%03d"}},"required":["text"],"additionalProperties":false}`, i)}, Execute: func(context.Context, string) tool.Result { return tool.Result{Text: "real catalog receipt"} }})
	}
	return defs
}

func TestLegacyMainLoadsOneOf500ToolsAndWorkerUsesCodeModeWithoutLoading(t *testing.T) {
	testToolDiscoveryMainAndWorker(t, nil, "lookup_499", "mcp__catalog__lookup_499")
}
func testToolDiscoveryMainAndWorker(t *testing.T, configure func(*Store, *Runs), query, target string) {
	s, r, a, p := childFixture(t, largeToolCatalog()...)
	if configure != nil {
		configure(s, r)
	}
	r.ConfigureJavaScript(realCodeRuntime(t))
	worker := false
	p.respond = func(request llm.GenerationRequest) (llm.GenerationResult, error) {
		last := request.Messages[len(request.Messages)-1]
		catalog := []string{}
		for _, spec := range request.Tools {
			if strings.HasPrefix(spec.Name, "mcp__catalog__") {
				catalog = append(catalog, spec.Name)
			}
		}
		if isRoot(request) {
			if len(catalog) != 0 {
				t.Fatal("Worker preloaded MCP schemas", len(catalog))
			}
			if last.Role == "tool" {
				return llm.GenerationResult{Text: last.Text}, nil
			}
			source := `if (ALL_TOOLS.some(t=>t.name==='builtin__tool_search')) throw Error('model-only search leaked'); `
			if configure != nil {
				q, _ := json.Marshal(query)
				want, _ := json.Marshal(target)
				source += `const hits=await searchTools(` + string(q) + `,{limit:1,namespace:'mcp__catalog'});if(hits.length!==1||hits[0].name!==` + string(want) + `)throw Error('ranked discovery not used'); `
			}
			targetJSON, _ := json.Marshal(target)
			source += `return await tools[` + string(targetJSON) + `]({text:'Worker request'});`
			raw, _ := json.Marshal(map[string]string{"source": source})
			return calls(codemode.ToolName, string(raw)), nil
		}
		if worker {
			if isFollowup(request) || last.Role == "tool" {
				return llm.GenerationResult{Text: "Worker result delivered"}, nil
			}
			return calls(task.ManageName, `{"operation":"create","task":"Read a catalog record with Code Mode."}`), nil
		}
		switch last.ToolCallID {
		case "forged":
			if !strings.Contains(last.Text, "NOT_FOUND") {
				t.Fatal("undeclared tool was dispatched", last.Text)
			}
			raw, _ := json.Marshal(tool.SearchRequest{Query: query, Limit: 1})
			return llm.GenerationResult{ToolCalls: []llm.ToolCall{{ID: "search", Name: tool.SearchName, Arguments: string(raw)}}}, nil
		case "search":
			if len(catalog) != 1 || catalog[0] != target {
				t.Fatal("search did not load exactly one tool", catalog)
			}
			return llm.GenerationResult{ToolCalls: []llm.ToolCall{{ID: "lookup", Name: target, Arguments: `{"text":"Main request"}`}}}, nil
		case "lookup":
			if last.Text != "real catalog receipt" {
				t.Fatal("Main lost tool receipt", last.Text)
			}
			return llm.GenerationResult{Text: "Main lookup complete"}, nil
		default:
			if len(catalog) != 0 {
				t.Fatal("Main preloaded MCP schemas", len(catalog))
			}
			raw, _ := json.Marshal(request)
			if strings.Contains(string(raw), "SCHEMA_SENTINEL_") {
				t.Fatal("catalog leaked through context")
			}
			return llm.GenerationResult{ToolCalls: []llm.ToolCall{{ID: "forged", Name: target, Arguments: `{"text":"not loaded"}`}}}, nil
		}
	}
	mustSubmit(t, a, "catalog-main", "Find a single catalog record")
	mustDrain(t, a)
	if scalar(t, s, `SELECT count(*) FROM task`) != 0 || scalar(t, s, `SELECT count(*) FROM tool_invocation WHERE tool_name=? AND status='succeeded' AND attempt_count=1`, target) != 1 || scalar(t, s, `SELECT count(*) FROM approval`) != 0 {
		t.Fatal("discovery executed extra work")
	}
	var checkpoint string
	if err := s.db.QueryRow(`SELECT checkpoint_json FROM run WHERE kind='main' ORDER BY created_at LIMIT 1`).Scan(&checkpoint); err != nil {
		t.Fatal(err)
	}
	var cp agent.Checkpoint
	if json.Unmarshal([]byte(checkpoint), &cp) != nil || len(cp.LoadedTools) != 1 || cp.LoadedTools[0] != target {
		t.Fatal("loaded state not durable", checkpoint)
	}
	worker = true
	mustSubmit(t, a, "catalog-worker", "Delegate a catalog workflow")
	mustDrain(t, a)
	if scalar(t, s, `SELECT count(*) FROM code_execution`) != 1 || scalar(t, s, `SELECT count(*) FROM tool_invocation WHERE tool_name=? AND status='succeeded' AND attempt_count=1`, target) != 2 {
		var outcomes string
		if err := s.db.QueryRow(`SELECT coalesce(group_concat(tool_name||':'||status||':'||result_json,';'),'') FROM tool_invocation WHERE tool_name=?`, codemode.ToolName).Scan(&outcomes); err != nil {
			t.Fatal(err)
		}
		t.Fatal("Code Mode required a model declaration or lost execution", outcomes)
	}
	if configure != nil && scalar(t, s, `SELECT count(*) FROM code_search_reply`) != 1 {
		t.Fatal("actual CodeMode discovery bypassed managed accepted cache")
	}
}

func TestLoadedToolSurvivesApprovalWaitAndPresentationChange(t *testing.T) {
	d := effectDef(t, "never")
	d.Spec.Exposure = tool.Deferred
	s, r, a, p := childFixture(t, tool.SearchDefinition(), d)
	p.respond = func(request llm.GenerationRequest) (llm.GenerationResult, error) {
		last := request.Messages[len(request.Messages)-1]
		if last.ToolCallID == "load" {
			found := false
			for _, spec := range request.Tools {
				found = found || spec.Name == d.Spec.Name
			}
			if !found {
				t.Fatal("loaded effect missing")
			}
			return llm.GenerationResult{ToolCalls: effectCall(`{"target":"fixture","text":"approved once"}`)}, nil
		}
		if last.Role == "tool" {
			return llm.GenerationResult{Text: "Done"}, nil
		}
		return llm.GenerationResult{ToolCalls: []llm.ToolCall{{ID: "load", Name: tool.SearchName, Arguments: `{"query":"mcp__fixture__send","limit":1}`}}}, nil
	}
	first := mustSubmit(t, a, "load-effect", "Make one approved change")
	mustDrain(t, a)
	if scalar(t, s, `SELECT count(*) FROM approval WHERE status='pending'`) != 1 || scalar(t, s, `SELECT sum(attempt_count) FROM tool_invocation WHERE tool_name=?`, d.Spec.Name) != 0 {
		t.Fatal("search approved or dispatched the effect")
	}
	defs := []tool.Definition{}
	for _, spec := range a.Registry.Describe() {
		current, _ := a.Registry.Resolve(spec.Name)
		if spec.Name == d.Spec.Name {
			current.Spec.Exposure = tool.Hidden
			current.Spec.NamespaceDescription = "new live summary"
		}
		defs = append(defs, current)
	}
	updated, err := tool.NewRegistry(defs)
	if err != nil {
		t.Fatal(err)
	}
	a.Registry = updated
	// A fresh Store adapter models process restart. The accepted invocation and
	// declaration are recovered from the same frozen Run, not the live registry.
	r = s.Runs(r.history, updated)
	a.Store = r
	approvePolicy(t, s, r)
	mustDrain(t, a)
	if scalar(t, s, `SELECT count(*) FROM tool_invocation WHERE tool_name=? AND status='succeeded' AND attempt_count=1`, d.Spec.Name) != 1 {
		t.Fatal("accepted effect lost or repeated")
	}
	var checkpoint string
	if err := s.db.QueryRow(`SELECT checkpoint_json FROM run WHERE run_id=?`, first.RunID).Scan(&checkpoint); err != nil {
		t.Fatal(err)
	}
	var cp agent.Checkpoint
	if json.Unmarshal([]byte(checkpoint), &cp) != nil || len(cp.LoadedTools) != 1 || cp.LoadedTools[0] != d.Spec.Name {
		t.Fatal("loaded declaration lost on restart")
	}
}

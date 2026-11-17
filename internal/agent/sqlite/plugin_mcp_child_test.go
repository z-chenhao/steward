package sqlite

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/z-chenhao/assistant/internal/agent"
	"github.com/z-chenhao/assistant/internal/llm"
	"github.com/z-chenhao/assistant/internal/plugin/mcpbridge/contract"
	"github.com/z-chenhao/assistant/internal/rsi"
	"github.com/z-chenhao/assistant/internal/task"
	"github.com/z-chenhao/assistant/internal/tool"
	"github.com/z-chenhao/assistant/internal/tool/codemode"
)

func TestMCPPluginActualChildRetainsMethodNamespaceAndPrivateScopeAfterRetirement(t *testing.T) {
	ctx := context.Background()
	defs, root := mcpPluginDefinitions(t, "  "+strings.Repeat("界", 700))
	if err := os.WriteFile(filepath.Join(root, "mcp-result.json"), []byte(`{"content":[{"type":"text","text":"甲"},{"type":"text","text":"乙"}]}`), 0600); err != nil {
		t.Fatal(err)
	}
	s, r, a, p := childFixture(t, append(defs, codemode.Definition(), tool.SearchDefinition())...)
	r.ConfigureJavaScript(realCodeRuntime(t))
	catalog := rsi.NewCatalog(filepath.Dir(s.path))
	catalog.Launcher = runtimeFeatureHost(t)
	r.ConfigurePlugins(catalog)
	h := rsi.Host{Catalog: catalog, Store: s.Plugins()}
	original, pkg := selectMCPVersion(t, h, "", "1.0.0")
	switched := false
	p.respond = func(req llm.GenerationRequest) (llm.GenerationResult, error) {
		last := req.Messages[len(req.Messages)-1]
		if isFollowup(req) || last.Role == "tool" {
			return llm.GenerationResult{Text: "Child MCP evidence observed"}, nil
		}
		if !isRoot(req) {
			return calls(task.ManageName, `{"operation":"create","task":"PRIVATE_ROOT_MCP_INSTRUCTION"}`), nil
		}
		if last.Text == "read child MCP with metadata" {
			raw, _ := json.Marshal(map[string]string{"source": `await tools.mcp__managed__read({text:"Child original source"}); return (await describeNamespace("managed")).description;`})
			return calls(codemode.ToolName, string(raw)), nil
		}
		if !switched {
			selectMCPVersion(t, h, original, "1.4.0") // changes both method and namespace
			if err := h.Store.Retire(ctx, pkg); err != nil {
				t.Fatal(err)
			}
			switched = true
		}
		return calls(agent.RunToolName, `{"task":"read child MCP with metadata"}`), nil
	}
	mustSubmit(t, a, "mcp-child-original", "PRIVATE_MAIN_MCP_CONVERSATION")
	mustDrain(t, a)
	if !switched || scalar(t, s, `SELECT count(*) FROM run c JOIN rsi_run cb ON cb.run_id=c.run_id JOIN rsi_run pb ON pb.run_id=c.parent_run_id WHERE c.worker_position='child' AND c.status='completed' AND cb.graph_revision=? AND cb.graph_revision=pb.graph_revision AND cb.managed_services_json=pb.managed_services_json AND EXISTS(SELECT 1 FROM json_each(cb.managed_services_json) WHERE value=?) AND EXISTS(SELECT 1 FROM json_each(cb.managed_services_json) WHERE value=?)`, original, contract.Method, contract.Namespace) != 1 {
		t.Fatal("actual Child did not inherit its original complete MCP graph")
	}
	var childID, raw string
	if err := s.db.QueryRow(`SELECT r.run_id,i.result_json FROM tool_invocation i JOIN run r USING(run_id) WHERE r.worker_position='child' AND i.tool_name='mcp__managed__read' AND i.status='succeeded'`).Scan(&childID, &raw); err != nil {
		t.Fatal(err)
	}
	var result tool.Result
	if json.Unmarshal([]byte(raw), &result) != nil || result.Error != nil || result.Text != "甲乙" {
		t.Fatal("Child substituted active MCP method or lost original source", raw)
	}
	if err := s.db.QueryRow(`SELECT result_json FROM tool_invocation WHERE run_id=? AND tool_name=?`, childID, codemode.ToolName).Scan(&raw); err != nil {
		t.Fatal(err)
	}
	var output []struct {
		Value string `json:"value"`
	}
	if json.Unmarshal([]byte(raw), &result) != nil || result.Error != nil || json.Unmarshal([]byte(result.Text), &output) != nil || len(output) != 1 || output[0].Value != strings.Repeat("界", 512)+"…" {
		t.Fatal("Child substituted active namespace policy", raw)
	}
	p.mu.Lock()
	requests := append([]llm.GenerationRequest{}, p.requests...)
	p.mu.Unlock()
	seen := 0
	for _, req := range requests {
		if req.RunID != childID {
			continue
		}
		seen++
		raw, _ := json.Marshal(req.Messages)
		if strings.Contains(string(raw), "PRIVATE_ROOT_MCP_INSTRUCTION") || strings.Contains(string(raw), "PRIVATE_MAIN_MCP_CONVERSATION") {
			t.Fatal("Child inherited parent private conversation or instruction")
		}
	}
	if seen == 0 {
		t.Fatal("no actual Child model request was inspected")
	}
}

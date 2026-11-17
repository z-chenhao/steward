package agent

import (
	"encoding/json"
	"testing"

	"github.com/z-chenhao/assistant/internal/tool"
)

func TestLoadedDeclarationsRemainSeparateFromAuthorityAndWorkingState(t *testing.T) {
	specs := []tool.Spec{{Name: "direct", Exposure: tool.Direct}, {Name: "deferred", Exposure: tool.Deferred}, {Name: "script", Exposure: tool.CodeMode}, {Name: "hidden", Exposure: tool.Hidden}, {Name: "bridge", Exposure: tool.Deferred, Internal: true}, tool.SearchDefinition().Spec}
	cp := Checkpoint{}
	if len(ProviderTools(specs)) != 2 {
		t.Fatal("indirect tools declared initially")
	}
	result := tool.Result{Text: `{"loaded":["deferred"]}`}
	if err := applyToolLoads(&cp, specs, "mcp__untrusted__result", result); err != nil || len(cp.LoadedTools) != 0 {
		t.Fatal("provider result loaded tools", err)
	}
	if err := applyToolLoads(&cp, specs, tool.SearchName, result); err != nil || len(ProviderTools(specs, cp.LoadedTools)) != 3 {
		t.Fatal("search failed to load schema", err)
	}
	cp.WorkingState = &WorkingState{Through: 0, Text: "Semantic context has been compacted"}
	raw, _ := json.Marshal(cp)
	var recovered Checkpoint
	if err := json.Unmarshal(raw, &recovered); err != nil || len(ProviderTools(specs, recovered.LoadedTools)) != 3 {
		t.Fatal("compaction/recovery lost declarations", err)
	}
	for _, name := range []string{"hidden", "bridge", "outside"} {
		bad := tool.Result{Text: `{"loaded":["` + name + `"]}`}
		if err := applyToolLoads(&recovered, specs, tool.SearchName, bad); err == nil {
			t.Fatal("search broadened frozen authority", name)
		}
	}
	if len(ProviderTools(specs, []string{"hidden", "bridge", "outside"})) != 2 {
		t.Fatal("active names granted unavailable capabilities")
	}
}

func TestCodeModeOnlyMCPNeverBecomesAModelTool(t *testing.T) {
	spec := tool.Spec{Name: "mcp__catalog__lookup", Exposure: tool.CodeMode, CodeModeOnly: true, Permission: "read_only"}
	if len(ProviderTools([]tool.Spec{spec}, []string{spec.Name})) != 0 {
		t.Fatal("loaded checkpoint exposed a script-only MCP tool")
	}
	if len(MainTools([]tool.Spec{spec})) != 1 || len(WorkerTools([]tool.Spec{spec})) != 1 {
		t.Fatal("frozen routing or Worker authority lost")
	}
	result := tool.SearchToolsLegacyV1([]tool.Spec{spec}, nil, "lookup", 1)
	if len(result.Tools) != 1 || len(result.Loaded) != 0 {
		t.Fatal("metadata search activated a native MCP declaration", result)
	}
}

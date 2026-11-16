package sqlite

import (
	"context"
	"encoding/json"
	"os"
	"sync/atomic"
	"testing"

	"github.com/z-chenhao/assistant/internal/llm"
	"github.com/z-chenhao/assistant/internal/skill"
	"github.com/z-chenhao/assistant/internal/tool"
)

func TestUIManagedSkillReadsOriginalPackageWithoutNativeReader(t *testing.T) {
	s, r, _, a, p := loopFixture(t)
	input := modelInput()
	input.ContextWindow = 65536
	activateLoop(t, s, input)
	defs, err := skill.IncludeIntelligentUI(skill.EmptyDefinitions())
	if err != nil {
		t.Fatal(err)
	}
	var nativeCalls atomic.Int32
	for i := range defs {
		if defs[i].Spec.Name == "builtin__skill_view" {
			defs[i].Execute = func(context.Context, string) tool.Result {
				nativeCalls.Add(1)
				return tool.Failure("NATIVE_READER_FORBIDDEN")
			}
		}
	}
	registry, err := tool.NewRegistry(defs)
	if err != nil {
		t.Fatal(err)
	}
	r.registry, a.Registry = registry, registry
	if err = r.ConfigureSkillLibrary(); err != nil {
		t.Fatal(err)
	}
	installUIRuntimePlugin(t, s, r)
	p.respond = func(req llm.GenerationRequest) (llm.GenerationResult, error) {
		if req.Messages[len(req.Messages)-1].Role == "tool" {
			return llm.GenerationResult{Text: "Read packaged UI methods."}, nil
		}
		return llm.GenerationResult{ToolCalls: []llm.ToolCall{
			{ID: "entry", Name: "builtin__skill_view", Arguments: `{"name":"intelligent_ui"}`},
			{ID: "recipe", Name: "builtin__skill_view", Arguments: `{"name":"intelligent_ui","path":"templates/architecture-diagram.json"}`},
			{ID: "missing", Name: "builtin__skill_view", Arguments: `{"name":"intelligent_ui","path":"missing.md"}`},
		}}, nil
	}
	receipt := mustSubmit(t, a, "ui-packaged-skill", "Read UI methods")
	mustDrain(t, a)
	rows, err := s.db.Query(`SELECT arguments_json,result_json FROM tool_invocation WHERE run_id=? AND tool_name='builtin__skill_view'`, receipt.RunID)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	count := 0
	for rows.Next() {
		var args, raw string
		if err = rows.Scan(&args, &raw); err != nil {
			t.Fatal(err)
		}
		var request struct {
			Name string `json:"name"`
			Path string `json:"path"`
		}
		var result tool.Result
		if json.Unmarshal([]byte(args), &request) != nil || json.Unmarshal([]byte(raw), &result) != nil {
			t.Fatal("invalid receipt")
		}
		if request.Path == "missing.md" {
			if result.Error == nil || result.Error.Code != "NOT_FOUND" {
				t.Fatal("undeclared file accepted", result)
			}
		} else {
			if request.Path == "" {
				request.Path = "SKILL.md"
			}
			expected, err := os.ReadFile("../../plugin/intelligentui/skill/intelligent_ui/" + request.Path)
			if err != nil || result.Error != nil || result.Text != string(expected) {
				t.Fatal("wrong package Skill bytes", request.Path, result.Error, err)
			}
		}
		count++
	}
	if err = rows.Err(); err != nil {
		t.Fatal(err)
	}
	if count != 3 || nativeCalls.Load() != 0 {
		t.Fatal("packaged Skill bypassed or native reader used", count, nativeCalls.Load())
	}
}

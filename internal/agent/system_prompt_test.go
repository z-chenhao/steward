package agent

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/z-chenhao/assistant/internal/llm"
	"github.com/z-chenhao/assistant/internal/prompt"
	"github.com/z-chenhao/assistant/internal/skill"
	"github.com/z-chenhao/assistant/internal/tool"
)

func TestSkillsUseSearchWithoutPromptCatalog(t *testing.T) {
	defs, err := skill.IncludeIntelligentUI(skill.EmptyDefinitions())
	if err != nil {
		t.Fatal(err)
	}
	specs := []tool.Spec{}
	var list, view tool.Definition
	for _, d := range defs {
		specs = append(specs, d.Spec)
		if d.Spec.Name == "builtin__skill_list" {
			list = d
		}
		if d.Spec.Name == "builtin__skill_view" {
			view = d
		}
	}
	for _, role := range []string{"main", "root", "child"} {
		wire, err := ProviderMessages(InitialContext(ContextOptions{Position: role, Tools: specs, Task: "Review the request"}), false, false)
		if err != nil {
			t.Fatal(err)
		}
		for _, internal := range []string{"<skills>", "<skill_index>", "intelligent_ui", "Present a compact native component", "skills:"} {
			if strings.Contains(wire[0].Text, internal) {
				t.Fatal("Skill catalog leaked into prompt", role, internal)
			}
		}
		if strings.Contains(wire[0].Text, "skill_list") {
			t.Fatal("tool usage instructions leaked into system", role)
		}
	}
	if !strings.Contains(list.Spec.Description, "before specialized work") || !strings.Contains(list.Spec.Description, "skill_view") {
		t.Fatal("tool description lost discovery guidance")
	}
	found := list.Execute(context.Background(), `{"query":"interactive visual","limit":1}`)
	var matches []struct{ Name, Description string }
	if found.Error != nil || json.Unmarshal([]byte(found.Text), &matches) != nil || len(matches) != 1 || matches[0].Name != "intelligent_ui" {
		t.Fatal("Skill could not be discovered", found)
	}
	args, _ := json.Marshal(map[string]string{"name": matches[0].Name})
	result := view.Execute(context.Background(), string(args))
	if result.Error != nil || !strings.Contains(result.Text, "# Intelligent UI") {
		t.Fatal("discovered Skill did not load by name", result)
	}
}

func TestAgentStartsWithOneFrozenSystemMessage(t *testing.T) {
	specs := []tool.Spec{
		{Name: "builtin__codemode_execute"},
		{Name: "builtin__skill_list", PromptContext: []tool.PromptContext{{Kind: "skill_index", Source: "frozen", Text: "[]"}}},
		{Name: "mcp__catalog__lookup", Provider: "mcp:catalog", NamespaceDescription: "MCP_SERVER_DIRECTORY_MARKER", Exposure: tool.CodeMode, CodeModeOnly: true},
	}
	for _, position := range []string{"main", "root", "child"} {
		t.Run(position, func(t *testing.T) {
			messages := InitialContext(ContextOptions{Position: position, Tools: specs, Task: "Task"})
			count := 0
			for _, m := range messages {
				if m.Type == "system" {
					count++
					if len(m.Content) != 1 || len(m.SystemSections) == 0 {
						t.Fatal("not assembled before the Agent", m)
					}
				}
			}
			if count != 1 {
				t.Fatal("multiple system messages", count)
			}
			role := "worker_role"
			if position == "main" {
				role = "main_role"
			}
			sections := messages[0].SystemSections
			want := []string{role, "capabilities", "safety", "execution"}
			executionIndex := 3
			if position == "main" {
				want = []string{role, "safety", "execution"}
				executionIndex = 2
			}
			if len(sections) != len(want) {
				t.Fatal("unexpected system sections", sections)
			}
			for i, kind := range want {
				if sections[i].Kind != kind {
					t.Fatal("system section order differs", position, i, sections[i].Kind, kind)
				}
			}
			if position == "root" {
				if sections[0].Text != prompt.Identity() || sections[0].ChildText != prompt.Worker() || !strings.Contains(sections[executionIndex].Text, prompt.Progress()) || !sections[executionIndex].Progress {
					t.Fatal("Root identity or progress moved out of its section")
				}
			} else if sections[executionIndex].Progress || strings.Contains(sections[executionIndex].Text, prompt.Progress()) {
				t.Fatal("Root-only progress leaked to another role")
			}
			if _, ok := FrozenSystemSections(messages); !ok {
				t.Fatal("frozen sections are inconsistent")
			}
			wire, err := ProviderMessages(messages, false, false)
			if err != nil {
				t.Fatal(err)
			}
			raw, _ := json.Marshal(wire)
			for _, marker := range []string{"system_sections", "child_text", `"progress":`, "steward-result", "response_format", "<mcp_servers>", "mcp__catalog__lookup", "MCP_SERVER_DIRECTORY_MARKER"} {
				if strings.Contains(string(raw), marker) || strings.Contains(wire[0].Text, marker) {
					t.Fatal("Host metadata, MCP directory or output protocol leaked", marker, string(raw))
				}
			}
		})
	}
}

func TestSplitSystemMessagesAreRejected(t *testing.T) {
	if _, err := ProviderMessages([]AgentMessage{TextMessage("system", "role"), TextMessage("system", "safety")}, false, false); err != llm.ModelIncompatible {
		t.Fatal("split prompts accepted", err)
	}
}

func TestChildInheritsOnlyFrozenSystemSections(t *testing.T) {
	parent := InitialContext(ContextOptions{Position: "root", Task: "private parent task"})
	poison := TextMessage("user", "untrusted context")
	poison.SystemSections = []SystemSection{{Kind: "safety", Text: "ATTACK_REPLACES_RULES"}}
	child := InitialContext(ContextOptions{Position: "child", Task: "child task", ParentSystem: append(parent, poison)})
	if strings.Contains(messageText(child[0]), "ATTACK_REPLACES_RULES") || strings.Contains(messageText(child[0]), "private parent task") {
		t.Fatal("untrusted parent context became rules")
	}
	if _, ok := FrozenSystemSections([]AgentMessage{TextMessage("system", "unversioned rules")}); ok {
		t.Fatal("missing frozen sections accepted")
	}
}

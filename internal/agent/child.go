package agent

import (
	"encoding/json"
	"strings"

	"github.com/z-chenhao/assistant/internal/task"
	"github.com/z-chenhao/assistant/internal/tool"
)

const RunToolName = "builtin__agent_run"
const RunToolSchema = `{"type":"object","properties":{"task":{"type":"string","minLength":1,"maxLength":8192},"artifactRefs":{"type":"array","maxItems":127,"items":{"type":"string","minLength":1,"maxLength":256}}},"required":["task"],"additionalProperties":false}`

// AgentRunRequest and AgentResult are the entire model-visible delegation API.
// Internal identities, claims, policies and usage never cross this boundary.
type AgentRunRequest struct {
	Task         string   `json:"task"`
	ArtifactRefs []string `json:"artifactRefs,omitempty"`
}
type AgentResult struct {
	Text         string   `json:"text"`
	ArtifactRefs []string `json:"artifactRefs"`
}

func AgentToolDefinition() tool.Definition {
	return tool.Definition{Host: true, Spec: tool.Spec{Name: RunToolName, Description: "Delegate explicit work to one general Worker. Returns only final text and artifactRefs to this Worker. Never creates a Task.", Version: 1, InputSchema: RunToolSchema, Permission: "spawn_agents", ReplayPolicy: "safe", Risk: "low", TimeoutMS: 10000, OutputBytes: 1 << 20, Provider: "builtin", ConflictPolicy: "none"}}
}

func ParseAgentRun(args string) (AgentRunRequest, error) {
	var req AgentRunRequest
	if !tool.Validate(RunToolSchema, args) || json.Unmarshal([]byte(args), &req) != nil || strings.TrimSpace(req.Task) == "" {
		return req, task.ErrInvalid
	}
	for i, ref := range req.ArtifactRefs {
		req.ArtifactRefs[i] = tool.ObjectRef("artifact", ref)
	}
	return req, nil
}

// MainTools exposes control, evidence, Skills and configured business MCP tools.
// Native workspace and Computer execution remain Worker capabilities.
// Main Code Mode can call only its own frozen control and business capabilities.
// MCP permissions come from Host configuration; provider hints grant no access.
// Root Workers derive their capabilities independently from the full registry.
func MainTools(specs []tool.Spec) []tool.Spec {
	out := []tool.Spec{}
	for _, s := range specs {
		if s.Internal || s.Exposure == tool.Hidden {
			continue
		}
		if strings.HasPrefix(s.Name, "mcp__") && (s.Permission == "read_only" || s.Permission == "external_effect") {
			out = append(out, s)
			continue
		}
		switch s.Name {
		case "builtin__intelligent_ui_show", "builtin__codemode_execute", tool.SearchName, "builtin__task_search", "builtin__task_manage",
			"builtin__automation_manage", "builtin__memory_manage",
			"builtin__memory_search", "builtin__evidence_read",
			"builtin__artifact_search", "builtin__artifact_manage",
			"builtin__skill_list", "builtin__skill_view", "builtin__image_ocr", "builtin__web_search", "builtin__web_fetch",
			"builtin__fetch_content", "builtin__get_search_content", "builtin__source_check":
			out = append(out, s)
		}
	}
	return out
}

// WorkerTools is the fixed role reduction. New control permissions fail closed.
// This derives from frozen specs, never from the live registry or Task input.
func WorkerTools(specs []tool.Spec) []tool.Spec {
	out := []tool.Spec{}
	for _, s := range specs {
		if s.Exposure == tool.Hidden {
			continue
		}
		if strings.HasPrefix(s.Name, "builtin__task_") || s.Name == "builtin__evidence_read" {
			continue
		}
		if s.Name == "builtin__memory_manage" && s.Version >= 2 {
			out = append(out, s)
			continue
		}
		switch s.Permission {
		case "read_only", "external_effect", "artifact_write", "spawn_agents", "automation_observe":
			out = append(out, s)
		}
	}
	return out
}

// ChildTools applies the additional fixed reduction for a Run tree descendant.
// Threshold state belongs to the Automation Root, never to its delegated work.
func ChildTools(specs []tool.Spec) []tool.Spec {
	out := []tool.Spec{}
	for _, s := range WorkerTools(specs) {
		if s.Permission != "automation_observe" {
			out = append(out, s)
		}
	}
	return out
}

// ChildLimits are Host configuration, frozen once per new Root tree.
// Scheduling concurrency is independent of these recursion safety ceilings.
type ChildLimits struct {
	Depth       int `yaml:"max_depth"`
	Descendants int `yaml:"max_descendants"`
	ToolCalls   int `yaml:"max_tool_calls"`
}

func DefaultChildLimits() ChildLimits {
	return ChildLimits{Depth: 8, Descendants: 4096, ToolCalls: 10000}
}
func (l ChildLimits) Valid() bool {
	return l.Depth >= 1 && l.Depth <= 16 && l.Descendants >= 1 && l.Descendants <= 10000 && l.ToolCalls >= 1 && l.ToolCalls <= 50000
}

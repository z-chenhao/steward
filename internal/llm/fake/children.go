package fake

import (
	"encoding/json"
	"strings"

	"github.com/z-chenhao/assistant/internal/agent"
	artifacttools "github.com/z-chenhao/assistant/internal/artifact/tools"
	"github.com/z-chenhao/assistant/internal/llm"
	"github.com/z-chenhao/assistant/internal/tool"
	toolfake "github.com/z-chenhao/assistant/internal/tool/fake"
)

// childrenReply drives the production Direct Tool path. It supplies protocol
// evidence, not model-quality scores or a JavaScript/Durable Replay claim.
func childrenReply(req llm.GenerationRequest) (llm.GenerationResult, error) {
	last := req.Messages[len(req.Messages)-1]
	worker, root := false, false
	input := ""
	var prior llm.ToolCall
	for _, m := range req.Messages {
		worker = worker || workerMessage(m)
		root = root || strings.Contains(m.Text, "Task anchor:")
		if m.Role == "user" && m.Origin == "" && input == "" {
			input = m.Text
			input, _, _ = strings.Cut(input, "\nExplicit artifact references: ")
			input, _, _ = strings.Cut(input, "\nExplicit artifact IDs: ")
		}
		for _, c := range m.ToolCalls {
			prior = c
		}
	}
	call := func(name string, value any) llm.GenerationResult {
		raw, _ := json.Marshal(value)
		return llm.GenerationResult{ToolCalls: []llm.ToolCall{{ID: "fixture-child-call", Name: name, Arguments: string(raw)}}}
	}
	if !worker {
		if !hasAppendedUser(req.Messages) {
			for _, m := range req.Messages {
				if m.Origin == "runtime" {
					_, payload, ok := strings.Cut(m.Text, "\n")
					var update struct {
						Content []struct {
							Text string `json:"text"`
						} `json:"content"`
					}
					if ok && json.Unmarshal([]byte(payload), &update) == nil {
						for _, part := range update.Content {
							if strings.HasPrefix(part.Text, "Batch aggregate:") {
								return llm.GenerationResult{Text: "Main follow-up: " + part.Text}, nil
							}
						}
					}
				}
			}
		}
		if input == "Forge child" && last.Role != "tool" {
			return call(agent.RunToolName, map[string]string{"task": "forbidden"}), nil
		}
		return tasksReply(req)
	}
	if (root && strings.Contains(input, "Batch")) || strings.HasPrefix(input, "Batch item ") {
		return batchReply(req, input, root)
	}
	if last.Role == "tool" {
		if !root && input == "Unknown child" {
			return llm.GenerationResult{}, llm.ModelNotFound
		}
		if root && strings.Contains(input, "Child artifact") && prior.Name == artifacttools.Manage && strings.Contains(prior.Arguments, `"operation":"save"`) {
			var output tool.Result
			if json.Unmarshal([]byte(last.Text), &output) != nil || len(output.ArtifactRefs) != 1 {
				return llm.GenerationResult{}, llm.InvalidProviderResponse
			}
			return call(agent.RunToolName, agent.AgentRunRequest{Task: "Read child artifact", ArtifactRefs: output.ArtifactRefs}), nil
		}
		if !root && input == "Read child artifact" && prior.Name == artifacttools.Manage && strings.Contains(prior.Arguments, `"operation":"read"`) {
			if refs := explicitTaskRefs(req.Messages); len(refs) > 0 {
				return call(artifacttools.Manage, map[string]any{"operation": "publish", "artifactRefs": refs}), nil
			}
		}
		if root && strings.Contains(input, "Child artifact") && prior.Name == agent.RunToolName {
			var result agent.AgentResult
			if json.Unmarshal([]byte(last.Text), &result) != nil {
				return llm.GenerationResult{}, llm.InvalidProviderResponse
			}
			return call(artifacttools.Manage, map[string]any{"operation": "publish", "artifactRefs": result.ArtifactRefs}), nil
		}
		return llm.GenerationResult{Text: "Worker aggregated: " + last.Text}, nil
	}
	if root {
		switch {
		case strings.Contains(input, "Spend final"):
			return llm.GenerationResult{Text: "frozen monetary final"}, nil
		case strings.Contains(input, "Child large"):
			return call(agent.RunToolName, agent.AgentRunRequest{Task: "Large child"}), nil
		case strings.Contains(input, "Child artifact"):
			return call(artifacttools.Manage, map[string]string{"operation": "save", "name": "child-evidence.txt", "mediaType": "text/plain", "text": "explicit artifact input"}), nil
		case strings.Contains(input, "Child invalid"):
			return call(agent.RunToolName, map[string]string{"task": "forbidden", "model": "forged"}), nil
		case strings.Contains(input, "Child inaccessible"):
			return call(agent.RunToolName, agent.AgentRunRequest{Task: "forbidden ref", ArtifactRefs: []string{"artifact:forged"}}), nil
		case strings.Contains(input, "Child failure"):
			return call(agent.RunToolName, agent.AgentRunRequest{Task: "Fail child"}), nil
		case strings.Contains(input, "Child unknown"):
			return call(agent.RunToolName, agent.AgentRunRequest{Task: "Unknown child"}), nil
		case strings.Contains(input, "Child approval"):
			return call(agent.RunToolName, agent.AgentRunRequest{Task: "Approve child"}), nil
		case strings.Contains(input, "Child hold"):
			return call(agent.RunToolName, agent.AgentRunRequest{Task: "Hold child"}), nil
		default:
			return call(agent.RunToolName, agent.AgentRunRequest{Task: "Branch explicit input"}), nil
		}
	}
	switch input {
	case "Large child":
		return llm.GenerationResult{Text: strings.Repeat("bounded detail ", 1800)}, nil
	case "Hold branch":
		return call(agent.RunToolName, agent.AgentRunRequest{Task: "Hold child"}), nil
	case "Branch explicit input":
		return call(agent.RunToolName, agent.AgentRunRequest{Task: "Leaf explicit input"}), nil
	case "Leaf explicit input":
		return llm.GenerationResult{Text: "Leaf final result"}, nil
	case "Fail child":
		return llm.GenerationResult{}, llm.ModelNotFound
	case "Unknown child":
		return call(toolfake.Name, map[string]string{"target": "Alex", "text": "uncertain Child action", "behavior": "unknown"}), nil
	case "Approve child":
		return call(toolfake.Name, map[string]string{"target": "Alex", "text": "Child frozen action"}), nil
	case "Read child artifact":
		if refs := explicitTaskRefs(req.Messages); len(refs) > 0 {
			return call(artifacttools.Manage, map[string]string{"operation": "read", "artifactRef": refs[0]}), nil
		}
	}
	return llm.GenerationResult{}, llm.InvalidProviderResponse
}

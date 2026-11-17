package fake

import (
	"encoding/json"
	"strings"

	"github.com/z-chenhao/assistant/internal/agent"
	"github.com/z-chenhao/assistant/internal/llm"
	"github.com/z-chenhao/assistant/internal/recall"
	"github.com/z-chenhao/assistant/internal/task"
)

// Scripted transport evidence, never a model-quality or full SC-08 verdict.
func recallReply(req llm.GenerationRequest) llm.GenerationResult {
	worker, root, followup := false, false, false
	var prior llm.ToolCall
	for _, m := range req.Messages {
		worker = worker || workerMessage(m)
		root = root || strings.HasPrefix(m.Text, "Task anchor:")
		followup = followup || strings.HasPrefix(m.Text, "Runtime task update") || contextSummary(m).TaskUpdate
		for _, c := range m.ToolCalls {
			prior = c
		}
	}
	call := func(name string, args any) llm.GenerationResult {
		raw, _ := json.Marshal(args)
		return llm.GenerationResult{ToolCalls: []llm.ToolCall{{ID: "recall-fixture-call", Name: name, Arguments: string(raw)}}}
	}
	if followup {
		return llm.GenerationResult{Text: "Main follow-up: independent recall completed."}
	}
	if prior.Name == "" {
		return call(recall.ToolName, map[string]any{"query": "tea", "limit": 10})
	}
	if prior.Name == recall.ToolName {
		if !worker {
			return call(task.ManageName, task.ManageRequest{Operation: "create", Task: "Retrieve tea context independently and delegate a separate recall."})
		}
		if root {
			return call(agent.RunToolName, agent.AgentRunRequest{Task: "Retrieve tea context independently; return the cited evidence."})
		}
		return llm.GenerationResult{Text: "Child cited independent durable context."}
	}
	if prior.Name == agent.RunToolName {
		return llm.GenerationResult{Text: "Root combined its own recall with the explicit Child result."}
	}
	return llm.GenerationResult{Text: "Main accepted the work."}
}

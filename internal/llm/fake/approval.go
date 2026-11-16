package fake

import (
	"encoding/json"
	"strings"

	"github.com/z-chenhao/assistant/internal/llm"
	"github.com/z-chenhao/assistant/internal/task"
	"github.com/z-chenhao/assistant/internal/tool"
)

func approvalReply(r llm.GenerationRequest) llm.GenerationResult {
	last := r.Messages[len(r.Messages)-1]
	root, followup := false, false
	user := ""
	inputRef := ""
	for _, m := range r.Messages {
		root = root || workerMessage(m)
		followup = followup || contextSummary(m).TaskUpdate || m.Origin == "runtime" && strings.HasPrefix(m.Text, "Runtime task update")
		if m.Role == "user" && m.Origin == "" {
			user, _, _ = strings.Cut(m.Text, "\n\n<recall_memory>")
			if m.Source != nil {
				inputRef = tool.ObjectID(m.Source.Ref)
			}
		}
	}
	if !root && inputRef != "" {
		for _, m := range r.Messages {
			if summary := contextSummary(m); summary.InputRef == inputRef {
				for _, id := range summary.Completed {
					if id == "effect" {
						return llm.GenerationResult{Text: "Observed effect result: accepted Task receipt retained in working context."}
					}
				}
			}
		}
	}
	if followup && !hasAppendedUser(r.Messages) {
		return llm.GenerationResult{Text: "Observed Worker update: " + last.Text}
	}
	if !root && (strings.HasPrefix(user, "Pause: ") || strings.HasPrefix(user, "Cancel: ")) {
		result, _ := tasksReply(r)
		return result
	}
	if !root && last.Role == "tool" && last.ToolCallID == "effect-search" {
		var matches []task.Summary
		_ = json.Unmarshal([]byte(last.Text), &matches)
		request := task.ManageRequest{Operation: "create", Task: "Effect fixture:\n" + user}
		if len(matches) == 1 {
			request = task.ManageRequest{Operation: "append", TaskRef: matches[0].TaskRef, Instruction: "Effect fixture:\n" + user}
		}
		raw, _ := json.Marshal(request)
		return llm.GenerationResult{ToolCalls: []llm.ToolCall{{ID: "effect", Name: task.ManageName, Arguments: string(raw)}}}
	}
	if !root && last.Role == "tool" {
		return llm.GenerationResult{Text: "Observed effect result: " + last.Text}
	}
	args := last.Text
	name := "mcp__fixture__send"
	if root {
		const marker = "Effect fixture:\n"
		instruction := false
		for _, m := range r.Messages {
			if m.Role == "user" && (strings.HasPrefix(m.Text, "Task anchor:") || strings.HasPrefix(m.Text, "Task instruction:")) {
				args, _, _ = strings.Cut(m.Text, "\nArtifact references:")
				instruction = strings.HasPrefix(m.Text, "Task instruction:")
			}
			if m.Source != nil && m.Source.Kind == "task_instruction" {
				var payload struct {
					Content struct {
						Instruction string `json:"instruction"`
					} `json:"content"`
				}
				_, raw, _ := strings.Cut(m.Text, "\n")
				if json.Unmarshal([]byte(raw), &payload) == nil {
					args = payload.Content.Instruction
					instruction = true
				}
			}
		}
		if at := strings.LastIndex(args, marker); at >= 0 {
			args = args[at+len(marker):]
		}
		// Execute the scripted JSON plan, not inline recalled evidence or Host
		// attachment hints following the current instruction.
		var plan json.RawMessage
		if json.NewDecoder(strings.NewReader(args)).Decode(&plan) == nil {
			args = string(plan)
		}
		if last.Role == "tool" {
			previous := ""
			for _, m := range r.Messages {
				for _, call := range m.ToolCalls {
					if call.Name == name {
						previous = call.Arguments
					}
				}
			}
			if !instruction || previous == args {
				return llm.GenerationResult{Text: "Observed effect result: " + last.Text}
			}
		}
	} else {
		request := task.ManageRequest{Operation: "create", Task: "Effect fixture:\n" + strings.TrimPrefix(args, "Delegate effect: ")}
		if !strings.HasPrefix(args, "Delegate effect: ") {
			return llm.GenerationResult{ToolCalls: []llm.ToolCall{{ID: "effect-search", Name: task.SearchName, Arguments: `{"query":"Effect fixture","scope":"active"}`}}}
		}
		raw, _ := json.Marshal(request)
		args = string(raw)
		name = task.ManageName
	}
	return llm.GenerationResult{ToolCalls: []llm.ToolCall{{ID: "effect", Name: name, Arguments: args}}}
}

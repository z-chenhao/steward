package fake

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/z-chenhao/assistant/internal/llm"
	"github.com/z-chenhao/assistant/internal/task"
	"github.com/z-chenhao/assistant/internal/tool"
)

// A follow-up contains historical user text. Only input after its runtime notice
// is fresh steering; accepted notices remain in context on later model steps.
func hasAppendedUser(messages []llm.Message) bool {
	user, notice := -1, -1
	summarizedInput := ""
	for i, m := range messages {
		if summary := contextSummary(m); summary.TaskUpdate {
			notice, summarizedInput = i, summary.InputRef
		} else if m.Origin == "runtime" && strings.HasPrefix(m.Text, "Runtime task update") {
			notice = i
			summarizedInput = ""
		} else if m.Role == "user" && m.Origin == "" {
			user = i
		}
	}
	// Compaction may place a retained input after its summary. Identify that
	// input by its source identity rather than mistaking its position for time.
	if summarizedInput != "" && user >= 0 && messages[user].Source != nil {
		return tool.ObjectID(messages[user].Source.Ref) != summarizedInput
	}
	return notice >= 0 && user > notice
}

// tasksReply is an explicitly selected scripted policy, never a production
// provider. Protocol assertions and state readback, not prose, are its evidence.
func tasksReply(r llm.GenerationRequest) (llm.GenerationResult, error) {
	root, followup := false, false
	for _, m := range r.Messages {
		root = root || workerMessage(m)
		followup = followup || m.Origin == "runtime" && strings.HasPrefix(m.Text, "Runtime task update")
	}
	last := r.Messages[len(r.Messages)-1]
	call := func(name string, value any) llm.GenerationResult {
		raw, _ := json.Marshal(value)
		return llm.GenerationResult{ToolCalls: []llm.ToolCall{{ID: "fixture-call", Name: name, Arguments: string(raw)}}}
	}
	if root {
		for _, m := range r.Messages {
			if strings.Contains(m.Text, "Fail worker:") {
				return llm.GenerationResult{}, llm.ModelNotFound
			}
		}
		if last.Role == "tool" {
			return llm.GenerationResult{Text: "Worker result: " + last.Text}, nil
		}
		result := call("mcp__fixture__echo", struct {
			Text string `json:"text"`
		}{last.Text})
		result.Text = "Requirements reviewed; checking the supporting evidence."
		return result, nil
	}
	if followup && !hasAppendedUser(r.Messages) {
		return llm.GenerationResult{Text: "Main follow-up: the delegated work has returned."}, nil
	}
	user := ""
	for _, m := range r.Messages {
		if m.Role == "user" && m.Origin == "" {
			user = m.Text
		}
	}
	if last.Role == "tool" {
		var prior llm.ToolCall
		for _, m := range r.Messages {
			if len(m.ToolCalls) > 0 {
				prior = m.ToolCalls[len(m.ToolCalls)-1]
			}
		}
		if prior.Name == task.SearchName {
			var results []task.Summary
			if json.Unmarshal([]byte(last.Text), &results) != nil {
				return llm.GenerationResult{Text: "Search was not available."}, nil
			}
			if strings.HasPrefix(user, "Pause: ") || strings.HasPrefix(user, "Cancel: ") || strings.HasPrefix(user, "Complete: ") {
				if len(results) != 1 {
					return llm.GenerationResult{Text: "Please clarify which work you mean."}, nil
				}
				op := "pause"
				if strings.HasPrefix(user, "Cancel: ") {
					op = "cancel"
				}
				if strings.HasPrefix(user, "Complete: ") {
					op = "complete"
				}
				result := call(task.ManageName, task.ManageRequest{Operation: op, TaskRef: results[0].TaskRef})
				if op == "complete" {
					second := result.ToolCalls[0]
					second.ID = "duplicate-complete"
					result.ToolCalls = append(result.ToolCalls, second)
				}
				return result, nil
			}
			if strings.HasPrefix(user, "Continue: ") {
				if len(results) != 1 {
					return llm.GenerationResult{Text: fmt.Sprintf("Please clarify which of the %d matching works you mean.", len(results))}, nil
				}
				pieces := strings.SplitN(strings.TrimPrefix(user, "Continue: "), " | ", 2)
				instruction := "继续"
				if len(pieces) == 2 {
					instruction = pieces[1]
				}
				return call(task.ManageName, task.ManageRequest{Operation: "append", TaskRef: results[0].TaskRef, Instruction: instruction}), nil
			}
			return llm.GenerationResult{Text: fmt.Sprintf("Found %d matching works.", len(results))}, nil
		}
		return llm.GenerationResult{Text: "Main accepted the work."}, nil
	}
	switch {
	case strings.HasPrefix(user, "Question: "):
		parts := strings.SplitN(strings.TrimPrefix(user, "Question: "), " | ", 2)
		if len(parts) != 2 {
			return llm.GenerationResult{Text: "Question needs TaskRef and prompt."}, nil
		}
		return call(task.ManageName, task.ManageRequest{Operation: "request_input", TaskRef: parts[0], Prompt: parts[1]}), nil
	case strings.HasPrefix(user, "Review: "):
		parts := strings.SplitN(strings.TrimPrefix(user, "Review: "), " | ", 3)
		if len(parts) != 3 {
			return llm.GenerationResult{Text: "Review needs TaskRef, ArtifactRef and prompt."}, nil
		}
		return call(task.ManageName, task.ManageRequest{Operation: "request_review", TaskRef: parts[0], ArtifactRefs: []string{parts[1]}, Prompt: strings.SplitN(parts[2], "\nArtifact reference (content not loaded):", 2)[0]}), nil
	case strings.HasPrefix(user, "Response to a saved Task question or artifact review."):
		var response struct {
			TaskRef      string   `json:"task_ref"`
			FeedbackRef  string   `json:"feedback_ref"`
			ArtifactRefs []string `json:"artifact_refs"`
			Answer       string   `json:"answer"`
		}
		_, data, _ := strings.Cut(user, "\n")
		if json.NewDecoder(strings.NewReader(data)).Decode(&response) != nil {
			return llm.GenerationResult{}, llm.InvalidProviderResponse
		}
		return call(task.ManageName, task.ManageRequest{Operation: "append", TaskRef: response.TaskRef, FeedbackRef: response.FeedbackRef, Instruction: response.Answer, ArtifactRefs: response.ArtifactRefs}), nil
	case strings.HasPrefix(user, "Ongoing: "):
		return call(task.ManageName, task.ManageRequest{Operation: "create", Task: strings.TrimPrefix(user, "Ongoing: "), CompletionPolicy: "ongoing"}), nil
	case strings.HasPrefix(user, "Delegate: "), strings.HasPrefix(user, "Duplicate: "):
		brief := strings.TrimPrefix(strings.TrimPrefix(user, "Delegate: "), "Duplicate: ")
		result := call(task.ManageName, task.ManageRequest{Operation: "create", Task: brief})
		if strings.HasPrefix(user, "Duplicate: ") {
			second := result.ToolCalls[0]
			second.ID = "duplicate-call"
			result.ToolCalls = append(result.ToolCalls, second)
		}
		return result, nil
	case strings.HasPrefix(user, "Delegate many: "):
		result := llm.GenerationResult{}
		for i, brief := range strings.Split(strings.TrimPrefix(user, "Delegate many: "), " | ") {
			c := call(task.ManageName, task.ManageRequest{Operation: "create", Task: brief}).ToolCalls[0]
			c.ID = fmt.Sprint(i)
			result.ToolCalls = append(result.ToolCalls, c)
		}
		return result, nil
	case strings.HasPrefix(user, "Find: "):
		return call(task.SearchName, task.SearchRequest{Query: strings.TrimPrefix(user, "Find: ")}), nil
	case strings.HasPrefix(user, "Continue: "):
		query := strings.SplitN(strings.TrimPrefix(user, "Continue: "), " | ", 2)[0]
		return call(task.SearchName, task.SearchRequest{Query: query}), nil
	case strings.HasPrefix(user, "Complete: "):
		return call(task.SearchName, task.SearchRequest{Query: strings.TrimPrefix(user, "Complete: ")}), nil
	case strings.HasPrefix(user, "Pause: "), strings.HasPrefix(user, "Cancel: "):
		return call(task.SearchName, task.SearchRequest{Query: strings.TrimPrefix(strings.TrimPrefix(user, "Pause: "), "Cancel: ")}), nil
	default:
		return llm.GenerationResult{Text: "Main synchronous answer."}, nil
	}
}

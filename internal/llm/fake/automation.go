package fake

import (
	"encoding/json"
	"github.com/z-chenhao/assistant/internal/automation"
	"github.com/z-chenhao/assistant/internal/llm"
	"strings"
)

// Explicit synthetic GC-14 model fixture. It records actual Tool calls; it
// cannot write domain state or choose a fire/Run identity.
func automationReply(r llm.GenerationRequest) llm.GenerationResult {
	last := r.Messages[len(r.Messages)-1]
	worker, followup := false, false
	for _, m := range r.Messages {
		worker = worker || workerMessage(m)
		followup = followup || m.Origin == "runtime" && strings.HasPrefix(m.Text, "Runtime task update")
	}
	call := func(name, args string) llm.GenerationResult {
		return llm.GenerationResult{ToolCalls: []llm.ToolCall{{ID: "fixture-call", Name: name, Arguments: args}}}
	}
	if worker {
		if last.Role == "tool" {
			return llm.GenerationResult{Text: "HKG-CDG 2026-12-01 observed; report only a new threshold entry."}
		}
		occurrence := ""
		for _, m := range r.Messages {
			if strings.Contains(m.Text, "Automation scheduled occurrence:") {
				occurrence = m.Text
			}
		}
		prices := map[string]int{"2026-10-16": 2200, "2026-10-17": 2190, "2026-10-18": 1750, "2026-10-19": 1740, "2026-10-20": 2050, "2026-10-21": 1780}
		for day, value := range prices {
			if strings.Contains(occurrence, "Record the observed integer value") && strings.Contains(occurrence, day) {
				raw, _ := json.Marshal(struct {
					Value int `json:"value"`
				}{value})
				result := call(automation.ObserveName, string(raw))
				result.ToolCalls = append(result.ToolCalls, llm.ToolCall{ID: "second-source", Name: automation.ObserveName, Arguments: string(raw)})
				return result
			}
		}
		return llm.GenerationResult{Text: "Reminder: review your long-term goals."}
	}
	if followup && !hasAppendedUser(r.Messages) {
		return llm.GenerationResult{Text: "Main: a meaningful Automation update is ready."}
	}
	if last.Role == "tool" {
		return llm.GenerationResult{Text: "Automation accepted."}
	}
	if strings.HasPrefix(last.Text, "Automation: ") {
		return call(automation.ManageName, strings.TrimPrefix(last.Text, "Automation: "))
	}
	return llm.GenerationResult{Text: "Automation fixture ready."}
}

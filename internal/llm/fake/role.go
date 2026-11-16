package fake

import (
	"strings"

	"github.com/z-chenhao/assistant/internal/llm"
)

// Scripted peers recognize both current and historical frozen Worker prompts.
// User text or retrieved evidence cannot switch a fixture's role.
func workerMessage(m llm.Message) bool {
	return m.Role == "system" && (strings.HasPrefix(m.Text, "You are a Root Worker") || strings.HasPrefix(m.Text, "You are a Subagent Worker") || strings.Contains(m.Text, "general Worker executing"))
}

// Private maintenance is identified by Host-owned request identity, not prompt
// wording. Independent method text must not be mistaken for ordinary Main work.
func privateMemoryRequest(req llm.GenerationRequest) bool {
	return len(req.Messages) > 1 && req.Messages[0].Role == "system" && req.Messages[1].Role == "user" && strings.HasPrefix(req.RunID, "memory:") && req.RunID == req.GenerationID && req.SessionID == req.RunID && len(req.Tools) == 0
}

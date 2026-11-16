package agent

import (
	"github.com/z-chenhao/assistant/internal/tool"
	"strings"
	"unicode/utf8"
)

// ApplyDelegationMethods changes only tool-derived capability guidance. Host
// proves Root identity and a new context; role, safety and inputs stay exact.
func ApplyDelegationMethods(messages []AgentMessage, specs []tool.Spec, instructions string) ([]AgentMessage, bool) {
	if strings.TrimSpace(instructions) == "" || len(instructions) > 4096 || !utf8.ValidString(instructions) {
		return nil, false
	}
	sections, ok := FrozenSystemSections(messages)
	if !ok || len(sections) != 4 || sections[0].Kind != "worker_role" || sections[1].Kind != "capabilities" || sections[2].Kind != "safety" || sections[3].Kind != "execution" {
		return nil, false
	}
	out := append([]AgentMessage(nil), messages...)
	sections = append([]SystemSection(nil), sections...)
	sections[1] = workerCapabilities(specs, instructions)
	texts := make([]string, len(sections))
	for i, s := range sections {
		texts[i] = s.Text
	}
	for i, m := range out {
		if m.Type == "system" {
			next := systemPart("system_prompt", strings.Join(texts, "\n\n"))
			next.SystemSections = sections
			out[i] = next
		}
	}
	return out, true
}

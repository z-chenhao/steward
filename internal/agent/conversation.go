package agent

import (
	"strings"
	"unicode/utf8"
)

// MainIdentity is a Host-owned identity prefix, independently of Main methods.
const MainIdentity = "You are Ghost, a personal assistant running inside Steward."

// MainMethods is an internal instruction contribution, never a role or message
// API. Safety and the other SystemSection fields are reconstructed by Host.
type MainMethods struct{ Role, Execution string }

// ApplyMainMethods runs only during a new Main's context preparation. Already
// ready/frozen requests are replayed without invoking or reapplying policy.
func ApplyMainMethods(messages []AgentMessage, p MainMethods) ([]AgentMessage, bool) {
	if len(p.Role) == 0 || len(p.Role) > 16384 || len(p.Execution) == 0 || len(p.Execution) > 16384 || !utf8.ValidString(p.Role) || !utf8.ValidString(p.Execution) {
		return nil, false
	}
	sections, ok := FrozenSystemSections(messages)
	if !ok || len(sections) != 3 {
		return nil, false
	}
	if sections[0].Kind != "main_role" || sections[1].Kind != "safety" || sections[2].Kind != "execution" || !strings.HasPrefix(sections[0].Text, MainIdentity) {
		return nil, false
	}
	out := append([]AgentMessage(nil), messages...)
	sections = append([]SystemSection(nil), sections...)
	sections[0].Text = MainIdentity + p.Role
	sections[2].Text = p.Execution + "\n</rules>"
	texts := []string{sections[0].Text, sections[1].Text, sections[2].Text}
	for i, m := range out {
		if m.Type == "system" {
			next := systemPart("system_prompt", strings.Join(texts, "\n\n"))
			next.SystemSections = sections
			out[i] = next
		}
	}
	return out, true
}

package agent

import (
	"strings"
	"unicode/utf8"
)

// ApplyRootProgressGuidance replaces only the Root-only execution contribution.
// An empty contribution disables the optional projection. ChildText, role,
// capabilities, safety and Task input remain the original Host-owned values.
// Callers must prove Root identity and an uninitialized context first.
func ApplyRootProgressGuidance(messages []AgentMessage, instructions string) ([]AgentMessage, bool) {
	if len(instructions) > 4096 || !utf8.ValidString(instructions) {
		return nil, false
	}
	sections, ok := FrozenSystemSections(messages)
	if !ok || len(sections) != 4 || sections[0].Kind != "worker_role" || sections[1].Kind != "capabilities" || sections[2].Kind != "safety" || sections[3].Kind != "execution" || !sections[3].Progress || sections[3].ChildText == "" {
		return nil, false
	}
	out := append([]AgentMessage(nil), messages...)
	sections = append([]SystemSection(nil), sections...)
	base := sections[3].ChildText
	if !strings.HasSuffix(base, "\n</rules>") {
		return nil, false
	}
	sections[3].Text, sections[3].Progress = base, instructions != ""
	if instructions != "" {
		sections[3].Text = strings.TrimSuffix(base, "\n</rules>") + "\n" + instructions + "\n</rules>"
	}
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

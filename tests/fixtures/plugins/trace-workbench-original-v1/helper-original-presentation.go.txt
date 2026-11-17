package web

import (
	"github.com/z-chenhao/assistant/internal/interaction"
	"strings"
)

func activeProfile(s Status) string {
	if s.ActiveModelProfileID == nil {
		return ""
	}
	return *s.ActiveModelProfileID
}
func canSend(s Status) bool {
	return s.Readiness == "ready" || s.Readiness == "" && (s.Status == "idle" || s.Status == "busy")
}
func bindingTarget(b interaction.Binding) string { return b.Channel + ":" + b.ID }
func toolLabel(name string) string {
	if name == "account_spend" {
		return "Continue model spending beyond the account boundary"
	}
	parts := strings.Split(strings.TrimPrefix(name, "builtin__"), "__")
	if len(parts) == 3 && parts[0] == "mcp" {
		return strings.ReplaceAll(parts[1], "_", " ") + " · " + strings.ReplaceAll(parts[2], "_", " ")
	}
	return strings.ReplaceAll(strings.TrimPrefix(name, "builtin__"), "_", " ")
}
func historyLabel(kind string) string {
	switch kind {
	case "instruction":
		return "Request updated"
	case "completed":
		return "Work round completed"
	case "failed":
		return "Work attempt failed"
	case "needs_input":
		return "Input requested"
	case "unknown":
		return "Outcome unconfirmed"
	}
	return kind
}

func tabLabel(name string) string {
	switch name {
	case "overview":
		return "Input / Output"
	case "metadata":
		return "Details"
	case "context":
		return "Context"
	case "wire":
		return "Requests"
	case "diff":
		return "Compare"
	}
	return name
}

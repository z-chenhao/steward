package mcp

import "strings"

// Exact original view for pre-namespace-service Run snapshots and explicit
// unbundled/default21 carriers. New managed snapshots never use this result as
// fallback. Delete when those retained consumers no longer need this policy.
func namespaceDescription(configured, instructions string) string {
	text := configured
	if text == "" {
		text = instructions
	}
	text = strings.SplitN(strings.TrimSpace(text), "\n", 2)[0]
	runes := []rune(text)
	if len(runes) > 512 {
		text = string(runes[:512]) + "…"
	}
	return text
}

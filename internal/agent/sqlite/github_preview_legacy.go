package sqlite

import "github.com/z-chenhao/assistant/internal/observer/github"

// Historical observations with NULL source_method_json and pre-opt-in owner
// binding views retain their original interpretation. Delete when no such
// observations/selections remain; queued Main messages already freeze the bytes.
func githubContextPreviewLegacyV1(text string) string {
	return github.Preview(text, 4096, "\n[Preview truncated; consult the original Task/source before relying on missing details.]")
}

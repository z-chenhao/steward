package sqlite

import (
	"encoding/json"
	"testing"
)

func TestHasForbiddenCodeStoreKeyStructuralDetection(t *testing.T) {
	// Ordinary string values mentioning "handle_token" must not be false-positived.
	if hasForbiddenCodeStoreKey(json.RawMessage(`{"note":"Do not pass \"handle_token\" in prose"}`)) {
		t.Fatal("prose mentioning handle_token was falsely rejected")
	}
	// Actual object keys (including nested and Unicode-escaped keys) must be rejected.
	for _, bad := range []string{
		`{"handle_token":"secret"}`,
		`{"nested":[{"handleToken":"secret"}]}`,
		`{"\u0068andle_token":"secret"}`,
	} {
		if !hasForbiddenCodeStoreKey(json.RawMessage(bad)) {
			t.Fatalf("expected forbidden key detection for %s", bad)
		}
	}
}

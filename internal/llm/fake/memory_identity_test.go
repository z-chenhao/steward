package fake

import (
	"testing"

	"github.com/z-chenhao/assistant/internal/llm"
	"github.com/z-chenhao/assistant/internal/memory"
)

func TestMemoryFixtureUsesPrivateRequestIdentity(t *testing.T) {
	req := llm.GenerationRequest{RunID: "memory:fixture", SessionID: "memory:fixture", GenerationID: "memory:fixture", Messages: []llm.Message{{Role: "system", Text: "independent versioned method"}, {Role: "user", Text: `{"mode":"dream"}`}}}
	if !privateMemoryRequest(req) {
		t.Fatal("versioned method was classified by old wording")
	}
	for _, variant := range []string{"ordinary", "different-session", "tool", "wrong-role"} {
		bad := req
		bad.Messages = append([]llm.Message(nil), req.Messages...)
		switch variant {
		case "ordinary":
			bad.RunID = "ordinary"
			bad.Messages[0].Text = memory.CurationInstruction
		case "different-session":
			bad.SessionID = "another"
		case "tool":
			bad.Tools = []llm.ToolSpec{{Name: "action"}}
		case "wrong-role":
			bad.Messages[0].Role = "user"
		}
		if privateMemoryRequest(bad) {
			t.Fatal("ordinary/unscoped request entered private memory fixture", variant)
		}
	}
}

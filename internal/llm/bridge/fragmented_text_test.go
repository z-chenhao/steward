package bridge

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/z-chenhao/assistant/internal/llm"
)

func TestNativeFragmentedTextWithinByteLimitPreservesLongReport(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		for range 5000 {
			fmt.Fprint(w, "data: {\"choices\":[{\"index\":0,\"delta\":{\"content\":\"a\"}}]}\n\n")
		}
		fmt.Fprint(w, "data: {\"choices\":[{\"index\":0,\"delta\":{},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n")
	}))
	defer server.Close()
	command, script := nativeBridgeProcess(t)
	client, err := OpenLocal(context.Background(), command, script, func(context.Context, llm.Snapshot) (llm.Connection, error) {
		return llm.Connection{Endpoint: server.URL + "/v1"}, nil
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	hash, _ := llm.EndpointHash(server.URL + "/v1")
	req := llm.GenerationRequest{GenerationID: "report", RunID: "report", SessionID: "report", Snapshot: llm.Snapshot{SnapshotID: "snapshot", ProviderKind: "openai_compatible", WireAPI: "openai_completions", EndpointHash: hash, ModelID: "fixture-model", ContextWindow: 8192, MaxOutputTokens: 2048, Capabilities: llm.Capabilities{TextInput: true}, Compatibility: llm.Compatibility{ToolResultFormat: "openai"}}, Messages: []llm.Message{{Role: "user", Text: "Write the report"}}, OutputLimitBytes: 64 << 10}
	events := 0
	result, err := client.Generate(context.Background(), req, func(e llm.GenerationEvent) {
		events++
		if _, err := json.Marshal(e); err != nil {
			t.Error(err)
		}
	})
	if err != nil || result.Text != strings.Repeat("a", 5000) || result.Clipped {
		t.Fatalf("legal provider chunking rejected a bounded report: %v bytes=%d", err, len(result.Text))
	}
	if events >= maxGenerationEvents {
		t.Fatal("native transport exhausted event budget", events)
	}
}

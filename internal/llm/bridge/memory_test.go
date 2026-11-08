package bridge

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/z-chenhao/assistant/internal/llm"
)

func TestLayeredMemoryResolvesOnlyForTransportAndOmitsDiagnosticBody(t *testing.T) {
	ctx := context.Background()
	const marker = "PRIVATE_MEMORY_TRANSPORT_MARKER"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var v any
		if err := json.NewDecoder(r.Body).Decode(&v); err != nil {
			t.Error(err)
		}
		raw, _ := json.Marshal(v)
		if !strings.Contains(string(raw), marker) || strings.Contains(string(raw), "memory_text") {
			t.Error("memory projection missing from actual request", string(raw))
		}
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, "data: {\"choices\":[{\"index\":0,\"delta\":{\"content\":\"seen\"},\"finish_reason\":null}]}\n\ndata: {\"choices\":[{\"index\":0,\"delta\":{},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n")
	}))
	defer server.Close()
	command, arg := nativeBridgeProcess(t)
	c, err := OpenLocal(ctx, command, arg, func(context.Context, llm.Snapshot) (llm.Connection, error) {
		return llm.Connection{Endpoint: server.URL + "/v1"}, nil
	}, func(_ context.Context, r llm.GenerationRequest, _ llm.Connection, _ llm.GenerationResult, _ time.Duration, _ error) error {
		raw, _ := json.Marshal(r)
		if strings.Contains(string(raw), marker) || r.Messages[0].Memory == nil {
			t.Error("observer received plaintext or lost frozen envelope")
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	observed := false
	c.SetDispatchObserver(func(_ llm.GenerationRequest, d llm.Dispatch) {
		if d.Phase == "started" {
			observed = true
			if d.Body != nil || d.Omitted != "Memory-bearing request body omitted" {
				t.Error("private diagnostic payload persisted", string(d.Body))
			}
		}
	})
	hash, _ := llm.EndpointHash(server.URL + "/v1")
	req := llm.GenerationRequest{GenerationID: "memory-read", RunID: "run", SessionID: "run", OwnerToken: "owner", Snapshot: llm.Snapshot{SnapshotID: "snapshot", ProviderKind: "openai_compatible", WireAPI: "openai_completions", EndpointHash: hash, ModelID: "fixture-model", ContextWindow: 8192, MaxOutputTokens: 1024, Capabilities: llm.Capabilities{TextInput: true}, Compatibility: llm.Compatibility{ToolResultFormat: "openai"}}, Messages: []llm.Message{{Role: "user", Text: "current request", Memory: &llm.MemoryText{Sealed: "sealed:test", Binding: "test", EncodedBytes: llm.MemoryEncodedBytes("\n" + marker)}}}, ResolveMemory: func(context.Context, llm.MemoryText) (string, error) { return "\n" + marker, nil }}
	result, err := c.Generate(ctx, req, nil)
	if err != nil || result.Text != "seen" || !observed {
		t.Fatal(result, err, observed)
	}
}

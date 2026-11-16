package rsiplugin

import (
	"context"
	"encoding/json"
	"sync"
	"testing"
)

func TestResourceClientRejectsChangedTruncatedAndAmbiguousHostBytes(t *testing.T) {
	for _, mode := range []string{"digest", "truncated", "oversized", "duplicate-json", "unknown-field"} {
		t.Run(mode, func(t *testing.T) {
			body := []byte(`{"text":"original"}`)
			if mode == "duplicate-json" {
				body = []byte(`{"text":"original","text":"forged"}`)
			}
			if mode == "unknown-field" {
				body = []byte(`{"text":"original","host_internal":"private"}`)
			}
			descriptor := Input{ID: "resource-1", Size: int64(len(body)), Digest: FileDigest(body)}
			if mode == "digest" {
				descriptor.Digest = FileDigest([]byte("changed"))
			}
			ctx := context.WithValue(context.Background(), resourceClientKey{}, resourceClient{mu: &sync.Mutex{}, exchange: func(_ context.Context, method string, _ any) (json.RawMessage, error) {
				if method == "resource" {
					raw, _ := json.Marshal(ResourceReply{Data: descriptor})
					return raw, nil
				}
				data := body
				if mode == "truncated" {
					data = nil
				}
				if mode == "oversized" {
					data = make([]byte, InputChunkBytes+1)
				}
				raw, _ := json.Marshal(InputChunk{Data: data})
				return raw, nil
			}})
			var output struct {
				Text string `json:"text"`
			}
			if err := CallResource(ctx, "steward.fixture/1", "original", map[string]string{"q": "accepted"}, &output); err == nil {
				t.Fatal("invalid resource content accepted", output)
			}
		})
	}
}
func TestResourceClientHasNoAmbientOrCancelledResourceAccess(t *testing.T) {
	var output any
	if err := CallResource(context.Background(), "steward.fixture/1", "key", nil, &output); err == nil {
		t.Fatal("missing domain grant accepted")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	called := false
	ctx = context.WithValue(ctx, resourceClientKey{}, resourceClient{mu: &sync.Mutex{}, exchange: func(context.Context, string, any) (json.RawMessage, error) {
		called = true
		return json.RawMessage(`{}`), nil
	}})
	if err := CallResource(ctx, "steward.fixture/1", "key", nil, &output); err != context.Canceled || called {
		t.Fatal("cancelled request touched a resource", called, err)
	}
}

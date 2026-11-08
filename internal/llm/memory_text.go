package llm

import (
	"context"
	"encoding/json"
)

// MemoryText keeps sensitive exact reads and injected memory sealed in durable
// requests. Only the Host resolver can expand them for provider transport.
type MemoryText struct {
	CodeMode     bool   `json:"code_mode,omitempty"`
	Ref          string `json:"ref,omitempty"`
	Sealed       string `json:"sealed"`
	Binding      string `json:"binding"`
	EncodedBytes int    `json:"encoded_bytes"`
}

// MemoryEncodedBytes includes JSON escaping in provider transport budgeting.
func MemoryEncodedBytes(text string) int { b, _ := json.Marshal(text); return len(b) }

func ResolveMemoryMessages(ctx context.Context, r GenerationRequest) ([]Message, error) {
	out := append([]Message(nil), r.Messages...)
	for i, m := range out {
		if m.Memory == nil {
			continue
		}
		if r.ResolveMemory == nil {
			return nil, ProviderUnavailable
		}
		plain, err := r.ResolveMemory(ctx, *m.Memory)
		if err != nil {
			return nil, err
		}
		if m.Memory.EncodedBytes != MemoryEncodedBytes(plain) || m.Memory.EncodedBytes > 1<<20 {
			return nil, ModelIncompatible
		}
		out[i].Text += plain
		out[i].Memory = nil
	}
	return out, nil
}

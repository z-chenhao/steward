package llm

import (
	"context"
	"testing"
)

func TestLayeredMemoryRejectsIncorrectTransportSize(t *testing.T) {
	plain := "private \\\"<>"
	envelope := &MemoryText{Sealed: "sealed", Binding: "binding", EncodedBytes: MemoryEncodedBytes(plain)}
	req := GenerationRequest{Messages: []Message{{Role: "tool", Memory: envelope}}, ResolveMemory: func(context.Context, MemoryText) (string, error) { return plain, nil }}
	out, err := ResolveMemoryMessages(context.Background(), req)
	if err != nil || out[0].Text != plain || out[0].Memory != nil || req.Messages[0].Memory == nil {
		t.Fatal(out, err)
	}
	envelope.EncodedBytes--
	if _, err := ResolveMemoryMessages(context.Background(), req); err != ModelIncompatible {
		t.Fatal("incorrect size accepted", err)
	}
}

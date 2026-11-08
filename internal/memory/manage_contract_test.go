package memory

import (
	"github.com/z-chenhao/assistant/internal/tool"
	"testing"
)

func TestMemoryWriteSchemaDeclaresRequiredFields(t *testing.T) {
	raw := `{"operation":"write","memory_ref":"example","revision":1,"content":"Corrected rule."}`
	if tool.Validate(Definition().Spec.InputSchema, raw) {
		t.Fatal("schema accepted missing layer/title")
	}
	if _, ok := Parse(raw); ok {
		t.Fatal("parser accepted incomplete write")
	}
	if MutationError(raw) != "Missing required memory fields: layer, title." {
		t.Fatal(MutationError(raw))
	}
}

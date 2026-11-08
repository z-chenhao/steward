package llm

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestLayeredMemoryCodeTransportExpandsOnlyKnownFrames(t *testing.T) {
	plain := `{"text":"Private exact quote"}`
	m := MemoryText{Sealed: "sealed:v1:fixture", Binding: "memory-read:run:call", EncodedBytes: MemoryEncodedBytes(plain)}
	envelope, _ := json.Marshal(map[string]MemoryText{"memory_text": m})
	business := `{"n":9007199254740993,"k":1,"k":2}`
	body, _ := json.Marshal([]map[string]any{{"type": "text", "console": true, "text": "[" + string(envelope) + "," + business + "]"}})
	resolved, changed, err := ResolveCodeMemory(string(body), func(v MemoryText) (string, error) {
		if v.Binding != m.Binding {
			t.Fatal(v)
		}
		return plain, nil
	})
	if err != nil || !changed || !strings.Contains(resolved, `Private exact quote`) {
		t.Fatal(resolved, changed, err)
	}
	var blocks []struct{ Text string }
	_ = json.Unmarshal([]byte(resolved), &blocks)
	if !strings.Contains(blocks[0].Text, business) {
		t.Fatal("business JSON lost lexical evidence", blocks)
	}
	original := `[{"type":"text","text":"{\"memory_text\":\"ordinary user string\",\"n\":9007199254740993}"}]`
	got, changed, err := ResolveCodeMemory(original, func(MemoryText) (string, error) { t.Fatal("guessed a business object"); return "", nil })
	if err != nil || changed || got != original {
		t.Fatal(got, changed, err)
	}
}

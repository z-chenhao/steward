package web

import (
	"encoding/json"
	"github.com/z-chenhao/assistant/internal/observability"
	"github.com/z-chenhao/assistant/internal/tool"
	"testing"
)

func TestToolPreviewKeepsEvidenceAndRawReceipt(t *testing.T) {
	evidence := `{"ref":"task:quoted","text":"{\"object_ids\":\"user text\"}","n":9007199254740993}`
	raw, _ := json.Marshal(tool.Result{ObjectIDs: "host", Text: evidence, ArtifactRefs: []string{"artifact:complete-id"}})
	body := observability.Body{Available: true, Content: string(raw)}
	p := previewToolOutput("builtin__web_search", body, 0)
	if p == nil || p.Texts[0] != evidence || p.Artifacts[0] != "artifact:complete-id" || body.Content != string(raw) {
		t.Fatal(p)
	}
	blocks, _ := json.Marshal([]map[string]string{{"type": "text", "text": evidence}})
	raw, _ = json.Marshal(tool.Result{Text: string(blocks)})
	body.Content = string(raw)
	p = previewToolOutput("builtin__codemode_execute", body, 0)
	if p == nil || p.Texts[0] != evidence {
		t.Fatal(p)
	}
	if p = previewToolOutput("external_tool", body, 0); p == nil || p.Texts[0] != string(blocks) {
		t.Fatal("guessed a foreign tool schema", p)
	}
	next := 10
	body.Next = &next
	if previewToolOutput("builtin__web_search", body, 0) != nil {
		t.Fatal("parsed incomplete receipt")
	}
}

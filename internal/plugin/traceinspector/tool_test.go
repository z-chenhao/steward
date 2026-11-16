package traceinspector

import (
	"encoding/json"
	"reflect"
	"testing"

	"github.com/z-chenhao/assistant/internal/plugin/traceinspector/contract"
)

func TestToolPreviewPreservesBusinessJSONAndRejectsPartialOrSealedReceipts(t *testing.T) {
	block := `[{"type":"text","text":"business JSON string"}]`
	raw, _ := json.Marshal(map[string]any{"text": block, "artifact_refs": []string{"artifact:original"}, "image_refs": []string{"artifact:image"}})
	f := contract.ToolFacts{Available: true, Complete: true, Content: string(raw), Name: "builtin__codemode_execute"}
	preview := ToolPreview(f)
	if preview == nil || !reflect.DeepEqual(preview.Texts, []string{"business JSON string"}) || !reflect.DeepEqual(preview.Artifacts, []string{"artifact:original"}) {
		t.Fatal(preview)
	}
	f.Name = "mcp__business__query"
	preview = ToolPreview(f)
	if preview == nil || !reflect.DeepEqual(preview.Texts, []string{block}) {
		t.Fatal("business payload recursively interpreted", preview)
	}
	for _, variant := range []string{"unavailable", "incomplete", "offset", "sealed"} {
		bad := f
		switch variant {
		case "unavailable":
			bad.Available = false
		case "incomplete":
			bad.Complete = false
		case "offset":
			bad.Offset = 1
		case "sealed":
			bad.Content = `{"text":"sealed:PRIVATE_DO_NOT_DECODE"}`
		}
		if ToolPreview(bad) != nil {
			t.Fatal("nonpublic partial receipt interpreted", variant)
		}
	}
}

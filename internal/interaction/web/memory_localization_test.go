package web

import (
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/z-chenhao/assistant/internal/memory"
)

func TestMemoryLocalizationMatchesCapturedOriginalAndIteratesWithoutNativeDictionary(t *testing.T) {
	raw, e := os.ReadFile("../../../tests/fixtures/plugins/memory-inspector-original-v1/localization-v1.json")
	if e != nil {
		t.Fatal(e)
	}
	var original []memory.InspectionTranslation
	if e = json.Unmarshal(raw, &original); e != nil {
		t.Fatal(e)
	}
	v := originalMemoryViewFixture(t, "document-detail")
	for _, entry := range original {
		if got := memoryTranslate(v, "en", entry.English); got != entry.English {
			t.Errorf("English %q changed", entry.English)
		}
		if got := memoryTranslate(v, "zh-CN", entry.English); got != entry.Chinese {
			t.Errorf("captured Chinese %q: got %q want %q", entry.English, got, entry.Chinese)
		}
	}
	changed := false
	for i := range v.Presentation.Copy {
		if v.Presentation.Copy[i].English == "Memory details" {
			v.Presentation.Copy[i].Chinese = "独立版本详情"
			changed = true
		}
	}
	if !changed || memoryTranslate(v, "zh-CN", "Memory details") != "独立版本详情" || translate("zh-CN", "Memory details") == "独立版本详情" {
		t.Fatal("managed copy did not evolve independently of native dictionary")
	}
	if translate("zh-CN", "Chat") == "Chat" || memoryTranslate(v, "zh-CN", "Chat") != "Chat" {
		t.Fatal("managed unknown copy consulted native feature dictionary")
	}
	if got := memoryTranslate(v, "zh-CN", "UNCLASSIFIED_ERROR"); got != "UNCLASSIFIED_ERROR" {
		t.Fatal("unknown diagnostic invented meaning", got)
	}
}

func TestMemoryClientConfigurationEscapesActiveCopyAndContainsNoInspectionFacts(t *testing.T) {
	v := originalMemoryViewFixture(t, "document-detail")
	v.Presentation.Copy[0].Chinese = `</script><img src="https://outside.invalid/private">`
	encoded, e := memoryClientConfiguration(v)
	if e != nil {
		t.Fatal(e)
	}
	raw := string(encoded)
	if strings.Count(raw, "<script ") != 1 || strings.Count(raw, "</script>") != 1 || strings.Contains(raw, "<img") || strings.Contains(raw, v.Selected.Content) || strings.Contains(raw, v.Selected.Ref) {
		t.Fatal("configuration exposed facts or active HTML", raw)
	}
	start := strings.Index(raw, ">") + 1
	end := strings.LastIndex(raw, "</script>")
	var fields map[string]json.RawMessage
	if e = json.Unmarshal([]byte(raw[start:end]), &fields); e != nil || len(fields) != 2 || fields["revision"] == nil || fields["copy"] == nil {
		t.Fatal("configuration grew private data fields", fields, e)
	}
	var got struct {
		Revision string                         `json:"revision"`
		Copy     []memory.InspectionTranslation `json:"copy"`
	}
	if e = json.Unmarshal([]byte(raw[start:end]), &got); e != nil || got.Revision != v.Presentation.ReaderRevision || len(got.Copy) != len(v.Presentation.Copy) {
		t.Fatal("frozen configuration changed source copy", got, e)
	}
	for i, entry := range got.Copy {
		if entry != v.Presentation.Copy[i] {
			t.Fatal("configuration introduced non-resource data", i)
		}
	}
}

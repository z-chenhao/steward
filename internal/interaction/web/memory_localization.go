package web

import (
	"encoding/json"
	"html/template"

	"github.com/z-chenhao/assistant/internal/memory"
)

// Managed copy has its own frozen lexicon. Unknown diagnostic/data strings keep
// their original English text; they never consult another native policy version.
func memoryTranslate(v *memoryView, language, text string) string {
	if language != "zh-CN" {
		return text
	}
	for _, entry := range v.Presentation.Copy {
		if entry.English == text {
			return entry.Chinese
		}
	}
	return text
}

func memoryClientConfiguration(v *memoryView) (template.HTML, error) {
	raw, err := json.Marshal(struct {
		Revision string                         `json:"revision"`
		Copy     []memory.InspectionTranslation `json:"copy"`
	}{v.Presentation.ReaderRevision, v.Presentation.Copy})
	if err != nil {
		return "", err
	}
	// json.Marshal escapes HTML-sensitive bytes. Only this Host-authored inert
	// script can carry configuration; candidate view DOM cannot create scripts.
	return template.HTML(`<script type="application/json" id="memory-inspector-config">` + string(raw) + `</script>`), nil
}

package web

import (
	"encoding/json"
	"html/template"
	"net/http"

	"github.com/z-chenhao/assistant/internal/plugin/webworkbench/contract"
)

// Only Host emits this inert configuration. json.Marshal escapes script-closing
// bytes; the owner template cannot create scripts or choose its graph reference.
func workbenchClientConfiguration(p page) (template.HTML, error) {
	if p.Workbench == nil && !p.WorkbenchUnavailable {
		return "", nil
	}

	value := struct {
		Revision    string           `json:"revision,omitempty"`
		Policy      *contract.Policy `json:"policy,omitempty"`
		Copy        json.RawMessage  `json:"copy,omitempty"`
		Unavailable bool             `json:"unavailable,omitempty"`
	}{Unavailable: true}
	if p.Workbench != nil {
		value.Revision = p.Workbench.ReaderRevision
		value.Policy = &p.Workbench.Presentation.Policy
		value.Copy = json.RawMessage(p.Workbench.Presentation.Copy.Source)
		value.Unavailable = false
	}

	raw, err := json.Marshal(value)
	if err != nil {
		return "", err
	}
	return template.HTML(`<script type="application/json" id="web-workbench-config">` + string(raw) + `</script>`), nil
}

func workbenchLivePath(p page, r *http.Request) string {
	if p.WorkbenchUnavailable {
		return ""
	}
	if p.Workbench == nil {
		return legacyWorkbenchLivePath(p.View, r.URL.Path)
	}
	kind := p.View
	for _, entry := range p.Workbench.Presentation.Policy.PageLive {
		if entry == kind {
			return r.URL.Path
		}
	}
	return ""
}

package web

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/z-chenhao/assistant/internal/agent"
	"github.com/z-chenhao/assistant/internal/delivery/ui"
)

func TestUIFormViewStaticPreviewCountsTowardExistingBudget(t *testing.T) {
	server := &Server{}
	preview := ui.Preview{Title: "Draft", FormManaged: true, Components: []ui.Component{{Name: "steward.form", Props: json.RawMessage(`{}`)}}, FormViews: map[int]ui.FormView{0: {Props: ui.FormProps{Title: "Original"}, Defaults: map[string]string{"notes": "original"}}}}
	emit := func(kind string) {
		server.observeUIPreview(agent.Event{RunID: "original-run", RunKind: "main", Kind: kind, PreviewID: "session", PreviewKey: "item", UIPreview: &preview})
	}
	emit("ui_preview_start")
	emit("ui_preview_update")
	revision := server.uiPreviews.frame.Revision
	preview.FormViews = map[int]ui.FormView{0: {Props: ui.FormProps{Title: "Changed"}, Defaults: map[string]string{"notes": strings.Repeat("x", uiPreviewMaxBytes+1)}}}
	emit("ui_preview_update")
	if server.uiPreviews.frame.Revision != revision || len(server.uiPreviews.frame.Items) != 1 || server.uiPreviews.frame.Items[0].FormViews[0].Defaults["notes"] != "original" {
		t.Fatal("oversize static form replaced original frame")
	}
}

package web

import (
	"encoding/json"
	"github.com/z-chenhao/assistant/internal/agent"
	"github.com/z-chenhao/assistant/internal/delivery/ui"
	"strings"
	"testing"
)

func TestUIChartPrivateWorkerCountsTowardPreviewBudget(t *testing.T) {
	server := &Server{}
	preview := ui.Preview{Title: "Draft", ChartManaged: true, Components: []ui.Component{{Name: "steward.lineChart", Props: json.RawMessage(`{}`)}}, ChartViews: map[int]ui.ChartView{0: {Name: "steward.lineChart", Worker: "original-worker", Props: json.RawMessage(`{}`)}}}
	emit := func(kind string) {
		server.observeUIPreview(agent.Event{RunID: "original-run", RunKind: "main", Kind: kind, PreviewID: "session", PreviewKey: "item", UIPreview: &preview})
	}
	emit("ui_preview_start")
	emit("ui_preview_update")
	revision := server.uiPreviews.frame.Revision
	preview.ChartViews = map[int]ui.ChartView{0: {Name: "steward.lineChart", Worker: strings.Repeat("x", uiPreviewMaxBytes+1), Props: json.RawMessage(`{}`)}}
	emit("ui_preview_update")
	if server.uiPreviews.frame.Revision != revision || len(server.uiPreviews.frame.Items) != 1 || server.uiPreviews.frame.Items[0].ChartViews[0].Worker != "original-worker" {
		t.Fatal("oversize private chart replaced original preview")
	}
}

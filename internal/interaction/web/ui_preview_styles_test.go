package web

import (
	"context"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/z-chenhao/assistant/internal/agent"
	"github.com/z-chenhao/assistant/internal/delivery/ui"
)

type previewStyleCore struct {
	*streamAccess
	styles []byte
	runID  string
}

func (c *previewStyleCore) UIPreviewStyles(_ context.Context, runID string) ([]byte, bool, error) {
	c.runID = runID
	return c.styles, true, nil
}

func TestUIPreviewDocumentUsesOriginalManagedStyleAndNeverNativeFallback(t *testing.T) {
	for _, missing := range []bool{false, true} {
		core := &previewStyleCore{streamAccess: &streamAccess{}, styles: []byte("/* ORIGINAL_BOUND_STYLE */body{font-family:Inter}")}
		if missing {
			core.styles = nil
		}
		s := &Server{Core: core}
		preview := ui.Preview{Title: "Visual", Components: []ui.Component{{Name: "generateSandboxedUi", Props: []byte(`{"initialHeight":240,"html":"<p>Visible</p>","css":"","cssComplete":true}`)}}}
		for _, kind := range []string{"ui_preview_start", "ui_preview_update"} {
			s.observeUIPreview(agent.Event{RunID: "original-run", RunKind: "main", Kind: kind, PreviewID: "session", PreviewKey: "item", UIPreview: &preview})
		}
		request := httptest.NewRequest("GET", "/ui/preview?key="+replyKey("session")+"&item="+replyKey("item")+"&component=0", nil)
		response := httptest.NewRecorder()
		s.previewWidget(response, request)
		if core.runID != "original-run" {
			t.Fatal("styles selected without originating Run", core.runID)
		}
		if missing {
			if response.Code != 503 || strings.Contains(response.Body.String(), "preview-config") {
				t.Fatal("missing managed style restored native resources", response.Code)
			}
		} else if response.Code != 200 || !strings.Contains(response.Body.String(), "ORIGINAL_BOUND_STYLE") {
			t.Fatal("original managed style not served", response.Code)
		}
	}
}

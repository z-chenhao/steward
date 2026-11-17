package web

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/z-chenhao/assistant/internal/delivery/ui"
)

type widgetCore struct{ identityCore }

func (widgetCore) WebWidgetView(context.Context, string) (ui.WidgetView, error) {
	props, _ := json.Marshal(ui.SandboxedUI{InitialHeight: 200, JSExpressions: []string{}, PlaceholderMessages: []string{}, HTML: "<p>Visual</p>", CSS: "</style><script>window.earlyRTC=window.RTCPeerConnection</script><style>"})
	return ui.WidgetView{Document: ui.Widget{Title: "Visual", Fallback: "Preserved text.", Components: []ui.Component{{Name: "generateSandboxedUi", Props: props}}}}, nil
}

func TestWidgetBootstrapPrecedesGeneratedCodeAndDirectNavigationUsesText(t *testing.T) {
	server := &Server{Core: widgetCore{}}
	for _, destination := range []string{"iframe", "document"} {
		r := httptest.NewRequest("GET", "/ui/widget?ref=ui:fixture", nil)
		r.Header.Set("Sec-Fetch-Dest", destination)
		w := httptest.NewRecorder()
		server.widget(w, r)
		if destination == "document" {
			if w.Header().Get("Content-Type") != "text/plain; charset=utf-8" || w.Body.String() != "Preserved text." {
				t.Fatal("direct navigation executed generated code", w.Header(), w.Body.String())
			}
			continue
		}
		text := w.Body.String()
		bootstrap, generated := strings.Index(text, "Object.defineProperty(window,name"), strings.Index(text, "window.earlyRTC")
		if bootstrap < 0 || generated < bootstrap || !strings.Contains(w.Header().Get("Content-Security-Policy"), "sandbox allow-scripts") {
			t.Fatal("generated CSS could execute before sandbox bootstrap", text)
		}
		if !strings.Contains(text, "SVGRenderer") || !strings.Contains(w.Header().Get("Content-Security-Policy"), "connect-src 'none'") || !strings.Contains(w.Header().Get("Content-Security-Policy"), "script-src 'unsafe-inline';") {
			t.Fatal("local visualization runtime missing or network boundary widened")
		}
	}
}

// This source has crossed the managed boundary; missing package bytes must not
// load the embedded native runtime that the historical fixture above uses.
type missingManagedWidgetCore struct{ widgetCore }

func (c missingManagedWidgetCore) WebWidgetView(ctx context.Context, ref string) (ui.WidgetView, error) {
	v, e := c.widgetCore.WebWidgetView(ctx, ref)
	v.Managed = true
	return v, e
}
func TestWidgetManagedMissingResourcesNeverUsesNative(t *testing.T) {
	s := &Server{Core: missingManagedWidgetCore{}}
	r := httptest.NewRequest("GET", "/ui/widget?ref=ui:fixture#0", nil)
	r.Header.Set("Sec-Fetch-Dest", "iframe")
	w := httptest.NewRecorder()
	s.widget(w, r)
	if w.Code != 503 || strings.Contains(w.Body.String(), "Websandbox") {
		t.Fatal("managed source recovered native runtime", w.Code)
	}
}

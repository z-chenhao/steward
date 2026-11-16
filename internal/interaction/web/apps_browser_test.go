package web

import (
	"context"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/z-chenhao/assistant/internal/plugin/appsinterface/contract"
)

func TestAppsBrowserSourceHasOpaqueCSPAndEscapesProgram(t *testing.T) {
	r := httptest.NewRequest("GET", "/api/apps/browser?interface_revision=original", nil)
	w := httptest.NewRecorder()
	renderAppBrowser(w, r, `onmessage=()=>{const attack="</script><script>location='/apps/configure'</script>"}`)
	if w.Code != 200 {
		t.Fatal(w.Code)
	}
	csp := w.Header().Get("Content-Security-Policy")
	for _, part := range []string{"sandbox allow-scripts", "default-src 'none'", "worker-src blob:", "connect-src 'none'", "form-action 'none'", "frame-ancestors 'self'"} {
		if !strings.Contains(csp, part) {
			t.Fatal(csp)
		}
	}
	if strings.Contains(csp, "allow-same-origin") || strings.Contains(csp, "allow-popups") || strings.Contains(csp, "unsafe-inline") {
		t.Fatal(csp)
	}
	body := w.Body.String()
	if strings.Count(body, "<script ") != 1 || strings.Count(body, "</script>") != 1 || !strings.Contains(body, `event.source!==parent`) || !strings.Contains(body, `new Worker(url)`) {
		t.Fatal(body)
	}
	r.Header.Set("Sec-Fetch-Dest", "document")
	w = httptest.NewRecorder()
	renderAppBrowser(w, r, "candidate")
	if !strings.HasPrefix(w.Header().Get("Content-Type"), "text/plain") || strings.Contains(w.Body.String(), "candidate") {
		t.Fatal(w.Body.String())
	}
}

func TestAppsBrowserRequiresExactSelectedReferenceAndCannotUseNative(t *testing.T) {
	var selected string
	s := &Server{Interface: func(_ context.Context, reference, locale string) (AppInterfacePolicy, error) {
		selected = reference
		return AppInterfacePolicy{revision: reference, browserView: &contract.BrowserView{Worker: "onmessage=()=>{}", CSS: ".app-tile{}"}}, nil
	}}
	for _, path := range []string{"/api/apps/browser", "/api/apps/browser?interface_revision=", "/api/apps/browser?interface_revision=one&interface_revision=two", "/api/apps/browser?interface_revision=one&action=disconnect", "/api/apps/browser?interface_revision=one&format=css&format=css", "/api/apps/browser?interface_revision=one&format=unknown", "/api/apps/browser?interface_revision=one&format=css&extra=1"} {
		w := httptest.NewRecorder()
		s.appBrowser(w, httptest.NewRequest("GET", path, nil))
		if w.Code != 400 {
			t.Fatalf("%s: %d", path, w.Code)
		}
	}
	w := httptest.NewRecorder()
	s.appBrowser(w, httptest.NewRequest("GET", "/api/apps/browser?interface_revision=frozen", nil))
	if w.Code != 200 || selected != "frozen" {
		t.Fatal(w.Code, selected)
	}
	w = httptest.NewRecorder()
	s.appBrowser(w, httptest.NewRequest("GET", "/api/apps/browser?interface_revision=frozen&format=css", nil))
	if w.Code != 200 || !strings.HasPrefix(w.Header().Get("Content-Type"), "text/css") || w.Body.String() != ".app-tile{}" {
		t.Fatal("style route changed frozen source", w.Code, w.Body.String())
	}
	s.Interface = nil
	w = httptest.NewRecorder()
	s.appBrowser(w, httptest.NewRequest("GET", "/api/apps/browser?interface_revision=frozen", nil))
	if w.Code != 503 {
		t.Fatal(w.Code)
	}
}

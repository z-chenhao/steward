package web

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/z-chenhao/assistant/internal/agent"
	"github.com/z-chenhao/assistant/internal/delivery/ui"
)

func TestUIPreviewLifecycleAndIsolation(t *testing.T) {
	s := &Server{}
	emit := func(session, kind, key string, p *ui.Preview) {
		s.ObserveReply(agent.Event{RunKind: "main", PreviewID: session, Kind: kind, PreviewKey: key, UIPreview: p, GenerationID: "final-generation"})
	}
	preview, _ := ui.ParsePreview(`{"title":"Answer","components":[{"name":"Table","props":{"columns":["Name"],"rows":[["A"]]}}]}`)
	emit("session", "ui_preview_start", "", nil)
	emit("session", "ui_preview_update", "private-call-id", &preview)
	var cursor uint64
	f, _ := s.uiPreviews.read(&cursor)
	if f.State != "draft" || len(f.Items) != 1 || f.Items[0].Key == "private-call-id" {
		t.Fatal(f)
	}
	data, _ := json.Marshal(f)
	if strings.Contains(string(data), "private-call-id") || strings.Contains(string(data), "private-run") {
		t.Fatal(string(data))
	}
	s.ObserveReply(agent.Event{RunKind: "worker", Kind: "ui_preview_start", PreviewID: "worker"})
	emit("stale", "ui_preview_discarded", "", nil)
	if f, _ = s.uiPreviews.read(&cursor); f.State != "" {
		t.Fatal("another scope changed preview", f)
	}
	emit("session", "ui_preview_committed", "", nil)
	f, _ = s.uiPreviews.read(&cursor)
	if f.State != "committed" || f.ReplyKey != replyKey("final-generation") || len(f.Items) != 0 {
		t.Fatal(f)
	}
	emit("session", "ui_preview_update", "late", &preview)
	if f, _ = s.uiPreviews.read(&cursor); f.State != "" {
		t.Fatal("late preview resurrected", f)
	}
	emit("new", "ui_preview_start", "", nil)
	emit("new", "ui_preview_update", "new-call", &preview)
	var reconnect uint64
	if f, _ = s.uiPreviews.read(&reconnect); len(f.Items) != 1 || f.Items[0].Title != "Answer" {
		t.Fatal("reconnect lost snapshot", f)
	}
	emit("new", "ui_preview_remove", "new-call", nil)
	if f, _ = s.uiPreviews.read(&reconnect); len(f.Items) != 0 {
		t.Fatal(f)
	}
	emit("new", "ui_preview_discarded", "", nil)
	if f, _ = s.uiPreviews.read(&reconnect); f.State != "discarded" {
		t.Fatal(f)
	}
}

func TestUIPreviewDocumentRunsOnlyTrustedUpdaterAndIsEphemeral(t *testing.T) {
	access := &streamAccess{}
	s := &Server{Core: access, Config: Config{TokenRef: "credential:test"}, Secrets: fakeSecrets{}}
	preview, _ := ui.ParsePreview(`{"title":"Visual","components":[{"name":"generateSandboxedUi","props":{"initialHeight":240,"html":"<p>Visual</p><svg viewBox=\"0 0 20 20\"><text>Diagram</text></svg><script>SECRET_SCRIPT</script><noscript><meta http-equiv=refresh content=\"0;url=https://outside.invalid\"></noscript><input autofocus onfocus=SECRET_HANDLER()><a href=\"https://outside.invalid\">Leave</a>","css":"</style><script>SECRET_CSS</script>"}}]}`)
	for _, kind := range []string{"ui_preview_start", "ui_preview_update"} {
		s.ObserveReply(agent.Event{RunKind: "main", Kind: kind, PreviewID: "session", PreviewKey: "item", UIPreview: &preview})
	}
	var cursor uint64
	frame, _ := s.uiPreviews.read(&cursor)
	wire, _ := json.Marshal(frame)
	if strings.Contains(string(wire), "SECRET") || strings.Contains(string(wire), `"html":`) || strings.Contains(string(wire), `"css":`) {
		t.Fatal("SSE leaked raw sandbox source", string(wire))
	}
	host := httptest.NewServer(s)
	defer host.Close()
	path := "/ui/preview?key=" + frame.Key + "&item=" + frame.Items[0].Key + "&component=0&theme=dark"
	request := func(auth, direct bool) (int, string, http.Header) {
		req, _ := http.NewRequest("GET", host.URL+path, nil)
		if auth {
			req.Header.Set("Authorization", "Bearer private-token-at-least-24-bytes")
		}
		if direct {
			req.Header.Set("Sec-Fetch-Dest", "document")
		}
		resp, err := host.Client().Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		body, _ := io.ReadAll(resp.Body)
		return resp.StatusCode, string(body), resp.Header
	}
	if code, _, _ := request(false, false); code != 401 {
		t.Fatal("unauthorized preview read", code)
	}
	code, body, headers := request(true, false)
	if code != 200 || !strings.Contains(body, `data-widget-theme="dark"`) || !strings.Contains(body, "Diagram") || !strings.Contains(body, "Inter") || strings.Contains(body, "SECRET_SCRIPT") || strings.Contains(body, "SECRET_HANDLER") || strings.Contains(body, "<script>") || strings.Contains(body, "outside.invalid") {
		t.Fatal("unsafe or unthemed preview", code)
	}
	if !strings.Contains(headers.Get("Content-Security-Policy"), "script-src 'nonce-") || strings.Contains(headers.Get("Content-Security-Policy"), "allow-same-origin") || headers.Get("Cache-Control") != "no-store" || !strings.Contains(body, `id="preview-content"`) {
		t.Fatal(headers)
	}
	if code, body, headers := request(true, true); code != 200 || body != "Visual" || !strings.HasPrefix(headers.Get("Content-Type"), "text/plain") {
		t.Fatal("direct preview navigation executed markup", code, body)
	}
	access.revoked.Store(true)
	if code, _, _ := request(true, false); code != 403 {
		t.Fatal("revoked preview remained readable", code)
	}
	access.revoked.Store(false)
	s.ObserveReply(agent.Event{RunKind: "main", Kind: "ui_preview_discarded", PreviewID: "session"})
	if code, _, _ := request(true, false); code != 404 {
		t.Fatal("ended preview remained readable", code)
	}
}

func TestUIPreviewMarkupPreservesLocalSVGAndDisabledFormContents(t *testing.T) {
	markup := previewMarkup(`<form action="https://outside.invalid"><label>Value<input oninput="unsafe()"></label></form><svg><defs><symbol id="node"><rect width="10" height="10"/></symbol></defs><use href="#node"/><use href="https://outside.invalid/image"/></svg>`)
	if !strings.Contains(markup, `href="#node"`) || !strings.Contains(markup, "Value") || !strings.Contains(markup, "disabled") || strings.Contains(markup, "<form") || strings.Contains(markup, "unsafe") || strings.Contains(markup, "outside.invalid") {
		t.Fatal(markup)
	}
}

func TestUIPreviewRestoredAndNewSectionsKeepPrivateAcceptedOrder(t *testing.T) {
	s := &Server{}
	s.observeUIPreview(agent.Event{RunKind: "main", PreviewID: "session", Kind: "ui_preview_start"})
	for _, value := range []struct{ key, order string }{{"restored", "private-order-001"}, {"third", "private-order-003"}, {"second", "private-order-002"}} {
		s.observeUIPreview(agent.Event{RunKind: "main", PreviewID: "session", Kind: "ui_preview_update", PreviewKey: value.key, UIPreview: &ui.Preview{Title: value.key, Order: value.order}})
	}
	var revision uint64
	frame, _ := s.uiPreviews.read(&revision)
	if len(frame.Items) != 3 || frame.Items[0].Title != "restored" || frame.Items[1].Title != "second" || frame.Items[2].Title != "third" {
		t.Fatal("completion timing reordered the recovered explanation", frame)
	}
	raw, _ := json.Marshal(frame)
	if strings.Contains(string(raw), "Order") || strings.Contains(string(raw), "private-order-") {
		t.Fatal("private order metadata reached the browser", string(raw))
	}
}

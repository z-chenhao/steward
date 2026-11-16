package web

import (
	"encoding/json"
	"github.com/z-chenhao/assistant/internal/agent"
	"github.com/z-chenhao/assistant/internal/delivery/ui"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
)

func TestUICardsPreviewRemainsPrivateAndExpiresDuringResourceRead(t *testing.T) {
	core := &planPreviewCore{previewStyleCore: &previewStyleCore{streamAccess: &streamAccess{}, styles: []byte("/* ORIGINAL_STYLE */body{}")}}
	s := &Server{Core: core, Config: Config{TokenRef: "credential:test"}, Secrets: fakeSecrets{}}
	p := ui.Preview{Title: "Draft", CardsManaged: true, Components: []ui.Component{{Name: "steward.cards", Props: json.RawMessage(`{}`)}}, CardsViews: map[int]ui.CardsView{0: {HTML: `<section class="native-carousel"><article>PRIVATE_CARDS</article><a href="https://example.org">Details</a><script>PRIVATE_RUNTIME()</script></section>`, CSS: "summary{}", CompactMaxWidth: 760}}}
	emit := func(kind string) {
		s.observeUIPreview(agent.Event{RunID: "original-run", RunKind: "main", Kind: kind, PreviewID: "session", PreviewKey: "item", UIPreview: &p})
	}
	emit("ui_preview_start")
	emit("ui_preview_update")
	var cursor uint64
	frame, _ := s.uiPreviews.read(&cursor)
	wire, _ := json.Marshal(frame)
	if strings.Contains(string(wire), "PRIVATE_CARDS") || !strings.Contains(string(wire), `"cards_view":true`) {
		t.Fatal("private recipe broadcast", string(wire))
	}
	path := "/ui/cards-preview?key=" + frame.Key + "&item=" + frame.Items[0].Key + "&component=0&revision=" + strconv.FormatUint(frame.Revision, 10)
	read := func() *httptest.ResponseRecorder {
		out := httptest.NewRecorder()
		request := httptest.NewRequest("GET", "http://127.0.0.1:8080"+path, nil)
		request.Header.Set("Authorization", "Bearer private-token-at-least-24-bytes")
		s.ServeHTTP(out, request)
		return out
	}
	unauth := httptest.NewRecorder()
	s.ServeHTTP(unauth, httptest.NewRequest("GET", "http://127.0.0.1:8080"+path, nil))
	if unauth.Code != 401 {
		t.Fatal("preview route unauthenticated", unauth.Code)
	}
	out := read()
	if strings.Contains(out.Body.String(), "PRIVATE_RUNTIME") || strings.Contains(out.Body.String(), "https://example.org") {
		t.Fatal("draft contains executable content or link", out.Body.String())
	}
	if out.Code != 200 || !strings.Contains(out.Body.String(), "<body inert>") || !strings.Contains(out.Body.String(), "ORIGINAL_STYLE") || core.runID != "original-run" {
		t.Fatal("original inert preview not served", out.Code, out.Body.String())
	}
	if out.Header().Get("Cache-Control") != "no-store" {
		t.Fatal("preview cacheable", out.Header())
	}
	core.revoked.Store(true)
	if out = read(); out.Code != 403 {
		t.Fatal("preview bypassed channel revocation", out.Code)
	}
	core.revoked.Store(false)
	originalPath := path
	path += "0"
	if out = read(); out.Code != 404 {
		t.Fatal("stale revision exposed recipe", out.Code)
	}
	path = originalPath
	core.styles = nil
	if out = read(); out.Code != 503 {
		t.Fatal("missing resource used native fallback", out.Code)
	}
	core.styles = []byte("body{}")
	core.afterRead = func() { emit("ui_preview_discarded") }
	if out = read(); out.Code != 404 {
		t.Fatal("ended preview escaped I/O recheck", out.Code)
	}
	if out = read(); out.Code != 404 {
		t.Fatal("GET recreated preview", out.Code)
	}
}

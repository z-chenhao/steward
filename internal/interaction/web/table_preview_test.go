package web

import (
	"encoding/base64"
	"encoding/json"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/z-chenhao/assistant/internal/agent"
	"github.com/z-chenhao/assistant/internal/delivery/ui"
)

func TestUITablePreviewRemainsPrivateReadOnlyAndExpiresDuringResourceRead(t *testing.T) {
	core := &planPreviewCore{previewStyleCore: &previewStyleCore{streamAccess: &streamAccess{}, styles: []byte("/* ORIGINAL_STYLE */body{}")}}
	server := &Server{Core: core, Config: Config{TokenRef: "credential:test"}, Secrets: fakeSecrets{}}
	preview := ui.Preview{Title: "Draft", TableManaged: true, Components: []ui.Component{{Name: "Table", Props: json.RawMessage(`{}`)}}, TableViews: map[int]ui.TableView{0: {HTML: `<section class="answer-table">PRIVATE_TABLE<script>DOM_EVIL()</script></section>`, CSS: "table{}", Worker: "/* PRIVATE_WORKER */", CompactMaxWidth: 760}}}
	emit := func(kind string) {
		server.observeUIPreview(agent.Event{RunID: "original-run", RunKind: "main", Kind: kind, PreviewID: "session", PreviewKey: "item", UIPreview: &preview})
	}
	emit("ui_preview_start")
	emit("ui_preview_update")
	var cursor uint64
	frame, _ := server.uiPreviews.read(&cursor)
	wire, _ := json.Marshal(frame)
	if strings.Contains(string(wire), "PRIVATE_TABLE") || strings.Contains(string(wire), "PRIVATE_WORKER") || !strings.Contains(string(wire), `"table_view":true`) {
		t.Fatal("private producer broadcast", string(wire))
	}
	path := "/ui/table-preview?key=" + frame.Key + "&item=" + frame.Items[0].Key + "&component=0&revision=" + strconv.FormatUint(frame.Revision, 10)
	read := func(token bool) *httptest.ResponseRecorder {
		out := httptest.NewRecorder()
		request := httptest.NewRequest("GET", "http://127.0.0.1:8080"+path, nil)
		if token {
			request.Header.Set("Authorization", "Bearer private-token-at-least-24-bytes")
		}
		server.ServeHTTP(out, request)
		return out
	}
	if response := read(false); response.Code != 401 {
		t.Fatal(response.Code)
	}
	response := read(true)
	if response.Code != 200 || !strings.Contains(response.Body.String(), "<body inert>") || !strings.Contains(response.Body.String(), "PRIVATE_TABLE") || !strings.Contains(response.Body.String(), "ORIGINAL_STYLE") || strings.Contains(response.Body.String(), "DOM_EVIL") || !strings.Contains(response.Body.String(), ",true);") || core.runID != "original-run" {
		t.Fatal(response.Code, response.Body.String())
	}
	if response.Header().Get("Cache-Control") != "no-store" {
		t.Fatal(response.Header())
	}
	original := path
	for _, suffix := range []string{"&ref=forged", "&component=1", "&revision=1", "&format=export", "&key=other"} {
		path = original + suffix
		if response = read(true); response.Code != 400 {
			t.Fatal("ambiguous preview query", path, response.Code)
		}
	}
	path = strings.Replace(original, "component=0", "component=00", 1)
	if response = read(true); response.Code != 404 {
		t.Fatal(response.Code)
	}
	path = original
	core.revoked.Store(true)
	if response = read(true); response.Code != 403 {
		t.Fatal(response.Code)
	}
	core.revoked.Store(false)
	core.styles = nil
	if response = read(true); response.Code != 503 {
		t.Fatal("missing styles used fallback", response.Code)
	}
	core.styles = []byte("body{}")
	core.afterRead = func() { emit("ui_preview_discarded") }
	if response = read(true); response.Code != 404 {
		t.Fatal("ended during resource read", response.Code)
	}
	if response = read(true); response.Code != 404 {
		t.Fatal("GET resurrected preview", response.Code)
	}
}

func TestUITablePreviewPrivateBytesCountTowardFrameBudget(t *testing.T) {
	server := &Server{}
	preview := ui.Preview{Title: "Draft", TableManaged: true, TableViews: map[int]ui.TableView{0: {HTML: "original", CSS: "table{}", Worker: "original-worker"}}}
	emit := func(kind string) {
		server.observeUIPreview(agent.Event{RunID: "run", RunKind: "main", Kind: kind, PreviewID: "session", PreviewKey: "item", UIPreview: &preview})
	}
	emit("ui_preview_start")
	emit("ui_preview_update")
	revision := server.uiPreviews.frame.Revision
	preview.TableViews = map[int]ui.TableView{0: {HTML: "replacement", CSS: "table{}", Worker: strings.Repeat("x", uiPreviewMaxBytes+1)}}
	emit("ui_preview_update")
	if server.uiPreviews.frame.Revision != revision || server.uiPreviews.frame.Items[0].TableViews[0].HTML != "original" {
		t.Fatal("private Worker bypassed preview frame budget")
	}
}

// Thirty existing-valid 2000-character cells fit the tool/native catalog but
// their HTML entities and original-text transport expand private view bytes.
// This is a frame-budget regression, not an owning-renderer dispatch oracle.
func TestUITablePreviewRetainsValidLiteralCellExpansion(t *testing.T) {
	rows := make([][]string, 30)
	for i := range rows {
		rows[i] = []string{strings.Repeat("'", 2000)}
	}
	props, _ := json.Marshal(map[string]any{"title": "Literal cells", "columns": []string{"N"}, "rows": rows, "source": "Original valid data"})
	markup := `<section data-table-facts="` + base64.StdEncoding.EncodeToString(props) + `"><table><tbody>` + strings.Repeat(`<tr><td>`+strings.Repeat("&#39;", 2000)+`</td></tr>`, 30) + `</tbody></table></section>`
	if len(props) > 65536 || len(markup) > 512<<10 {
		t.Fatal("not an existing valid input/view budget", len(props), len(markup))
	}
	preview := ui.Preview{Title: "Draft", TableManaged: true, Components: []ui.Component{{Name: "Table", Props: props}}, TableViews: map[int]ui.TableView{0: {HTML: markup, CSS: "table{}", Worker: "/* opaque owning Worker */", CompactMaxWidth: 760}}}
	server := &Server{}
	server.observeUIPreview(agent.Event{RunID: "run", RunKind: "main", Kind: "ui_preview_start", PreviewID: "session"})
	server.observeUIPreview(agent.Event{RunID: "run", RunKind: "main", Kind: "ui_preview_update", PreviewID: "session", PreviewKey: "item", UIPreview: &preview})
	if len(server.uiPreviews.frame.Items) != 1 {
		t.Fatal("valid original literal cells disappeared when private HTML was budgeted")
	}
}

package web

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/z-chenhao/assistant/internal/agent"
	"github.com/z-chenhao/assistant/internal/delivery/ui"
)

func TestUIFormViewPreviewBrowserReadOnlyAndRevision(t *testing.T) {
	if os.Getenv("STEWARD_TEST_APP_BROWSER") != "1" {
		t.Skip("run by make test-browser")
	}
	props := json.RawMessage(`{"title":"Original form","fields":[{"name":"notes","label":"Notes","type":"textarea"},{"name":"confirm","label":"Confirm","type":"boolean"}]}`)
	var fields ui.FormProps
	if err := json.Unmarshal(props, &fields); err != nil {
		t.Fatal(err)
	}
	core := formViewCore{}
	server := &Server{Core: core, Config: Config{TokenRef: "credential:test"}, Secrets: fakeSecrets{}}
	preview := ui.Preview{Title: "Draft form", FormManaged: true, Components: []ui.Component{{Name: "steward.form", Props: props}}, FormViews: map[int]ui.FormView{0: {Props: fields, Defaults: map[string]string{"notes": "Original readonly <script>window.formPreviewProbe=1</script>", "confirm": "false"}}}}

	emit := func(kind string) {
		server.observeUIPreview(agent.Event{RunID: "original-run", RunKind: "main", Kind: kind, PreviewID: "session", PreviewKey: "item", UIPreview: &preview})
	}
	emit("ui_preview_start")
	emit("ui_preview_update")
	webRoot, err := filepath.Abs("../../../web")
	if err != nil {
		t.Fatal(err)
	}
	build := `import{build}from'../../../web/node_modules/esbuild/lib/main.js';const r=await build({stdin:{contents:"import{createUIPreview}from'./src/intelligent-ui/preview';window.preview=createUIPreview(document.getElementById('root'));preview.receive(JSON.parse(document.getElementById('fixture').textContent));",resolveDir:process.argv[1],sourcefile:'form-preview.tsx',loader:'tsx'},bundle:true,jsx:'automatic',write:false,format:'iife',define:{'process.env.NODE_ENV':'"production"'}});process.stdout.write(r.outputFiles[0].contents)`
	parent, err := exec.Command("node", "--input-type=module", "-e", build, webRoot).Output()
	if err != nil {
		t.Fatal(err)
	}
	current := func() uiPreviewFrame { var cursor uint64; f, _ := server.uiPreviews.read(&cursor); return f }
	host := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/fixture.js":
			w.Header().Set("Content-Type", "text/javascript")
			_, _ = w.Write(parent)
			return
		case "/fixture":
			raw, _ := json.Marshal(current())
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			_, _ = w.Write([]byte(`<!doctype html><link rel="stylesheet" href="/style.css"><style>#root{max-width:672px;margin:24px}</style><div id="root"></div><script type="application/json" id="fixture">` + string(raw) + `</script><script src="/fixture.js"></script>`))
			return
		case "/fixture-update":
			next := preview.FormViews[0]
			next.Props.Title = "Updated form"
			next.Defaults = map[string]string{"notes": "Updated readonly", "confirm": "true"}
			preview.FormViews = map[int]ui.FormView{0: next}
			emit("ui_preview_update")
			jsonResult(w, current())
			return
		case "/fixture-missing":
			preview.FormViews = map[int]ui.FormView{}
			emit("ui_preview_update")
			jsonResult(w, current())
			return
		case "/fixture-discard":
			emit("ui_preview_discarded")
			jsonResult(w, current())
			return
		}
		server.ServeHTTP(w, r)
	}))
	defer host.Close()
	evidence := t.TempDir()
	if dir := os.Getenv("STEWARD_APP_BROWSER_EVIDENCE"); dir != "" {
		evidence = filepath.Join(dir, "form-preview")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	if out, err := exec.CommandContext(ctx, "node", "../../../guest-browser/form-preview-browser.mjs", host.URL, evidence).CombinedOutput(); err != nil {
		t.Fatalf("form preview browser: %v\n%s", err, out)
	}
}

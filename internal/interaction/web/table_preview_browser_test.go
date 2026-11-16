package web

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/z-chenhao/assistant/internal/agent"
	"github.com/z-chenhao/assistant/internal/delivery/ui"
)

// Actual createUIPreview/React and protected HTTP; accepted domain views are
// fixtures. This does not replace the full App streaming/lifecycle gate.
func TestUITablePreviewBrowserReadOnlyAndRevision(t *testing.T) {
	if os.Getenv("STEWARD_TEST_APP_BROWSER") != "1" {
		t.Skip("run by make test-browser")
	}
	read := func(path string) []byte {
		t.Helper()
		raw, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		return raw
	}
	var original struct {
		HTML   string                        `json:"initial_html"`
		Props  json.RawMessage               `json:"props"`
		Stages []struct{ Name, HTML string } `json:"stages"`
	}
	if err := json.Unmarshal(read("../../plugin/intelligentui/testdata/original-table.json"), &original); err != nil {
		t.Fatal(err)
	}

	original.HTML = strings.Replace(original.HTML, "<section ", `<section data-table-facts="`+base64.StdEncoding.EncodeToString(original.Props)+`" `, 1)
	icon := ""
	for _, stage := range original.Stages {
		if stage.Name == "amount-ascending" {
			icon = regexp.MustCompile(`<svg[^>]*>[\s\S]*?</svg>`).FindString(stage.HTML)
		}
	}
	if icon == "" {
		t.Fatal("original sort icon missing")
	}
	end := strings.LastIndex(original.HTML, "</section>")
	original.HTML = original.HTML[:end] + `<template data-table-sort-icon>` + icon + `</template>` + original.HTML[end:]
	core := &planPreviewCore{previewStyleCore: &previewStyleCore{streamAccess: &streamAccess{}, styles: read("assets/widget.css")}}
	s := &Server{Core: core, Config: Config{TokenRef: "credential:test"}, Secrets: fakeSecrets{}}
	preview := ui.Preview{Title: "Draft tables", TableManaged: true, Components: []ui.Component{{Name: "Table", Props: original.Props}}, TableViews: map[int]ui.TableView{0: {HTML: original.HTML, CSS: string(read("../../plugin/intelligentui/view/table.css")), Worker: string(read("../../plugin/intelligentui/view/table-worker.js")), CompactMaxWidth: 760}}}
	emit := func(kind string) {
		s.observeUIPreview(agent.Event{RunID: "original-run", RunKind: "main", Kind: kind, PreviewID: "session", PreviewKey: "item", UIPreview: &preview})
	}
	emit("ui_preview_start")
	emit("ui_preview_update")
	webRoot, err := filepath.Abs("../../../web")
	if err != nil {
		t.Fatal(err)
	}
	build := `import{build}from'../../../web/node_modules/esbuild/lib/main.js';const result=await build({stdin:{contents:"import{createUIPreview}from'./src/intelligent-ui/preview';window.preview=createUIPreview(document.getElementById('root'));window.preview.receive(JSON.parse(document.getElementById('fixture').textContent));",resolveDir:process.argv[1],sourcefile:'table-preview.tsx',loader:'tsx'},bundle:true,jsx:'automatic',write:false,format:'iife',define:{'process.env.NODE_ENV':'\"production\"'}});process.stdout.write(result.outputFiles[0].contents);`
	parent, err := exec.Command("node", "--input-type=module", "-e", build, webRoot).Output()
	if err != nil {
		t.Fatal(err)
	}
	current := func() uiPreviewFrame { var cursor uint64; f, _ := s.uiPreviews.read(&cursor); return f }
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/fixture.js":
			w.Header().Set("Content-Type", "text/javascript")
			_, _ = w.Write(parent)
			return
		case "/fixture":
			raw, _ := json.Marshal(current())
			w.Header().Set("Content-Type", "text/html")
			_, _ = w.Write([]byte(`<!doctype html><meta charset="utf-8"><link rel="stylesheet" href="/style.css"><style>#root{max-width:672px;margin:24px}</style><div id="root"></div><script type="application/json" id="fixture">` + string(raw) + `</script><script src="/fixture.js"></script>`))
			return
		// Test-only event transport, not a product mutation API.
		case "/fixture-update":
			emit("ui_preview_update")
			jsonResult(w, current())
			return
		case "/fixture-discard":
			emit("ui_preview_discarded")
			jsonResult(w, current())
			return
		}
		s.ServeHTTP(w, r)
	}))
	defer server.Close()
	evidence := t.TempDir()
	if base := os.Getenv("STEWARD_APP_BROWSER_EVIDENCE"); base != "" {
		evidence = filepath.Join(base, "table-preview")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	if output, err := exec.CommandContext(ctx, "node", "../../../guest-browser/table-preview.mjs", server.URL, evidence).CombinedOutput(); err != nil {
		t.Fatalf("Table preview browser: %v\n%s", err, output)
	}
}

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

// Accepted facts and CAS storage are fixtures; transport, projection, React,
// fixed DOM driver and owning Worker are real. Full App lifecycle is separate.
type tableControllerCore struct {
	cardsControllerCore
	view    ui.TableView
	facts   ui.TableExport
	exports [][]int
}

func (c *tableControllerCore) WebTableView(_ context.Context, ref string) (ui.TableView, error) {
	if ref != "ui:original#0" {
		return ui.TableView{}, agent.ErrNotFound
	}
	return c.view, nil
}
func (c *tableControllerCore) WebTableExport(_ context.Context, ref string, rows []int) (ui.TableExport, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if ref != "ui:original#0" {
		c.forbidden++
		return ui.TableExport{}, agent.ErrNotFound
	}
	result := ui.TableExport{Title: c.facts.Title, Columns: c.facts.Columns, Rows: [][]string{}}
	for _, row := range rows {
		if row < 0 || row >= len(c.facts.Rows) {
			c.forbidden++
			return ui.TableExport{}, agent.ErrNotFound
		}
		result.Rows = append(result.Rows, c.facts.Rows[row])
	}
	c.exports = append(c.exports, append([]int{}, rows...))
	return result, nil
}
func TestUITableControllerBrowserProjectionCASGroupAndExport(t *testing.T) {
	if os.Getenv("STEWARD_TEST_APP_BROWSER") != "1" {
		t.Skip("run by make test-browser")
	}
	read := func(path string) []byte {
		t.Helper()
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		return data
	}
	var original struct {
		Props  json.RawMessage               `json:"props"`
		Stages []struct{ Name, HTML string } `json:"stages"`
	}
	if err := json.Unmarshal(read("../../plugin/intelligentui/testdata/original-table.json"), &original); err != nil {
		t.Fatal(err)
	}
	var facts ui.TableExport
	if err := json.Unmarshal(original.Props, &facts); err != nil {
		t.Fatal(err)
	}
	initial := original.Stages[0].HTML
	icon := ""
	for _, stage := range original.Stages {
		if stage.Name == "amount-ascending" {
			icon = regexp.MustCompile(`<svg[^>]*>[\s\S]*?</svg>`).FindString(stage.HTML)
		}
	}
	if icon == "" {
		t.Fatal("missing independent original icon")
	}
	initial = strings.Replace(initial, "<section ", `<section data-table-facts="`+base64.StdEncoding.EncodeToString(original.Props)+`" `, 1)
	end := strings.LastIndex(initial, "</section>")
	initial = initial[:end] + `<template data-table-sort-icon>` + icon + `</template>` + initial[end:]
	contexts := []ui.TableSortContext{}
	for column := -1; column < len(facts.Columns); column++ {
		for _, direction := range []int{1, -1} {
			var label *string
			if column >= 0 {
				value := "Frozen " + facts.Columns[column]
				label = &value
			}
			contexts = append(contexts, ui.TableSortContext{Sort: column, Direction: direction, Description: ui.TableDescription{Title: "Frozen Table title", Sort: label, Direction: direction}})
		}
	}
	core := &tableControllerCore{cardsControllerCore: cardsControllerCore{states: map[string]ui.ViewState{}}, facts: facts, view: ui.TableView{HTML: initial, CSS: string(read("../../plugin/intelligentui/view/table.css")), Worker: string(read("../../plugin/intelligentui/view/table-worker.js")), Styles: read("assets/widget.css"), CompactMaxWidth: 760, Contexts: contexts}}
	webRoot, err := filepath.Abs("../../../web")
	if err != nil {
		t.Fatal(err)
	}
	build := `import{build}from'../../../web/node_modules/esbuild/lib/main.js';const result=await build({stdin:{contents:"import{enhanceProjections}from'./src/projections';import{flushUIStates}from'./src/intelligent-ui/state';document.getElementById('projection').dataset.uiSpec=document.getElementById('fixture').textContent;document.getElementById('projection').dataset.uiFallback='Original fallback';enhanceProjections(document);window.flushTable=flushUIStates;",resolveDir:process.argv[1],sourcefile:'table-controller.tsx',loader:'tsx'},bundle:true,jsx:'automatic',write:false,format:'iife',define:{'process.env.NODE_ENV':'\"production\"'}});process.stdout.write(result.outputFiles[0].contents);`
	script, err := exec.Command("node", "--input-type=module", "-e", build, webRoot).Output()
	if err != nil {
		t.Fatal(err)
	}
	spec := `{"schema_version":1,"fallback":"Original fallback","components":[{"type":"openintelligentui","name":"Table","props":` + string(original.Props) + `,"widget_ref":"ui:original#0","view_ref":"ui:original#0","text":"Original Table fallback"},{"type":"openintelligentui","name":"Table","props":` + string(original.Props) + `,"widget_ref":"ui:original#1"}]}`
	var value any
	if err = json.Unmarshal([]byte(spec), &value); err != nil {
		t.Fatal(err)
	}
	specJSON, _ := json.Marshal(value)
	server := &Server{Core: core, Config: Config{TokenRef: "credential:test"}, Secrets: fakeSecrets{}}
	host := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/fixture.js":
			w.Header().Set("Content-Type", "text/javascript")
			_, _ = w.Write(script)
			return
		case "/fixture":
			w.Header().Set("Content-Type", "text/html")
			_, _ = w.Write([]byte(`<!doctype html><html lang="en"><meta charset="utf-8"><link rel="stylesheet" href="/style.css"><style>#projection{max-width:672px;margin:24px}</style><div id="projection"></div><script type="application/json" id="fixture">` + string(specJSON) + `</script><script src="/fixture.js"></script></html>`))
			return
		}
		server.ServeHTTP(w, r)
	}))
	defer host.Close()
	evidence := t.TempDir()
	if path := os.Getenv("STEWARD_APP_BROWSER_EVIDENCE"); path != "" {
		evidence = filepath.Join(path, "table-controller")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 75*time.Second)
	defer cancel()
	if out, err := exec.CommandContext(ctx, "node", "../../../guest-browser/table-controller.mjs", host.URL, evidence).CombinedOutput(); err != nil {
		t.Fatalf("Table controller: %v\n%s", err, out)
	}
	core.mu.Lock()
	defer core.mu.Unlock()
	if core.forbidden != 0 || len(core.writes) == 0 || len(core.exports) != 1 {
		t.Fatal("authority/persistence/export mismatch", core.forbidden, core.writes, core.exports)
	}
	if len(core.exports[0]) != 2 || core.exports[0][0] != 0 || core.exports[0][1] != 1 {
		t.Fatal("export did not use original visible order", core.exports)
	}
}

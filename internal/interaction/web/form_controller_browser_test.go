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

type formControllerCore struct {
	*cardsControllerCore
	view    ui.FormView
	missing bool
}

func (c *formControllerCore) WebFormView(_ context.Context, ref string) (ui.FormView, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.missing || ref != "ui:original#1" {
		return ui.FormView{}, agent.ErrNotFound
	}
	return c.view, nil
}

// Real projection/React/native controls/HTTP/CAS; accepted view and form ingress
// are fixtures. The separate App gate must prove real form transactions.
func TestUIFormViewControllerBrowserOriginalControlsDraftAndBoundSubmit(t *testing.T) {
	if os.Getenv("STEWARD_TEST_APP_BROWSER") != "1" {
		t.Skip("run by make test-browser")
	}
	props := json.RawMessage(`{"title":"Original form","submitLabel":"Continue","fields":[{"name":"notes","label":"Notes","type":"textarea"},{"name":"count","label":"Count","type":"number","required":true,"min":1,"max":5},{"name":"date","label":"Date","type":"date"},{"name":"email","label":"Email","type":"email"},{"name":"mode","label":"Mode","type":"select","required":true,"options":[{"value":"train","label":"Train"},{"value":"bus","label":"Bus"}]},{"name":"extras","label":"Extras","type":"multiselect","options":[{"value":"meal","label":"Meal"},{"value":"bag","label":"Bag"}]},{"name":"confirm","label":"Confirm","type":"boolean","required":true}]}`)
	var fields ui.FormProps
	if err := json.Unmarshal(props, &fields); err != nil {
		t.Fatal(err)
	}
	core := &formControllerCore{cardsControllerCore: &cardsControllerCore{states: map[string]ui.ViewState{}}, view: ui.FormView{Props: fields, Defaults: map[string]string{"notes": "Owning initial", "count": "", "date": "", "email": "", "mode": "", "extras": "[]", "confirm": "false"}}}
	root, err := filepath.Abs("../../../web")
	if err != nil {
		t.Fatal(err)
	}
	build := `import{build}from'../../../web/node_modules/esbuild/lib/main.js';const result=await build({stdin:{contents:"import{enhanceProjections}from'./src/projections';import{flushUIStates}from'./src/intelligent-ui/state';for(const id of ['projection','baseline']){const node=document.getElementById(id);node.dataset.uiSpec=document.getElementById(id+'-spec').textContent;node.dataset.uiFallback='Original fallback'}enhanceProjections(document);window.flushForm=flushUIStates;",resolveDir:process.argv[1],sourcefile:'form-controller.tsx',loader:'tsx'},bundle:true,jsx:'automatic',write:false,format:'iife',define:{'process.env.NODE_ENV':'"production"'}});process.stdout.write(result.outputFiles[0].contents)`
	program, err := exec.Command("node", "--input-type=module", "-e", build, root).Output()
	if err != nil {
		t.Fatal(err)
	}
	makeSpec := func(managed bool) []byte {
		c := ui.Component{Type: "openintelligentui", Name: "steward.form", Props: props, WidgetRef: "ui:original#1", FormID: "form-v2-original", MessageID: "message-original"}
		if managed {
			c.ViewRef = "ui:original#1"
		}
		data, _ := json.Marshal(ui.Spec{SchemaVersion: 1, Fallback: "Original fallback", Components: []ui.Component{c}})
		return data
	}
	server := &Server{Core: core, Config: Config{TokenRef: "credential:test"}, Secrets: fakeSecrets{}}
	host := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/fixture.js":
			w.Header().Set("Content-Type", "text/javascript")
			_, _ = w.Write(program)
			return
		case "/fixture":
			core.mu.Lock()
			core.missing = r.URL.Query().Get("missing") == "1"
			core.mu.Unlock()
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			_, _ = w.Write([]byte(`<!doctype html><link rel="stylesheet" href="/style.css"><style>#projection,#baseline{width:calc(100% - 48px);max-width:672px;margin:24px}#baseline{position:absolute;visibility:hidden}</style><div id="projection"></div><div id="baseline"></div><script type="application/json" id="projection-spec">` + string(makeSpec(true)) + `</script><script type="application/json" id="baseline-spec">` + string(makeSpec(false)) + `</script><script src="/fixture.js"></script>`))
			return
		}
		server.ServeHTTP(w, r)
	}))
	defer host.Close()
	evidence := t.TempDir()
	if out := os.Getenv("STEWARD_APP_BROWSER_EVIDENCE"); out != "" {
		evidence = filepath.Join(out, "form-controller")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	if out, err := exec.CommandContext(ctx, "node", "../../../guest-browser/form-controller-browser.mjs", host.URL, evidence).CombinedOutput(); err != nil {
		t.Fatalf("form controller: %v\n%s", err, out)
	}
}

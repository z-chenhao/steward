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

	"github.com/z-chenhao/assistant/internal/delivery/ui"
	"github.com/z-chenhao/assistant/internal/plugin/intelligentui/contract"
)

// Actual projection/React/HTTP/CAS with accepted display and action receipt
// fixtures. The separate real Actor and App gates must prove execution rights.
func TestUIActionViewBrowserFeedbackRecoveryAndOriginalSource(t *testing.T) {
	if os.Getenv("STEWARD_TEST_APP_BROWSER") != "1" {
		t.Skip("run by make test-browser")
	}
	view := contract.ActionView{Component: 0, WorkingLabel: "Working", UnconfirmedError: "Action not confirmed. Retry.", UnknownError: "Owning unknown. Check the original receipt.", CancelledError: "Action cancelled.", NotExecutedError: "Action was not completed.", FailedError: "Action failed. Retry.", SuccessIndicator: false}
	props := json.RawMessage(`{"id":"refresh","label":"Refresh values","tool":"mcp__ui__refresh","arguments":{}}`)
	root, err := filepath.Abs("../../../web")
	if err != nil {
		t.Fatal(err)
	}
	build := `import{build}from'../../../web/node_modules/esbuild/lib/main.js';const r=await build({stdin:{contents:"import{enhanceProjections}from'./src/projections';for(const id of ['projection','baseline']){const node=document.getElementById(id);node.dataset.uiSpec=document.getElementById(id+'-spec').textContent;node.dataset.uiFallback='Original fallback'}enhanceProjections(document);",resolveDir:process.argv[1],sourcefile:'action-controller.tsx',loader:'tsx'},bundle:true,jsx:'automatic',write:false,format:'iife',define:{'process.env.NODE_ENV':'"production"'}});process.stdout.write(r.outputFiles[0].contents)`
	program, err := exec.Command("node", "--input-type=module", "-e", build, root).Output()
	if err != nil {
		t.Fatal(err)
	}
	core := &cardsControllerCore{states: map[string]ui.ViewState{}}
	server := Server{Core: core, Config: Config{TokenRef: "credential:test"}, Secrets: fakeSecrets{}}
	host := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/fixture.js":
			w.Header().Set("Content-Type", "text/javascript")
			_, _ = w.Write(program)
			return
		case "/fixture":
			component := ui.Component{Type: "openintelligentui", Name: "steward.action", Props: props, WidgetRef: "ui:original#1", ActionManaged: true}
			spec := ui.Spec{SchemaVersion: ui.Version, Fallback: "Original fallback", Components: []ui.Component{component}, ActionViews: map[string]contract.ActionView{"ui:original#1": view}}
			if r.URL.Query().Get("missing") == "1" {
				spec.ActionViews = nil
			}
			encoded, _ := json.Marshal(spec)
			component.ActionManaged = false
			component.WidgetRef = "ui:original#0"
			baseline, _ := json.Marshal(ui.Spec{SchemaVersion: ui.Version, Fallback: "Original fallback", Components: []ui.Component{component}})
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			_, _ = w.Write([]byte(`<!doctype html><link rel="stylesheet" href="/style.css"><style>#projection,#baseline{width:calc(100% - 48px);max-width:672px;margin:24px}#baseline{position:absolute;visibility:hidden}</style><div id="projection"></div><div id="baseline"></div><script type="application/json" id="projection-spec">` + string(encoded) + `</script><script type="application/json" id="baseline-spec">` + string(baseline) + `</script><script src="/fixture.js"></script>`))
			return
		}
		server.ServeHTTP(w, r)
	}))
	defer host.Close()
	evidence := t.TempDir()
	if out := os.Getenv("STEWARD_APP_BROWSER_EVIDENCE"); out != "" {
		evidence = filepath.Join(out, "action-controller")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	if out, err := exec.CommandContext(ctx, "node", "../../../guest-browser/action-controller-browser.mjs", host.URL, evidence).CombinedOutput(); err != nil {
		t.Fatalf("action feedback browser: %v\n%s", err, out)
	}
}

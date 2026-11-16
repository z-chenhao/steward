package web

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/z-chenhao/assistant/internal/delivery/ui"
	"github.com/z-chenhao/assistant/internal/plugin/intelligentui/contract"
)

// Actual projection/React/HTTP and original CSS. Display acceptance and ingress
// are fixture boundaries; separate Actor/App tests prove their real ownership.
func TestUIMeetingViewBrowserOriginalControlsChoiceAndPreview(t *testing.T) {
	if os.Getenv("STEWARD_TEST_APP_BROWSER") != "1" {
		t.Skip("run by make test-browser")
	}
	props := json.RawMessage(`{"reasonForScheduling":"Review","meetingDuration":30,"timeSlots":[{"date":"2026-11-17","time":"09:15"},{"date":"2026-11-18","time":"14:00"}]}`)
	view := contract.MeetingView{Component: 0, Title: "Review", Slots: []contract.MeetingSlot{{Choice: 1, Date: "2026-11-18", Time: "14:00", Duration: "30 min"}, {Choice: 0, Date: "2026-11-17", Time: "09:15", Duration: "30 min"}}, DeclineLabel: "None of these work", SelectedTitle: "Time Selected", DeclinedTitle: "No Time Selected", SavingLabel: "Saving selection…", SelectError: "Your selection was not confirmed. Retry the same time.", DeclineError: "Your selection was not confirmed. Retry the same choice.", SavedLabel: "Time preference saved."}
	formID := strings.Repeat("a", 64)
	root, err := filepath.Abs("../../../web")
	if err != nil {
		t.Fatal(err)
	}
	build := `import{build}from'../../../web/node_modules/esbuild/lib/main.js';const r=await build({stdin:{contents:"import{enhanceProjections}from'./src/projections';import{createUIPreview}from'./src/intelligent-ui/preview';for(const id of ['projection','baseline']){const node=document.getElementById(id);node.dataset.uiSpec=document.getElementById(id+'-spec').textContent;node.dataset.uiFallback='Original fallback'}enhanceProjections(document);const preview=createUIPreview(document.getElementById('preview'));window.previewMeeting=data=>preview.receive(data);",resolveDir:process.argv[1],sourcefile:'meeting-controller.tsx',loader:'tsx'},bundle:true,jsx:'automatic',write:false,format:'iife',define:{'process.env.NODE_ENV':'"production"'}});process.stdout.write(r.outputFiles[0].contents)`
	program, err := exec.Command("node", "--input-type=module", "-e", build, root).Output()
	if err != nil {
		t.Fatal(err)
	}
	server := Server{Core: &cardsControllerCore{states: map[string]ui.ViewState{}}, Config: Config{TokenRef: "credential:test"}, Secrets: fakeSecrets{}}
	host := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/fixture.js":
			w.Header().Set("Content-Type", "text/javascript")
			_, _ = w.Write(program)
			return
		case "/fixture":
			c := ui.Component{Type: "openintelligentui", Name: "scheduleTime", Props: props, FormID: formID, MessageID: "original-message", MeetingManaged: true}
			if r.URL.Query().Get("accepted") == "1" {
				c.Submitted = true
				c.Response = "1"
			}
			spec := ui.Spec{SchemaVersion: ui.Version, Fallback: "Original fallback", Components: []ui.Component{c}, MeetingViews: map[string]contract.MeetingView{formID: view}}
			if r.URL.Query().Get("missing") == "1" {
				spec.MeetingViews = nil
			}
			data, _ := json.Marshal(spec)
			baseline := c
			baseline.MeetingManaged = false
			baseline.Props = json.RawMessage(`{"reasonForScheduling":"Review","meetingDuration":30,"timeSlots":[{"date":"2026-11-18","time":"14:00"},{"date":"2026-11-17","time":"09:15"}]}`)
			legacy, _ := json.Marshal(ui.Spec{SchemaVersion: ui.Version, Fallback: "Original fallback", Components: []ui.Component{baseline}})
			preview, _ := json.Marshal(view)
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			_, _ = w.Write([]byte(`<!doctype html><link rel="stylesheet" href="/style.css"><style>#projection,#baseline,#preview{width:calc(100% - 48px);max-width:672px;margin:24px}#baseline{position:absolute;visibility:hidden}</style><div id="projection">` + string(ui.HTML(data, "Original fallback")) + `</div><div id="baseline"></div><div id="preview"></div><script type="application/json" id="projection-spec">` + string(data) + `</script><script type="application/json" id="baseline-spec">` + string(legacy) + `</script><script type="application/json" id="view">` + string(preview) + `</script><script src="/fixture.js"></script>`))
			return
		}
		server.ServeHTTP(w, r)
	}))
	defer host.Close()
	evidence := t.TempDir()
	if out := os.Getenv("STEWARD_APP_BROWSER_EVIDENCE"); out != "" {
		evidence = filepath.Join(out, "meeting-controller")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	if out, err := exec.CommandContext(ctx, "node", "../../../guest-browser/meeting-controller-browser.mjs", host.URL, evidence).CombinedOutput(); err != nil {
		t.Fatalf("meeting browser: %v\n%s", err, out)
	}
}

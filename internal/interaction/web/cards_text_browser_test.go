package web

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/z-chenhao/assistant/internal/delivery/ui"
)

// Called by the required Cards HTTP/browser gate. Producer encoding has its own
// regression; this checks the real sanitizer, CSP, fixed driver and Worker.
func testCardsLiteralTextBrowser(t *testing.T) {
	t.Helper()
	raw, err := os.ReadFile("../../plugin/intelligentui/testdata/original-cards.json")
	if err != nil {
		t.Fatal(err)
	}
	var fixture struct {
		HTML string `json:"initial_html"`
	}
	if err = json.Unmarshal(raw, &fixture); err != nil {
		t.Fatal(err)
	}
	worker, err := os.ReadFile("../../plugin/intelligentui/view/cards-worker.js")
	if err != nil {
		t.Fatal(err)
	}
	css, err := os.ReadFile("../../plugin/intelligentui/view/cards.css")
	if err != nil {
		t.Fatal(err)
	}
	styles, err := os.ReadFile("assets/widget.css")
	if err != nil {
		t.Fatal(err)
	}
	value := "Choice\r\nline\x00中文🌙<script>"
	markup := strings.Replace(fixture.HTML, `aria-pressed="false">Alpha</button>`, `aria-pressed="false" data-card-text="`+base64.StdEncoding.EncodeToString([]byte(value))+`">Alpha</button>`, 1)
	if markup == fixture.HTML {
		t.Fatal("fixture target missing")
	}
	view := ui.CardsView{HTML: markup, Worker: string(worker), CSS: string(css), Styles: styles, CompactMaxWidth: 760}
	expected, _ := json.Marshal(value)
	host := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/cards" {
			renderCardsView(w, r, view)
			return
		}
		if r.URL.Path == "/preview" {
			renderReadonlyView(w, r, ui.PlanView{HTML: markup, CSS: string(css), Styles: styles, CompactMaxWidth: 760}, true, "Cards preview", cardsMarkup(markup))
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte(`<!doctype html><iframe sandbox="allow-scripts" src="` + r.URL.Query().Get("view") + `"></iframe><script>window.messages=[];addEventListener('message',e=>{if(e.source!==frames[0])return;messages.push(e.data);if(e.data.type==='ready')frames[0].postMessage({channel:'steward-cards-view',type:'restore',state:{index:0},selected:` + string(expected) + `},'*')})</script>`))
	}))
	defer host.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	script := `import assert from 'node:assert/strict';import{chromium}from'../../../guest-browser/node_modules/playwright-core/index.mjs';const[url,expected]=process.argv.slice(1),value=JSON.parse(expected);const browser=await chromium.launch({headless:true,...(process.env.STEWARD_BROWSER_EXECUTABLE?{executablePath:process.env.STEWARD_BROWSER_EXECUTABLE}:{})});try{const page=await browser.newPage();for(const route of['/cards','/preview']){await page.goto(url+'/?view='+route);const frame=page.frameLocator('iframe');await frame.locator('.native-card-title').first().waitFor();await frame.locator('.native-card-title').first().evaluate((n,v)=>{if(n.textContent!==v)throw Error('raw text changed')},value);assert.equal(await frame.locator('[data-card-text]').count(),0);if(route==='/cards'){await page.waitForFunction(()=>messages.some(m=>m.type==='restored'));assert.equal(await frame.locator('.native-card-title').first().getAttribute('aria-pressed'),'true');await page.evaluate(()=>frames[0].postMessage({channel:'steward-cards-view',type:'group',selected:'Beta'},'*'));await frame.locator('.native-card-title').nth(1).evaluate(n=>new Promise(resolve=>{const check=()=>n.getAttribute('aria-pressed')==='true'?resolve():requestAnimationFrame(check);check()}));await page.evaluate(v=>frames[0].postMessage({channel:'steward-cards-view',type:'group',selected:v},'*'),value);await frame.locator('.native-card-title').first().evaluate(n=>new Promise(resolve=>{const check=()=>n.getAttribute('aria-pressed')==='true'?resolve():requestAnimationFrame(check);check()}))}else{assert.equal(await frame.locator('body').evaluate(n=>n.inert),true);assert.equal(await frame.locator('script').count(),1)}}}finally{await browser.close()}`
	if out, err := exec.CommandContext(ctx, "node", "--input-type=module", "-e", script, host.URL, string(expected)).CombinedOutput(); err != nil {
		t.Fatalf("literal Cards browser: %v\n%s", err, out)
	}
}

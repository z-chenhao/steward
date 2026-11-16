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

// Actual createUIPreview/React and protected HTTP; accepted domain views are
// fixtures. This does not replace the full App streaming/lifecycle gate.
func TestUICardsPreviewBrowserReadOnlyAndRevision(t *testing.T) {
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
		HTML  string          `json:"initial_html"`
		Props json.RawMessage `json:"props"`
	}
	if err := json.Unmarshal(read("../../plugin/intelligentui/testdata/original-cards.json"), &original); err != nil {
		t.Fatal(err)
	}
	core := &planPreviewCore{previewStyleCore: &previewStyleCore{streamAccess: &streamAccess{}, styles: read("assets/widget.css")}}
	s := &Server{Core: core, Config: Config{TokenRef: "credential:test"}, Secrets: fakeSecrets{}}
	preview := ui.Preview{Title: "Draft cards", CardsManaged: true, Components: []ui.Component{{Name: "steward.cards", Props: original.Props}}, CardsViews: map[int]ui.CardsView{0: {HTML: original.HTML, CSS: string(read("../../plugin/intelligentui/view/cards.css")), CompactMaxWidth: 760}}}
	emit := func(kind string) {
		s.observeUIPreview(agent.Event{RunID: "original-run", RunKind: "main", Kind: kind, PreviewID: "session", PreviewKey: "item", UIPreview: &preview})
	}
	emit("ui_preview_start")
	emit("ui_preview_update")
	webRoot, err := filepath.Abs("../../../web")
	if err != nil {
		t.Fatal(err)
	}
	build := `import{build}from'../../../web/node_modules/esbuild/lib/main.js';const result=await build({stdin:{contents:"import{createUIPreview}from'./src/intelligent-ui/preview';window.preview=createUIPreview(document.getElementById('root'));window.preview.receive(JSON.parse(document.getElementById('fixture').textContent));",resolveDir:process.argv[1],sourcefile:'cards-preview.tsx',loader:'tsx'},bundle:true,jsx:'automatic',write:false,format:'iife',define:{'process.env.NODE_ENV':'\"production\"'}});process.stdout.write(result.outputFiles[0].contents);`
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
			_, _ = w.Write([]byte(`<!doctype html><link rel="stylesheet" href="/style.css"><style>#root{max-width:672px;margin:24px}</style><div id="root"></div><script type="application/json" id="fixture">` + string(raw) + `</script><script src="/fixture.js"></script>`))
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
		evidence = filepath.Join(base, "cards-preview")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	script := `import{chromium}from'../../../guest-browser/node_modules/playwright-core/index.mjs';import{mkdir,writeFile}from'node:fs/promises';const[url,out]=process.argv.slice(1);await mkdir(out,{recursive:true});const browser=await chromium.launch({headless:true,...(process.env.STEWARD_BROWSER_EXECUTABLE?{executablePath:process.env.STEWARD_BROWSER_EXECUTABLE}:{})});try{const context=await browser.newContext();await context.request.post(url+'/session',{form:{token:'private-token-at-least-24-bytes'},maxRedirects:0});const page=await context.newPage();const requests=[];page.on('request',r=>requests.push(r.url()));await page.goto(url+'/fixture');const frame=page.frameLocator('iframe[title="Cards preview"]');await frame.locator('.native-card-title').first().waitFor();await page.waitForFunction(()=>Number.parseInt(document.querySelector('iframe')?.style.height)>50);const source=await page.locator('iframe').getAttribute('src');const isolation=await frame.locator('body').evaluate(body=>({inert:body.inert,origin:self.origin,scripts:document.scripts.length,links:[...document.querySelectorAll('a')].map(a=>a.getAttribute('href')),images:document.images.length}));if(!isolation.inert||isolation.origin!=='null'||isolation.scripts!==1||isolation.links.some(Boolean)||isolation.images!==0)throw Error('draft authority leak '+JSON.stringify(isolation));if(await frame.locator('.native-card-title').first().textContent()!=='Alpha')throw Error('original card missing');for(const width of[1400,390]){await page.setViewportSize({width,height:900});for(const theme of['light','dark']){await page.evaluate(theme=>document.documentElement.classList.toggle('dark',theme==='dark'),theme);await frame.locator('body').evaluate(()=>document.fonts.ready);await frame.locator('body').evaluate(()=>new Promise(resolve=>requestAnimationFrame(()=>requestAnimationFrame(resolve))));const style=await frame.locator('.native-card-title').first().evaluate(n=>({size:getComputedStyle(n).fontSize,font:getComputedStyle(n).fontFamily}));if(style.size!=='14px'||!style.font.includes('Inter'))throw Error('typography changed '+JSON.stringify(style));await page.screenshot({path:out+'/preview-'+theme+'-'+width+'.png'})}}await page.evaluate(async()=>window.preview.receive(await(await fetch('/fixture-update')).json()));await page.waitForFunction(old=>document.querySelector('iframe')?.getAttribute('src')!==old,source);await frame.locator('.native-card-title').first().waitFor();if((await context.request.get(url+source)).status()!==404)throw Error('stale preview remained readable');const newSource=await page.locator('iframe').getAttribute('src');await page.evaluate(async()=>window.preview.receive(await(await fetch('/fixture-discard')).json()));if(await page.locator('iframe').count())throw Error('discard did not unmount');if((await context.request.get(url+newSource)).status()!==404)throw Error('GET resurrected discarded view');if(requests.some(r=>r.includes('/api/ui/state')||r.includes('/ui/cards-image')||r.includes('/ui/cards-link')))throw Error('draft requested delivery grants');await writeFile(out+'/report.json',JSON.stringify({scope:'actual createUIPreview/React and protected HTTP; domain accepted views are fixtures, not full App streaming',isolation,originalContent:true,revisionReplacesFrame:true,staleReadDenied:true,discardUnmountsAndDenies:true,noDeliveryGrants:true},null,2));await context.close()}finally{await browser.close()}`
	if output, err := exec.CommandContext(ctx, "node", "--input-type=module", "-e", script, server.URL, evidence).CombinedOutput(); err != nil {
		t.Fatalf("Cards preview browser: %v\n%s", err, output)
	}
}

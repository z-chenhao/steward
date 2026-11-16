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

func TestUIChartPreviewBrowserReadOnlyAndRevision(t *testing.T) {
	if os.Getenv("STEWARD_TEST_APP_BROWSER") != "1" {
		t.Skip("run by make test-browser")
	}
	worker, err := os.ReadFile("../../plugin/intelligentui/view/charts-worker.js")
	if err != nil {
		t.Fatal(err)
	}
	styles, err := os.ReadFile("assets/widget.css")
	if err != nil {
		t.Fatal(err)
	}
	props := json.RawMessage(`{"title":"Trend","description":"Original preview","data":[{"label":"A","value":10},{"label":"B","value":42}]}`)
	core := &planPreviewCore{previewStyleCore: &previewStyleCore{streamAccess: &streamAccess{}, styles: styles}}
	server := &Server{Core: core, Config: Config{TokenRef: "credential:test"}, Secrets: fakeSecrets{}}
	preview := ui.Preview{Title: "Draft charts", ChartManaged: true, Components: []ui.Component{{Name: "steward.lineChart", Props: props}}, ChartViews: map[int]ui.ChartView{0: {Name: "steward.lineChart", Title: "Trend", Description: "Original preview", Props: props, Worker: string(worker)}}}
	emit := func(kind string) {
		server.observeUIPreview(agent.Event{RunID: "original-run", RunKind: "main", Kind: kind, PreviewID: "session", PreviewKey: "item", UIPreview: &preview})
	}
	emit("ui_preview_start")
	emit("ui_preview_update")
	webRoot, err := filepath.Abs("../../../web")
	if err != nil {
		t.Fatal(err)
	}
	build := `import{build}from'../../../web/node_modules/esbuild/lib/main.js';const r=await build({stdin:{contents:"import{createUIPreview}from'./src/intelligent-ui/preview';window.preview=createUIPreview(document.getElementById('root'));preview.receive(JSON.parse(document.getElementById('fixture').textContent));",resolveDir:process.argv[1],sourcefile:'chart-preview.tsx',loader:'tsx'},bundle:true,jsx:'automatic',write:false,format:'iife',define:{'process.env.NODE_ENV':'"production"'}});process.stdout.write(r.outputFiles[0].contents)`
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
		evidence = filepath.Join(dir, "chart-preview")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	script := `import assert from'node:assert/strict';import{mkdir,writeFile}from'node:fs/promises';import{chromium}from'../../../guest-browser/node_modules/playwright-core/index.mjs';const[url,out]=process.argv.slice(1);await mkdir(out,{recursive:true});const browser=await chromium.launch({headless:true,...(process.env.STEWARD_BROWSER_EXECUTABLE?{executablePath:process.env.STEWARD_BROWSER_EXECUTABLE}:{})});try{const context=await browser.newContext({reducedMotion:'reduce'});await context.request.post(url+'/session',{form:{token:'private-token-at-least-24-bytes'},maxRedirects:0});const page=await context.newPage({viewport:{width:1400,height:900}}),requests=[];page.on('request',r=>requests.push(r.url()));await page.goto(url+'/fixture');const chart=page.locator('[data-openintelligentui="steward.lineChart"]');await chart.locator('.recharts-line-dot').nth(1).waitFor();assert.equal(await chart.evaluate(n=>n.inert),true);assert.equal(await chart.locator('input[type=range]').count(),0);assert.equal(await page.locator('#fixture').textContent().then(s=>s.includes('onmessage')),false);const frame=chart.frameLocator('iframe[title="Chart policy"]'),source=await chart.locator('iframe').getAttribute('src');await frame.locator('body').evaluate(()=>{for(const value of[{type:'restore',state:{start:1,end:1}},{type:'group',query:'no match',selected:'B'},{type:'zoom'},{type:'range',state:{start:1,end:1}},{type:'select',item:0},{type:'capture',id:'forged-capture'},{type:'export',id:'forged-export'}])dispatchEvent(new MessageEvent('message',{source:parent,data:{channel:'steward-chart-view',...value}}))});await chart.locator('.recharts-line-dot').first().evaluate(n=>n.dispatchEvent(new KeyboardEvent('keydown',{key:'Enter',bubbles:true})));await chart.locator('.native-chart').evaluate(()=>new Promise(resolve=>requestAnimationFrame(()=>requestAnimationFrame(resolve))));assert.equal(await chart.locator('.recharts-line-dot').count(),2);for(const width of[1400,390]){await page.setViewportSize({width,height:900});for(const theme of['light','dark']){await page.evaluate(theme=>document.documentElement.classList.toggle('dark',theme==='dark'),theme);await page.locator('body').evaluate(()=>document.fonts.ready);await page.waitForFunction(()=>{const plot=document.querySelector('.recharts-responsive-container'),svg=plot?.querySelector('svg');return svg&&Math.abs(svg.getBoundingClientRect().width-plot.clientWidth)<1});const type=await chart.locator('.native-answer-title').evaluate(n=>({font:getComputedStyle(n).fontFamily,size:getComputedStyle(n).fontSize}));assert.equal(type.size,'14px');assert.ok(type.font.includes('Inter'));await chart.screenshot({path:out+'/preview-'+width+'-'+theme+'.png'})}}await page.evaluate(async()=>preview.receive(await(await fetch('/fixture-update')).json()));await page.waitForFunction(old=>document.querySelector('iframe')?.getAttribute('src')!==old,source);await chart.locator('.recharts-line-dot').nth(1).waitFor();assert.equal((await context.request.get(url+source)).status(),404);const next=await chart.locator('iframe').getAttribute('src');assert.equal((await context.request.get(url+next+'&format=export&rows=0')).status(),400);await page.evaluate(async()=>preview.receive(await(await fetch('/fixture-discard')).json()));assert.equal(await page.locator('iframe').count(),0);assert.equal((await context.request.get(url+next)).status(),404);assert.equal(requests.some(u=>u.includes('/api/ui/state')||u.includes('/api/ui/action')||u.includes('format=export')),false);await writeFile(out+'/report.json',JSON.stringify({scope:'actual streaming preview React/HTTP/opaque Worker; accepted domain is fixture',readonlyCommandsIgnored:true,noStateOrDownloadRequests:true,privateWorkerNotBroadcast:true,revisionInvalidatesOldRead:true,discardCannotBeResurrected:true,originalContentAndTypography:true},null,2));await context.close()}finally{await browser.close()}`
	if out, err := exec.CommandContext(ctx, "node", "--input-type=module", "-e", script, host.URL, evidence).CombinedOutput(); err != nil {
		t.Fatalf("chart preview browser: %v\n%s", err, out)
	}
}

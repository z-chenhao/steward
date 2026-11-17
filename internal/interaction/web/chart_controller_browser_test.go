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

	"github.com/z-chenhao/assistant/internal/agent"
	"github.com/z-chenhao/assistant/internal/delivery/ui"
)

type chartControllerCore struct {
	*cardsControllerCore
	view       ui.ChartView
	revoked    bool
	exportRows [][]int
}

func (c *chartControllerCore) WebChartView(_ context.Context, ref string) (ui.ChartView, error) {
	if ref != "ui:original#1" {
		return ui.ChartView{}, agent.ErrNotFound
	}
	return c.view, nil
}

func (c *chartControllerCore) WebChartExport(_ context.Context, ref string, rows []int) (ui.ChartExport, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if ref != "ui:original#1" || c.revoked {
		return ui.ChartExport{}, agent.ErrNotFound
	}
	var facts struct {
		Data []struct {
			Label string  `json:"label"`
			Value float64 `json:"value"`
		} `json:"data"`
	}
	if json.Unmarshal(c.view.Props, &facts) != nil {
		return ui.ChartExport{}, agent.ErrNotFound
	}
	result := ui.ChartExport{Title: c.view.Title, Columns: []string{"Label", "Value"}, Rows: [][]json.RawMessage{}}
	seen := map[int]bool{}
	for _, i := range rows {
		if i < 0 || i >= len(facts.Data) || seen[i] {
			return ui.ChartExport{}, agent.ErrNotFound
		}
		seen[i] = true
		label, _ := json.Marshal(facts.Data[i].Label)
		value, _ := json.Marshal(facts.Data[i].Value)
		result.Rows = append(result.Rows, []json.RawMessage{label, value})
	}
	c.exportRows = append(c.exportRows, append([]int{}, rows...))
	return result, nil
}

func TestUIChartControllerBrowserProjectionCASAndRange(t *testing.T) {
	if os.Getenv("STEWARD_TEST_APP_BROWSER") != "1" {
		t.Skip("run by make test-browser")
	}
	worker, err := os.ReadFile("../../plugin/intelligentui/view/charts-worker.js")
	if err != nil {
		t.Fatal(err)
	}
	props := json.RawMessage(`{"title":"Trend","description":"Original chart","data":[{"label":"A","value":10},{"label":"B","value":42},{"label":"C","value":25}]}`)
	core := &chartControllerCore{cardsControllerCore: &cardsControllerCore{states: map[string]ui.ViewState{}}, view: ui.ChartView{Name: "steward.lineChart", Title: "Trend", Description: "Original chart", Props: props, Worker: string(worker)}}
	webRoot, err := filepath.Abs("../../../web")
	if err != nil {
		t.Fatal(err)
	}
	build := `import{build}from'../../../web/node_modules/esbuild/lib/main.js';const result=await build({stdin:{contents:"import{enhanceProjections}from'./src/projections';import{flushUIStates}from'./src/intelligent-ui/state';for(const id of ['projection','baseline']){const n=document.getElementById(id);n.dataset.uiSpec=document.getElementById(id+'-spec').textContent;n.dataset.uiFallback='Original fallback'}enhanceProjections(document);window.flushChart=flushUIStates;",resolveDir:process.argv[1],sourcefile:'chart-controller.tsx',loader:'tsx'},bundle:true,jsx:'automatic',write:false,format:'iife',define:{'process.env.NODE_ENV':'"production"'}});process.stdout.write(result.outputFiles[0].contents)`
	parentScript, err := exec.Command("node", "--input-type=module", "-e", build, webRoot).Output()
	if err != nil {
		t.Fatal(err)
	}
	makeSpec := func(managed bool) []byte {
		component := ui.Component{Type: "openintelligentui", Name: "steward.lineChart", Props: props, WidgetRef: "ui:original#1"}
		if managed {
			component.ViewRef = "ui:original#1"
		}
		raw, _ := json.Marshal(ui.Spec{SchemaVersion: 1, Fallback: "Original fallback", Components: []ui.Component{component}})
		return raw
	}
	server := &Server{Core: core, Config: Config{TokenRef: "credential:test"}, Secrets: fakeSecrets{}}
	host := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/fixture-revoke" {
			core.mu.Lock()
			core.revoked = true
			core.mu.Unlock()
			w.WriteHeader(204)
			return
		}
		if r.URL.Path == "/fixture.js" {
			w.Header().Set("Content-Type", "text/javascript")
			_, _ = w.Write(parentScript)
			return
		}
		if r.URL.Path == "/fixture" {
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			managedSpec := string(makeSpec(true))
			if r.URL.Query().Get("missing") == "1" {
				managedSpec = strings.Replace(managedSpec, `"view_ref":"ui:original#1"`, `"view_ref":"ui:missing#1"`, 1)
			}
			_, _ = w.Write([]byte(`<!doctype html><link rel="stylesheet" href="/style.css"><style>#projection,#baseline{width:calc(100% - 48px);max-width:672px;margin:24px}#baseline{position:absolute;visibility:hidden}</style><div id="projection"></div><div id="baseline"></div><script type="application/json" id="projection-spec">` + managedSpec + `</script><script type="application/json" id="baseline-spec">` + string(makeSpec(false)) + `</script><script src="/fixture.js"></script>`))
			return
		}
		server.ServeHTTP(w, r)
	}))
	defer host.Close()
	evidence := t.TempDir()
	if dir := os.Getenv("STEWARD_APP_BROWSER_EVIDENCE"); dir != "" {
		evidence = filepath.Join(dir, "chart-controller")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	script := `import assert from'node:assert/strict';import{mkdir,writeFile,readFile}from'node:fs/promises';import{chromium}from'../../../guest-browser/node_modules/playwright-core/index.mjs';const[url,out]=process.argv.slice(1);await mkdir(out,{recursive:true});const browser=await chromium.launch({headless:true,...(process.env.STEWARD_BROWSER_EXECUTABLE?{executablePath:process.env.STEWARD_BROWSER_EXECUTABLE}:{})});try{const context=await browser.newContext({reducedMotion:"reduce"});await context.request.post(url+'/session',{form:{token:'private-token-at-least-24-bytes'},maxRedirects:0});const page=await context.newPage({viewport:{width:1400,height:900}});await page.goto(url+'/fixture');let downloads=0;page.on('download',()=>downloads++);const chart=page.locator('#projection');await chart.locator('.recharts-line-dot').nth(2).waitFor();assert.equal(await chart.locator('iframe[title="Chart policy"]').count(),1);assert.equal(await page.locator('#baseline iframe').count(),0);const menu=async name=>{await chart.getByRole('button',{name:'Options',exact:true}).click();await page.getByRole('menuitem',{name,exact:true}).click()};await menu('Zoom');await chart.locator('input[type=range]').nth(1).fill('1');await chart.locator('.recharts-line-dot').nth(2).waitFor({state:'detached'});await chart.locator('.recharts-line-dot').evaluateAll(nodes=>{if(nodes.length!==2)throw Error('range projection not applied')});await page.evaluate(()=>flushChart());const before=await page.evaluate(async()=>await(await fetch('/api/ui/state?ref=ui:original%231')).json());assert.deepEqual(before.state,{start:0,end:1});assert.deepEqual(before.model_context,{title:'Trend',range:[0,1]});const pendingDownload=page.waitForEvent('download');await menu('Download CSV');const originalCSV=await readFile(await(await pendingDownload).path());assert.equal(originalCSV.toString(),'\ufeff"Label","Value"\r\n"A","10"\r\n"B","42"');assert.equal((await context.request.get(url+'/ui/chart?ref=ui:original%231&format=export&rows=0,0')).status(),400);await page.reload();await chart.locator('.recharts-line-dot').nth(2).waitFor();const recovered=await page.evaluate(async()=>await(await fetch('/api/ui/state?ref=ui:original%231')).json());assert.deepEqual(recovered,before);assert.equal(await chart.locator('input[type=range]').count(),0);const checks=[];for(const width of[1400,390]){await page.setViewportSize({width,height:900});for(const theme of['light','dark']){await page.evaluate(theme=>document.documentElement.classList.toggle('dark',theme==='dark'),theme);await page.locator('body').evaluate(()=>document.fonts.ready);await chart.locator('.recharts-surface').evaluate(()=>new Promise(resolve=>requestAnimationFrame(()=>requestAnimationFrame(resolve))));await page.waitForFunction(()=>['projection','baseline'].every(id=>{const plot=document.querySelector('#'+id+' .recharts-responsive-container'),svg=plot?.querySelector('svg');return svg&&Math.abs(svg.getBoundingClientRect().width-plot.clientWidth)<1}));const geometry=async root=>page.locator(root+' .recharts-line-curve').getAttribute('d');assert.equal(await geometry('#projection'),await geometry('#baseline'));const style=await chart.locator('.native-answer-title').evaluate(n=>({font:getComputedStyle(n).fontFamily,size:getComputedStyle(n).fontSize}));assert.equal(style.size,'14px');await chart.screenshot({path:out+'/chart-'+width+'-'+theme+'.png'});checks.push({width,theme,style})}}await context.request.get(url+'/fixture-revoke');const beforeDenied=downloads;await menu('Download CSV');await chart.getByText('Export failed.',{exact:true}).waitFor();assert.equal(downloads,beforeDenied);await page.goto(url+'/fixture?missing=1');await chart.locator('iframe').waitFor({state:'detached',timeout:15000});assert.equal(await chart.locator('svg').count(),0);await chart.getByText('Original fallback',{exact:true}).waitFor();await writeFile(out+'/report.json',JSON.stringify({scope:'real React/controller/HTTP/CAS with accepted-domain fixture; not full App or chart migration completion',originalDrawing:true,rangePersisted:true,firstReloadPreservesOriginalVersion:true,zoomRemainsLocal:true,missingManagedNeverUsesNative:true,originalFilteredCSVBytes:true,revokedExportDoesNotDownload:true,checks},null,2));await context.close()}finally{await browser.close()}`
	if out, err := exec.CommandContext(ctx, "node", "--input-type=module", "-e", script, host.URL, evidence).CombinedOutput(); err != nil {
		t.Fatalf("chart controller: %v\n%s", err, out)
	}
}

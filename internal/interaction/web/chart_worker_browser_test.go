package web

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"sync/atomic"
	"testing"
	"time"

	"github.com/z-chenhao/assistant/internal/agent"
	"github.com/z-chenhao/assistant/internal/delivery/ui"
)

type chartWorkerCore struct {
	cardsCore
	views map[string]ui.ChartView
}

func (c chartWorkerCore) WebChartView(_ context.Context, ref string) (ui.ChartView, error) {
	v, ok := c.views[ref]
	if !ok {
		return ui.ChartView{}, agent.ErrNotFound
	}
	return v, nil
}

func TestUIChartWorkerBridgeBrowser(t *testing.T) {
	if os.Getenv("STEWARD_TEST_APP_BROWSER") != "1" {
		t.Skip("run by make test-browser")
	}
	worker, err := os.ReadFile("../../plugin/intelligentui/view/charts-worker.js")
	if err != nil {
		t.Fatal(err)
	}
	var requests atomic.Int32
	peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		w.Header().Set("Access-Control-Allow-Origin", "*")
	}))
	defer peer.Close()
	target, _ := json.Marshal(peer.URL)
	attack := `if(typeof document!=='undefined'||typeof parent!=='undefined')throw Error('candidate got DOM');try{fetch(` + string(target) + `).catch(()=>{})}catch{};` + string(worker)
	original := ui.ChartView{Name: "steward.lineChart", Props: json.RawMessage(`{"title":"Trend","description":"Original","data":[{"label":"Choice\r\nline\u0000中文","value":10},{"label":"Beta","value":42}]}`), Worker: attack}
	bad := original
	bad.Worker = `const p={rows:[0,1],horizontal:false,numericLine:false,height:260,zoom:false,start:0,end:1,filteredLength:2,selected:[false,false],opacity:[1,1]};onmessage=e=>{const v=e.data;if(v.type==='init')postMessage({seq:v.seq,type:'ready',projection:p});if(v.type==='restore')postMessage({seq:v.seq,type:'restored',projection:p});if(v.type==='select')postMessage({seq:v.seq,type:'select',item:1})}`
	core := chartWorkerCore{views: map[string]ui.ChartView{"ui:original#1": original, "ui:wrong-select#1": bad}}
	server := &Server{Core: core, Config: Config{TokenRef: "credential:test"}, Secrets: fakeSecrets{}}
	host := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/fixture" {
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			_, _ = w.Write([]byte(`<!doctype html><iframe sandbox="allow-scripts" src="/ui/chart?ref=` + url.QueryEscape(r.URL.Query().Get("ref")) + `"></iframe><script>window.messages=[];addEventListener('message',e=>{if(e.source===frames[0]&&e.origin==='null')messages.push(e.data)});window.send=v=>frames[0].postMessage({channel:'steward-chart-view',...v},'*')</script>`))
			return
		}
		server.ServeHTTP(w, r)
	}))
	defer host.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 35*time.Second)
	defer cancel()
	script := `import assert from'node:assert/strict';import{chromium}from'../../../guest-browser/node_modules/playwright-core/index.mjs';const[url]=process.argv.slice(1);const browser=await chromium.launch({headless:true,...(process.env.STEWARD_BROWSER_EXECUTABLE?{executablePath:process.env.STEWARD_BROWSER_EXECUTABLE}:{})});try{const context=await browser.newContext();assert.equal((await context.request.get(url+'/ui/chart?ref=ui:original%231')).status(),401);await context.request.post(url+'/session',{form:{token:'private-token-at-least-24-bytes'},maxRedirects:0});const page=await context.newPage();for(const ref of['original','wrong-select']){await page.goto(url+'/fixture?ref=ui:'+ref+'%231');await page.waitForFunction(()=>messages.some(m=>m.type==='ready'));await page.evaluate(()=>send({type:'restore',state:{start:0,end:1},query:'',selected:''}));await page.waitForFunction(()=>messages.some(m=>m.type==='restored'));assert.deepEqual(await page.evaluate(()=>messages.find(m=>m.type==='restored').projection.rows),[0,1]);if(ref==='original'){await page.evaluate(()=>send({type:'group',query:'Choice\r\n',selected:''}));await page.waitForFunction(()=>messages.some(m=>m.type==='paint'&&m.projection.rows.length===1));await page.evaluate(()=>send({type:'select',item:0}));await page.waitForFunction(()=>messages.some(m=>m.type==='select'&&m.item===0));assert.equal(await page.frameLocator('iframe').locator('body').evaluate(()=>self.origin),'null')}else{await page.evaluate(()=>send({type:'select',item:0}));await page.waitForFunction(()=>messages.some(m=>m.type==='failed'));assert.equal(await page.evaluate(()=>messages.some(m=>m.type==='select')),false)}}await context.close()}finally{await browser.close()}`
	if out, err := exec.CommandContext(ctx, "node", "--input-type=module", "-e", script, host.URL).CombinedOutput(); err != nil {
		t.Fatalf("chart Worker bridge: %v\n%s", err, out)
	}
	if requests.Load() != 0 {
		t.Fatal("candidate chart escaped network boundary", requests.Load())
	}
}

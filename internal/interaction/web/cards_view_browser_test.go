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
)

// Real authenticated HTTP/CSP and owning package runtime; accepted domain view
// and parent state are fixtures, not a full App/Run or CAS integration proof.
func TestUICardsViewBrowserIsolationAndOriginalStyles(t *testing.T) {
	if os.Getenv("STEWARD_TEST_APP_BROWSER") != "1" {
		t.Skip("run by make test-browser")
	}
	raw, err := os.ReadFile("../../plugin/intelligentui/testdata/original-cards.json")
	if err != nil {
		t.Fatal(err)
	}
	var original struct {
		HTML         string `json:"initial_html"`
		SelectedHTML string `json:"selected_html"`
	}
	if err = json.Unmarshal(raw, &original); err != nil {
		t.Fatal(err)
	}
	read := func(path string) string {
		t.Helper()
		raw, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		return string(raw)
	}
	view := ui.CardsView{HTML: original.HTML + `<script>globalThis.markupInjected=true</script>`, CSS: read("../../plugin/intelligentui/view/cards.css"), Worker: read("../../plugin/intelligentui/view/cards-worker.js"), Styles: []byte(read("assets/widget.css")), CompactMaxWidth: 760}
	s := &Server{Core: cardsCore{view: view}, Config: Config{TokenRef: "credential:test"}, Secrets: fakeSecrets{}}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/fixture" {
			w.Header().Set("Content-Type", "text/html")
			_, _ = w.Write([]byte(`<!doctype html><link rel="stylesheet" href="/style.css"><div class="openintelligentui-native" style="position:absolute;visibility:hidden;width:672px"><dialog open class="answer-surface" id="baseline">` + original.SelectedHTML + `</dialog></div><iframe sandbox="allow-scripts" style="width:672px;height:400px;border:0;max-width:100%" src="/ui/cards?ref=ui:original%231"></iframe><script>
window.messages=[];const frame=document.querySelector('iframe');window.environment=()=>{const style=getComputedStyle(document.documentElement),tokens={};for(const key of ['--card','--border','--foreground','--muted-foreground','--hover','--font-ui','--control-border'])tokens[key]=style.getPropertyValue(key).trim();frame.contentWindow.postMessage({channel:'steward-cards-view',type:'theme',theme:document.documentElement.classList.contains('dark')?'dark':'light',tokens,viewportWidth:innerWidth},'*')};addEventListener('message',event=>{if(event.source!==frame.contentWindow||event.origin!=='null'||event.data?.channel!=='steward-cards-view')return;messages.push(event.data);if(event.data.type==='view-state'){const i=event.data.state.index,controls=document.querySelector('#baseline .native-carousel-controls'),buttons=controls.querySelectorAll('button');buttons[0].disabled=i===0;buttons[1].disabled=i===1;controls.querySelector('span').textContent=(i+1)+' / 2'}if(event.data.type==='ready'){environment();frame.contentWindow.postMessage({channel:'steward-cards-view',type:'restore',state:{index:1},selected:'Beta'},'*')}if(event.data.type==='height')frame.style.height=Math.min(4000,Math.max(1,event.data.height))+'px'});</script>`))
			return
		}
		s.ServeHTTP(w, r)
	}))
	defer server.Close()
	evidence := t.TempDir()
	if directory := os.Getenv("STEWARD_APP_BROWSER_EVIDENCE"); directory != "" {
		evidence = filepath.Join(directory, "cards-view")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	script := `import{chromium}from'../../../guest-browser/node_modules/playwright-core/index.mjs';import{mkdir,writeFile}from'node:fs/promises';const[url,out]=process.argv.slice(1);await mkdir(out,{recursive:true});const browser=await chromium.launch({headless:true,...(process.env.STEWARD_BROWSER_EXECUTABLE?{executablePath:process.env.STEWARD_BROWSER_EXECUTABLE}:{})});try{const context=await browser.newContext();if((await context.request.get(url+'/ui/cards?ref=ui:original%231')).status()!==401)throw Error('anonymous view allowed');if((await context.request.post(url+'/session',{form:{token:'private-token-at-least-24-bytes'},maxRedirects:0})).status()!==303)throw Error('session failed');const page=await context.newPage();await page.goto(url+'/fixture');await page.waitForFunction(()=>messages.some(m=>m.type==='restored'));const frame=page.frameLocator('iframe');if(await frame.locator('.native-carousel-controls span').textContent()!=='2 / 2')throw Error('accepted runtime did not restore');const isolation=await frame.locator('body').evaluate(async()=>{let parentBlocked=false,storageBlocked=false,networkBlocked=false;try{parent.document.body}catch{parentBlocked=true}try{localStorage.getItem('private')}catch{storageBlocked=true}try{await fetch('https://outside.invalid/private')}catch{networkBlocked=true}return{origin:self.origin,parentBlocked,storageBlocked,networkBlocked,injected:globalThis.markupInjected}});if(isolation.origin!=='null'||!isolation.parentBlocked||!isolation.storageBlocked||!isolation.networkBlocked||isolation.injected)throw Error('Cards isolation lost '+JSON.stringify(isolation));await frame.getByRole('button',{name:'Previous card',exact:true}).click();await page.waitForFunction(()=>messages.some(m=>m.type==='view-state'&&m.state.index===0));await page.mouse.move(1000,600);const checks=[];for(const width of[1400,761,760,390]){await page.setViewportSize({width,height:900});for(const theme of['light','dark']){await page.evaluate(theme=>{document.documentElement.classList.toggle('dark',theme==='dark');environment()},theme);await frame.locator('body').evaluate(()=>document.fonts.ready);await frame.locator('.native-answer-title').hover();await frame.locator('body').evaluate(()=>new Promise(resolve=>requestAnimationFrame(()=>requestAnimationFrame(resolve))));for(const selector of['.native-card-title','.native-card-title[aria-pressed=true]','.native-result-card p','.native-carousel-controls','.answer-icon-button','.native-answer-title']){const fields=['fontFamily','fontSize','lineHeight','minHeight','color','backgroundColor','borderTopWidth','borderTopColor','borderRadius','paddingTop','paddingRight'];const expected=await page.locator('#baseline '+selector).first().evaluate(async(node,fields)=>{await Promise.all(node.getAnimations().filter(a=>Number.isFinite(a.effect.getTiming().iterations)).map(a=>a.finished.catch(()=>{})));return Object.fromEntries(fields.map(k=>[k,getComputedStyle(node)[k]]))},fields);const actual=await frame.locator(selector).first().evaluate(async(node,fields)=>{await Promise.all(node.getAnimations().filter(a=>Number.isFinite(a.effect.getTiming().iterations)).map(a=>a.finished.catch(()=>{})));return Object.fromEntries(fields.map(k=>[k,getComputedStyle(node)[k]]))},fields);if(JSON.stringify(actual)!==JSON.stringify(expected))throw Error('original Cards style changed '+width+' '+selector+' '+JSON.stringify({expected,actual,debug:await frame.locator(selector).first().evaluate(n=>({hover:n.matches(':hover'),focus:n.matches(':focus'),disabled:n.matches(':disabled'),class:n.className,css:getComputedStyle(n).cssText}))}));checks.push({width,theme,selector,actual})}await page.screenshot({path:out+'/cards-'+theme+'-'+width+'.png'})}}await writeFile(out+'/report.json',JSON.stringify({scope:'real protected HTTP/CSP and owning runtime, fixture accepted domain/parent state; not full App/CAS/default migration',isolation,checks},null,2));await context.close()}finally{await browser.close()}`
	if out, err := exec.CommandContext(ctx, "node", "--input-type=module", "-e", script, server.URL, evidence).CombinedOutput(); err != nil {
		t.Fatalf("Cards browser: %v\n%s", err, out)
	}
	testCardsLiteralTextBrowser(t)
}

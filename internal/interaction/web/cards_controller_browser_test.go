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
	"sync"
	"testing"
	"time"

	"github.com/z-chenhao/assistant/internal/agent"
	"github.com/z-chenhao/assistant/internal/conversation"
	"github.com/z-chenhao/assistant/internal/delivery/ui"
)

// The real Host transport uses the normal HTTP/CAS routes. This fixture owns
// only two original presentation refs, not a database or full App simulation.
type cardsControllerCore struct {
	cardsCore
	mu        sync.Mutex
	states    map[string]ui.ViewState
	writes    []string
	forbidden int
}

func (c *cardsControllerCore) WebUIState(_ context.Context, ref string) (ui.ViewState, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if ref != "ui:original#0" && ref != "ui:original#1" {
		c.forbidden++
		return ui.ViewState{}, agent.ErrNotFound
	}
	if state, ok := c.states[ref]; ok {
		return state, nil
	}
	return ui.ViewState{State: json.RawMessage(`{}`), ModelContext: json.RawMessage(`{}`)}, nil
}
func (c *cardsControllerCore) SaveWebUIState(_ context.Context, ref string, state ui.ViewState) (ui.ViewState, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if ref != "ui:original#0" && ref != "ui:original#1" {
		c.forbidden++
		return ui.ViewState{}, agent.ErrNotFound
	}
	if !ui.ValidViewState(state) {
		return ui.ViewState{}, conversation.ErrInvalid
	}
	if c.states[ref].Version != state.Version {
		return ui.ViewState{}, conversation.ErrConflict
	}
	state.Version++
	c.states[ref] = state
	c.writes = append(c.writes, ref)
	return state, nil
}
func (c *cardsControllerCore) RequestWebUIAction(context.Context, string, string) (agent.Receipt, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.forbidden++
	return agent.Receipt{}, agent.ErrNotFound
}
func (c *cardsControllerCore) WebUIActionResult(context.Context, string, string) (ui.ActionResult, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.forbidden++
	return ui.ActionResult{}, agent.ErrNotFound
}

func (c *cardsControllerCore) WebCardsLink(_ context.Context, ref string, item int) (string, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if ref != "ui:original#1" || item != 0 {
		c.forbidden++
		return "", agent.ErrNotFound
	}
	return "https://example.com/alpha", nil
}

func TestUICardsControllerBrowserProjectionCASGroupAndRecovery(t *testing.T) {
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
		Props json.RawMessage `json:"props"`
		HTML  string          `json:"initial_html"`
	}
	if err := json.Unmarshal(read("../../plugin/intelligentui/testdata/original-cards.json"), &original); err != nil {
		t.Fatal(err)
	}
	core := &cardsControllerCore{cardsCore: cardsCore{view: ui.CardsView{HTML: original.HTML, CSS: string(read("../../plugin/intelligentui/view/cards.css")), Worker: string(read("../../plugin/intelligentui/view/cards-worker.js")), Styles: read("assets/widget.css"), CompactMaxWidth: 760, Contexts: []ui.CardsContext{{Title: "Card choices", VisibleItem: "Alpha"}, {Title: "Card choices", VisibleItem: "Beta"}}}}, states: map[string]ui.ViewState{}}
	webRoot, err := filepath.Abs("../../../web")
	if err != nil {
		t.Fatal(err)
	}
	build := `import{build}from'../../../web/node_modules/esbuild/lib/main.js';const result=await build({stdin:{contents:"import{enhanceProjections}from'./src/projections';import{flushUIStates}from'./src/intelligent-ui/state';const node=document.getElementById('projection');node.dataset.uiSpec=document.getElementById('fixture').textContent;node.dataset.uiFallback='Original fallback';enhanceProjections(document);window.flushCards=flushUIStates;",resolveDir:process.argv[1],sourcefile:'cards-controller.tsx',loader:'tsx'},bundle:true,jsx:'automatic',write:false,format:'iife',define:{'process.env.NODE_ENV':'\"production\"'}});process.stdout.write(result.outputFiles[0].contents);`
	parentScript, err := exec.Command("node", "--input-type=module", "-e", build, webRoot).Output()
	if err != nil {
		t.Fatal(err)
	}
	spec := `{"schema_version":1,"fallback":"Original fallback","components":[{"type":"openintelligentui","name":"Table","props":{"title":"Values","columns":["Category","Value"],"rows":[["Alpha","10"],["Beta","20"]],"source":"Fixture"},"widget_ref":"ui:original#0"},{"type":"openintelligentui","name":"steward.cards","props":` + string(original.Props) + `,"widget_ref":"ui:original#1","view_ref":"ui:original#1","text":"Original Cards fallback"}]}`
	var specValue any
	if err = json.Unmarshal([]byte(spec), &specValue); err != nil {
		t.Fatal(err)
	}
	specJSON, _ := json.Marshal(specValue)
	s := &Server{Core: core, Config: Config{TokenRef: "credential:test"}, Secrets: fakeSecrets{}}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/fixture.js":
			w.Header().Set("Content-Type", "text/javascript")
			_, _ = w.Write(parentScript)
			return
		case "/fixture":
			bodySpec := string(specJSON)
			if r.URL.Query().Get("legacy") == "1" {
				bodySpec = strings.Replace(bodySpec, `,"view_ref":"ui:original#1"`, "", 1)
			}
			w.Header().Set("Content-Type", "text/html")
			_, _ = w.Write([]byte(`<!doctype html><link rel="stylesheet" href="/style.css"><style>#projection{max-width:672px;margin:24px}</style><div id="projection"></div><script type="application/json" id="fixture">` + bodySpec + `</script><script src="/fixture.js"></script>`))
			return
		case "/api/message":
			core.mu.Lock()
			core.forbidden++
			core.mu.Unlock()
		}
		s.ServeHTTP(w, r)
	}))
	defer server.Close()
	evidence := t.TempDir()
	if directory := os.Getenv("STEWARD_APP_BROWSER_EVIDENCE"); directory != "" {
		evidence = filepath.Join(directory, "cards-controller")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 75*time.Second)
	defer cancel()
	script := `import{waitForUIState}from'../../../guest-browser/ui-state.mjs';import{chromium}from'../../../guest-browser/node_modules/playwright-core/index.mjs';import{mkdir,writeFile}from'node:fs/promises';const[url,out]=process.argv.slice(1);await mkdir(out,{recursive:true});const browser=await chromium.launch({headless:true,...(process.env.STEWARD_BROWSER_EXECUTABLE?{executablePath:process.env.STEWARD_BROWSER_EXECUTABLE}:{})});try{const context=await browser.newContext();await context.request.post(url+'/session',{form:{token:'private-token-at-least-24-bytes'},maxRedirects:0});const page=await context.newPage();await page.setViewportSize({width:1400,height:900});await page.goto(url+'/fixture');const frame=page.frameLocator('iframe[title="Cards view"]');await frame.getByRole('button',{name:'Next card',exact:true}).waitFor();await frame.getByRole('button',{name:'Next card',exact:true}).waitFor({state:'visible'});await page.waitForFunction(()=>document.querySelector('iframe')?.contentWindow!==null);await page.evaluate(()=>{window.cardOpens=[];const open=window.open.bind(window);window.open=(...args)=>{window.cardOpens.push(args);return open(...args)}});await context.route('https://example.com/**',route=>route.fulfill({contentType:'text/html',body:'Original destination'}));const firstPopup=context.waitForEvent('page');await frame.getByRole('link',{name:'Details',exact:true}).click();const opened=await firstPopup;await opened.waitForURL('https://example.com/alpha');await opened.close();const keyboardPopup=context.waitForEvent('page');await frame.getByRole('link',{name:'Details',exact:true}).focus();await page.keyboard.press('Enter');const keyboardOpened=await keyboardPopup;await keyboardOpened.waitForURL('https://example.com/alpha');await keyboardOpened.close();await frame.locator('body').evaluate(()=>{const bridge=new MessageChannel();parent.postMessage({channel:'steward-cards-view',type:'gesture-port'},'*',[bridge.port2]);bridge.port1.postMessage({type:'open-link',item:0});parent.postMessage({channel:'steward-cards-view',type:'open-link',item:0},'*');const link=document.querySelector('a');link.click();link.dispatchEvent(new KeyboardEvent('keydown',{key:'Enter',bubbles:true}))});await frame.getByRole('button',{name:'Next card',exact:true}).click();await waitForUIState(page,'ui:original#1',s=>s.state.index===1&&s.model_context.visible_item==='Beta');if(await page.evaluate(()=>window.cardOpens.length)!==2)throw Error('forged link port or synthetic click opened a window');await frame.getByRole('button',{name:'Beta',exact:true}).click();await page.waitForFunction(()=>document.querySelector('.answer-table tbody')?.textContent?.includes('Beta')&&!document.querySelector('.answer-table tbody')?.textContent?.includes('Alpha'));await page.evaluate(()=>window.flushCards());const surface=page.locator('dialog.answer-surface').filter({has:page.locator('iframe[title="Cards view"]')});const before=await frame.locator('.native-carousel').evaluate(n=>n.outerHTML);for(const[label,mode]of[['Expand','fullscreen'],['Open dialog','modal'],['Keep visible','pip']]){await surface.getByRole('button',{name:'Options',exact:true}).click();await surface.getByRole('menuitem',{name:label,exact:true}).click();await page.waitForFunction(mode=>document.querySelector('dialog:has(iframe[title="Cards view"])')?.dataset.mode===mode,mode);if(['modal','fullscreen'].includes(mode)&&!await surface.evaluate(n=>n.matches(':modal')))throw Error('surface lost top-layer semantics');if(await frame.locator('.native-carousel-controls span').textContent()!=='2 / 2')throw Error('mode switch lost index');await frame.locator('body').evaluate(()=>{dispatchEvent(new KeyboardEvent('keydown',{key:'Escape',bubbles:true}));parent.postMessage({channel:'steward-cards-view',type:'escape'},'*');const forged=new MessageChannel();parent.postMessage({channel:'steward-cards-view',type:'gesture-port'},'*',[forged.port2]);forged.port1.postMessage({type:'escape'});Object.defineProperty(KeyboardEvent.prototype,'key',{get(){return 'FORGED'},configurable:true})});await page.evaluate(()=>new Promise(resolve=>requestAnimationFrame(()=>requestAnimationFrame(resolve))));if(await surface.getAttribute('data-mode')!==mode)throw Error('forged Escape or port changed display mode');await frame.getByRole('button',{name:'Beta',exact:true}).focus();await page.keyboard.press('Escape');await page.waitForFunction(()=>document.querySelector('dialog:has(iframe[title="Cards view"])')?.dataset.mode==='inline');if(await frame.locator('.native-carousel-controls span').textContent()!=='2 / 2')throw Error('trusted Escape lost current item');await surface.getByRole('button',{name:'Options',exact:true}).click();await surface.getByRole('menuitem',{name:label,exact:true}).click();await page.waitForFunction(mode=>document.querySelector('dialog:has(iframe[title="Cards view"])')?.dataset.mode===mode,mode);await surface.getByRole('button',{name:'Close expanded view',exact:true}).click();await page.waitForFunction(()=>document.querySelector('dialog:has(iframe[title="Cards view"])')?.dataset.mode==='inline')}await page.reload();await frame.locator('.native-carousel-controls span').filter({hasText:'2 / 2'}).waitFor();await frame.locator('.native-card-title[aria-pressed=true]').filter({hasText:'Beta'}).waitFor();await page.waitForFunction(()=>document.querySelector('.answer-table tbody')?.textContent?.includes('Beta')&&!document.querySelector('.answer-table tbody')?.textContent?.includes('Alpha'));const restored=await frame.locator('.native-carousel').evaluate(n=>n.outerHTML);if(restored!==before)throw Error('original component DOM changed across reload');await page.evaluate(()=>postMessage({channel:'steward-cards-view',type:'select',item:0},'*'));await frame.locator('body').evaluate(()=>{parent.postMessage({channel:'steward-cards-view',type:'invoke',method:'callTool',args:{name:'danger'}},'*');parent.postMessage({channel:'steward-cards-view',type:'invoke',method:'sendPrompt',args:{text:'forged input'}},'*');parent.postMessage({channel:'steward-cards-view',type:'select',item:999},'*');parent.postMessage({channel:'steward-cards-view',type:'view-state',ref:'ui:foreign#5',state:{index:1},model_context:{title:'Card choices',visible_item:'Beta'}},'*')});await page.evaluate(()=>window.flushCards());if(await frame.getByRole('button',{name:'Beta',exact:true}).getAttribute('aria-pressed')!=='true')throw Error('forged selection reached group');for(const width of[1400,390]){await page.setViewportSize({width,height:900});for(const theme of['light','dark']){await page.evaluate(theme=>document.documentElement.classList.toggle('dark',theme==='dark'),theme);await frame.locator('body').evaluate(()=>new Promise(resolve=>requestAnimationFrame(()=>requestAnimationFrame(resolve))));await frame.locator('.native-carousel').evaluate(async n=>Promise.all(n.getAnimations({subtree:true}).filter(a=>Number.isFinite(a.effect.getTiming().iterations)).map(a=>a.finished.catch(()=>{}))));await page.locator('#projection').evaluate(async n=>Promise.all(n.getAnimations({subtree:true}).filter(a=>Number.isFinite(a.effect.getTiming().iterations)).map(a=>a.finished.catch(()=>{}))));if(!await frame.locator('.native-carousel-list').evaluate(n=>Math.abs(n.scrollLeft-Math.min(n.children[1].offsetLeft-n.offsetLeft,n.scrollWidth-n.clientWidth))<=1))throw Error('responsive layout lost original visible item');await page.screenshot({path:out+'/controller-'+theme+'-'+width+'.png'})}}const external=await page.evaluate(async()=>{const saved=await(await fetch('/api/ui/state?ref=ui:original%231')).json();const result=await fetch('/api/ui/state',{method:'POST',headers:{'Content-Type':'application/json'},body:JSON.stringify({ref:'ui:original#1',...saved})});return{status:result.status,version:(await result.json()).version}});if(external.status!==200)throw Error('external original-state write failed');await frame.getByRole('button',{name:'Previous card',exact:true}).click();await page.getByRole('alert').filter({hasText:'Updated in another window. Reload to continue.'}).waitFor();const retained=await page.evaluate(async()=>(await(await fetch('/api/ui/state?ref=ui:original%231')).json()).state.index);if(retained!==1)throw Error('stale CAS overwrote concurrent state');await page.reload();await frame.locator('.native-carousel-controls span').filter({hasText:'2 / 2'}).waitFor({timeout:5000}).catch(async error=>{throw Error('post-conflict original restore '+JSON.stringify({saved:await page.evaluate(async()=>(await(await fetch('/api/ui/state?ref=ui:original%231')).json())),dom:await frame.locator('.native-carousel').evaluate(n=>n.outerHTML),geometry:await frame.locator('.native-carousel-list').evaluate(n=>({width:n.clientWidth,left:n.scrollLeft,auto:getComputedStyle(n).gridAutoColumns,tracks:getComputedStyle(n).gridTemplateColumns,style:getComputedStyle(n).width,compact:document.documentElement.hasAttribute('data-host-compact'),body:getComputedStyle(document.body).display,parentWidth:getComputedStyle(n.parentElement).width,matches:[...document.styleSheets].flatMap(s=>[...s.cssRules]).filter(r=>r.selectorText&&n.matches(r.selectorText)).map(r=>r.cssText),items:[...n.children].map(c=>({offset:c.offsetLeft,width:c.clientWidth,max:getComputedStyle(c).maxWidth,trackWidth:getComputedStyle(c).width}))}))})+' '+error)});await frame.locator('.native-card-title[aria-pressed=true]').filter({hasText:'Beta'}).waitFor();await page.waitForFunction(()=>document.querySelector('.answer-table tbody')?.textContent?.includes('Beta')&&!document.querySelector('.answer-table tbody')?.textContent?.includes('Alpha'));const recovered=await page.evaluate(async()=>(await(await fetch('/api/ui/state?ref=ui:original%231')).json()));if(recovered.version!==external.version||recovered.state.index!==1||recovered.model_context.visible_item!=='Beta')throw Error('recovery rewrote original state/version');await page.route('**/ui/cards?**',route=>route.fulfill({status:404,contentType:'text/plain',body:'unavailable'}));await page.reload();await page.getByText('Original Cards fallback',{exact:true}).waitFor({timeout:12000});if(await page.locator('iframe[title="Cards view"],#projection .native-carousel').count())throw Error('missing managed view used native fallback');await page.setViewportSize({width:1400,height:900});await page.goto(url+'/fixture?legacy=1');const legacy=page.locator('#projection .native-carousel');await legacy.locator('.native-carousel-controls span').filter({hasText:'2 / 2'}).waitFor();await legacy.locator('.native-card-title[aria-pressed=true]').filter({hasText:'Beta'}).waitFor();if(await page.locator('#projection iframe').count())throw Error('historical missing view_ref adopted managed renderer');const legacyState=await page.evaluate(async()=>(await(await fetch('/api/ui/state?ref=ui:original%231')).json()));if(legacyState.version!==recovered.version||JSON.stringify(legacyState.state)!==JSON.stringify(recovered.state)||JSON.stringify(legacyState.model_context)!==JSON.stringify(recovered.model_context))throw Error('historical native hydration rewrote original state/version');await page.waitForFunction(()=>document.querySelector('.answer-table tbody')?.textContent?.includes('Beta')&&!document.querySelector('.answer-table tbody')?.textContent?.includes('Alpha'));await writeFile(out+'/report.json',JSON.stringify({scope:'actual projection/React controller, protected HTTP/CAS transport, owning runtime; fixture domain/state store, not full App/SQLite',trustedLinkMouseAndKeyboard:true,forgedLinkPortAndSyntheticClickRejected:true,groupFiltersActualTable:true,originalDOMRestored:true,displayModesRetainState:true,trustedEscapeAllModes:true,closeButtonAllModes:true,syntheticEscapeAndCounterfeitPortRejected:true,nativeKeyboardGetterTamperResisted:true,responsiveVisibleItemRetained:true,staleCASRejected:true,recoveryPreservesOriginalVersion:true,foreignRefAndInvokesIgnored:true,missingViewTextFallback:true,historicalMissingViewRefUsesNative:true,legacyStateVersionRetained:true,originalRef:'ui:original#1'},null,2));await context.close()}finally{await browser.close()}`
	if out, err := exec.CommandContext(ctx, "node", "--input-type=module", "-e", script, server.URL, evidence).CombinedOutput(); err != nil {
		t.Fatalf("Cards controller: %v\n%s", err, out)
	}
	core.mu.Lock()
	defer core.mu.Unlock()
	if core.forbidden != 0 || len(core.writes) == 0 {
		t.Fatal("controller widened authority or never persisted", core.forbidden, core.writes)
	}
}

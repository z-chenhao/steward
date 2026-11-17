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

type planBrowserCore struct{ planCore }

func (c planBrowserCore) UIPreviewStyles(context.Context, string) ([]byte, bool, error) {
	return c.view.Styles, true, nil
}

// Real protected HTTP/CSP and Chrome; only the accepted read-only view is a
// fixture. Full App/projection/preview wiring is a separate required gate.
func TestUIPlanViewBrowserIsolationAndDetails(t *testing.T) {
	if os.Getenv("STEWARD_TEST_APP_BROWSER") != "1" {
		t.Skip("run by make test-browser")
	}
	styles, err := os.ReadFile("assets/widget.css")
	if err != nil {
		t.Fatal(err)
	}
	css, err := os.ReadFile("../../plugin/intelligentui/testdata/original-plan.css")
	if err != nil {
		t.Fatal(err)
	}
	core := planCore{view: ui.PlanView{HTML: `<details class="native-plan"><summary>Plan: Go</summary><div class="native-plan-body"><p>Original plan body</p></div></details><script>window.planInjected=true</script>`, CSS: strings.Replace(string(css), "@media(max-width:760px){.native-plan summary{min-height:44px}}", "html[data-host-compact] .native-plan summary{min-height:44px}", 1), Styles: styles, CompactMaxWidth: 760}}
	webRoot, err := filepath.Abs("../../../web")
	if err != nil {
		t.Fatal(err)
	}
	buildScript := `import {build} from '../../../web/node_modules/esbuild/lib/main.js';const result=await build({stdin:{contents:"import {createElement} from 'react';import {createRoot} from 'react-dom/client';import {FrozenPlanView} from './src/intelligent-ui/plan-view';import {createUIPreview} from './src/intelligent-ui/preview';if(location.search.includes('preview=1')){createUIPreview(document.getElementById('root')).receive(JSON.parse(document.getElementById('preview-fixture').textContent))}else{createRoot(document.getElementById('root')).render(createElement(FrozenPlanView,{reference:'ui:original#0',fallback:'Original fallback'}));}",resolveDir:process.argv[1],sourcefile:'plan-parent.tsx',loader:'tsx'},bundle:true,jsx:'automatic',write:false,format:'iife',define:{'process.env.NODE_ENV':'\"production\"'}});process.stdout.write(result.outputFiles[0].contents);`
	parentScript, err := exec.Command("node", "--input-type=module", "-e", buildScript, webRoot).Output()
	if err != nil {
		t.Fatal("actual Host controller build", err)
	}
	s := &Server{Core: planBrowserCore{core}, Config: Config{TokenRef: "credential:test"}, Secrets: fakeSecrets{}}
	privateView := core.view
	privateView.Styles = nil // Styles are read by HTTP, never cached in preview payloads.
	preview := ui.Preview{Title: "Preview plan", PlanManaged: true, Components: []ui.Component{{Name: "plan_visualization", Props: json.RawMessage(`{}`)}}, PlanViews: map[int]ui.PlanView{0: privateView}}
	for _, kind := range []string{"ui_preview_start", "ui_preview_update"} {
		s.observeUIPreview(agent.Event{RunID: "fixture-run", RunKind: "main", Kind: kind, PreviewID: "fixture-session", PreviewKey: "fixture-item", UIPreview: &preview})
	}
	var cursor uint64
	previewFrame, _ := s.uiPreviews.read(&cursor)
	if len(previewFrame.Items) != 1 {
		t.Fatal("preview fixture rejected by payload quota")
	}
	previewJSON, _ := json.Marshal(previewFrame)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/fixture.js" {
			w.Header().Set("Content-Type", "text/javascript")
			_, _ = w.Write(parentScript)
			return
		}
		if r.URL.Path == "/fixture" {
			w.Header().Set("Content-Type", "text/html")
			_, _ = w.Write([]byte(`<!doctype html><link rel="stylesheet" href="/style.css"><style>` + string(css) + `#root{max-width:672px}.baseline{position:absolute;visibility:hidden}</style><details class="native-plan baseline"><summary>Original baseline</summary></details><div id="root"></div><script type="application/json" id="preview-fixture">` + string(previewJSON) + `</script><script src="/fixture.js"></script>`))
			return
		}
		s.ServeHTTP(w, r)
	}))
	defer server.Close()
	evidence := t.TempDir()
	if folder := os.Getenv("STEWARD_APP_BROWSER_EVIDENCE"); folder != "" {
		evidence = filepath.Join(folder, "plan-view")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	script := `import {chromium} from '../../../guest-browser/node_modules/playwright-core/index.mjs';import {mkdir,writeFile} from 'node:fs/promises';
const [url,evidence]=process.argv.slice(1);await mkdir(evidence,{recursive:true});const browser=await chromium.launch({headless:true,...(process.env.STEWARD_BROWSER_EXECUTABLE?{executablePath:process.env.STEWARD_BROWSER_EXECUTABLE}:{})});
try{const context=await browser.newContext();const denied=await context.request.get(url+'/ui/plan?ref=ui:original%230');if(denied.status()!==401)throw Error('view bypassed authentication');const login=await context.request.post(url+'/session',{form:{token:'private-token-at-least-24-bytes'},maxRedirects:0});if(login.status()!==303)throw Error('real session admission failed');const page=await context.newPage();await page.addInitScript(()=>{window.planHeights=[];addEventListener('message',event=>{if(event.source===document.querySelector('iframe')?.contentWindow&&event.data?.channel==='steward-plan-view'&&event.data.type==='height')window.planHeights.push(event.data.height)})});await page.goto(url+'/fixture');const frame=page.frameLocator('iframe');await frame.locator('summary').waitFor();await page.waitForFunction(()=>document.querySelector('iframe')?.style.height!=='50px');const beforeSpoof=await page.locator('iframe').evaluate(node=>node.style.height);await page.evaluate(()=>postMessage({channel:'steward-plan-view',type:'height',height:3999},'*'));await page.waitForTimeout(30);if(await page.locator('iframe').evaluate(node=>node.style.height)!==beforeSpoof)throw Error('controller accepted spoofed parent height');
if(!await frame.locator('details').evaluate(node=>!node.open)||!await frame.locator('body').evaluate(()=>self.origin==='null'&&window.planInjected===undefined))throw Error('view did not retain inert source isolation');
const blocked=await frame.locator('body').evaluate(()=>{let parentBlocked=false,storageBlocked=false;try{parent.document.body}catch{parentBlocked=true}try{localStorage.getItem('private')}catch{storageBlocked=true}return {parentBlocked,storageBlocked}});if(!blocked.parentBlocked||!blocked.storageBlocked)throw Error('opaque frame gained parent or storage');
await frame.locator('body').evaluate(()=>document.fonts.ready);const closed=await frame.locator('details').evaluate(node=>node.getBoundingClientRect().height);await frame.locator('summary').click();if(!await frame.locator('details').evaluate(node=>node.open))throw Error('native details did not expand');await page.waitForFunction(closed=>window.planHeights.some(height=>height>closed),closed);await frame.locator('summary').focus();await frame.locator('summary').press('Enter');if(await frame.locator('details').evaluate(node=>node.open))throw Error('native keyboard collapse failed');
for(const width of [1400,761,760,390]){await page.setViewportSize({width,height:900});for(const theme of ['light','dark']){await page.evaluate(theme=>{document.documentElement.classList.toggle('dark',theme==='dark')},theme);await frame.locator('details').evaluate(()=>new Promise(resolve=>requestAnimationFrame(()=>requestAnimationFrame(resolve))));if(await frame.locator('html').evaluate(node=>getComputedStyle(node).colorScheme)!==theme)throw Error('iframe color scheme did not follow Host');const expectedMinHeight=await page.locator('.baseline summary').evaluate(node=>getComputedStyle(node).minHeight);const actualMinHeight=await frame.locator('summary').evaluate(node=>getComputedStyle(node).minHeight);if(actualMinHeight!==expectedMinHeight)throw Error('host viewport parity failed '+width+': '+actualMinHeight+' vs '+expectedMinHeight);const font=await frame.locator('summary').evaluate(node=>getComputedStyle(node).fontFamily);if(!font.includes('Inter'))throw Error('sealed font missing');await page.screenshot({path:evidence+'/plan-'+theme+'-'+width+'.png'})}}
await page.route('**/ui/plan?**',route=>route.fulfill({status:404,contentType:'text/plain',body:'unavailable'}));await page.goto(url+'/fixture');await page.getByText('Original fallback',{exact:true}).waitFor({timeout:12000});if(await page.locator('#root iframe,#root details,#root .native-plan').count())throw Error('missing frozen view silently used a native renderer');
await page.goto(url+'/fixture?preview=1');const previewFrame=page.frameLocator('#root iframe');await previewFrame.locator('summary').waitFor();if(!await page.locator('#root iframe').evaluate(node=>node.src.includes('/ui/plan-preview?'))||!await previewFrame.locator('body').evaluate(node=>node.inert))throw Error('actual preview branch used native or active rendering');for(const width of [1400,390]){await page.setViewportSize({width,height:900});for(const theme of ['light','dark']){await page.evaluate(theme=>document.documentElement.classList.toggle('dark',theme==='dark'),theme);await previewFrame.locator('body').evaluate(()=>new Promise(resolve=>requestAnimationFrame(()=>requestAnimationFrame(resolve))));if(await previewFrame.locator('html').evaluate(node=>getComputedStyle(node).colorScheme)!==theme)throw Error('preview color scheme did not follow Host');await page.screenshot({path:evidence+'/plan-preview-'+theme+'-'+width+'.png'})}}
await writeFile(evidence+'/report.json',JSON.stringify({managedPreviewBranch:true,previewBodyInert:true,colorSchemeFollowsHost:true,missingViewTextFallback:true,authentication:true,opaqueParentAndStorage:true,candidateScriptBlocked:true,nativeDetailsClickAndKeyboard:true,heightNotifications:true,themesAndWidths:[1400,761,760,390],actualHostController:true,hostViewportParityAgainstOriginalCSS:true,spoofedHeightRejected:true,scope:'actual React controller and protected HTTP shell; fixture domain views, not full App/Run or default package migration'},null,2));await context.close()}finally{await browser.close()}`
	if out, err := exec.CommandContext(ctx, "node", "--input-type=module", "-e", script, server.URL, evidence).CombinedOutput(); err != nil {
		t.Fatalf("plan browser: %v\n%s", err, out)
	}
}

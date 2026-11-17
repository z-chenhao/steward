package web

import (
	"context"
	"github.com/z-chenhao/assistant/internal/observability"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// Exercise the real read-only routes and shared controls with representative
// fixture facts. Managed-package lifecycle acceptance is a separate App gate.
type managedBrowserTraceReader struct {
	*selectedTraceWorkbenchReader
	source string
}

func (r *managedBrowserTraceReader) TraceWorkbenchProgram(_ context.Context, reference string) (observability.TraceProgram, error) {
	style, err := os.ReadFile("../../plugin/traceinspector/view/trace-activity.css")
	if err != nil {
		return observability.TraceProgram{}, err
	}
	return observability.TraceProgram{ReaderRevision: reference, Source: r.source, Stylesheet: string(style)}, nil
}

// Representative nonempty histogram with original count and trace facts intact.
type legacyBrowserTraceReader struct{ traceReader }

func browserActivityFacts(out observability.Traces) observability.Traces {
	out.Activity = []observability.TraceBucket{
		{At: "2026-10-29T01:00:00Z", Until: "2026-10-29T01:00:20Z", Count: 1},
		{At: "2026-10-29T01:00:20Z", Until: "2026-10-29T01:00:40Z", Count: 0},
		{At: "2026-10-29T01:00:40Z", Until: "2026-10-29T01:01:00Z", Count: 0},
	}
	return out
}
func (r legacyBrowserTraceReader) Traces(ctx context.Context, q observability.TraceQuery) (observability.Traces, error) {
	out, err := r.traceReader.Traces(ctx, q)
	return browserActivityFacts(out), err
}
func (r *managedBrowserTraceReader) Traces(ctx context.Context, q observability.TraceQuery) (observability.Traces, error) {
	out, err := r.selectedTraceWorkbenchReader.Traces(ctx, q)
	return browserActivityFacts(out), err
}

func browserWireBody(q observability.BodyQuery) (observability.Body, error) {
	if q.Field != "wire" {
		return (traceReader{}).ObservationBody(context.Background(), q)
	}
	const body = `{"model":"fixture-model","system":"System <script>escaped</script>","messages":[{"role":"system","content":"private system"},{"role":"user","content":[{"text":"private user"},{"parts":["second line"]}]}],"tools":[{"function":{"name":"fixture_tool","parameters":{"type":"object"}}}],"temperature":0.5}`
	return observability.Body{Available: true, Content: body, Length: len(body)}, nil
}
func (r legacyBrowserTraceReader) ObservationBody(_ context.Context, q observability.BodyQuery) (observability.Body, error) {
	return browserWireBody(q)
}
func (r *managedBrowserTraceReader) ObservationBody(_ context.Context, q observability.BodyQuery) (observability.Body, error) {
	return browserWireBody(q)
}

func TestTraceWorkbenchBrowserReadOnlyControls(t *testing.T) { testTraceWorkbenchBrowser(t, false) }
func TestTraceWorkbenchBrowserManagedTreeAndColumnPolicies(t *testing.T) {
	testTraceWorkbenchBrowser(t, true)
}
func testTraceWorkbenchBrowser(t *testing.T, managed bool) {
	if os.Getenv("STEWARD_TEST_APP_BROWSER") != "1" {
		t.Skip("run by browser gate")
	}
	s := languageServer()
	s.Traces = legacyBrowserTraceReader{}
	if managed {
		source, err := os.ReadFile("../../plugin/traceinspector/view/trace-worker.js")
		if err != nil {
			t.Fatal(err)
		}
		s.Traces = &managedBrowserTraceReader{selectedTraceWorkbenchReader: &selectedTraceWorkbenchReader{revision: "sha256:" + strings.Repeat("a", 64)}, source: string(source)}
	}
	s.Inspector = &inspectionReader{}
	server := httptest.NewServer(s)
	defer server.Close()
	evidence := os.Getenv("STEWARD_TRACE_BROWSER_EVIDENCE")
	if evidence == "" && os.Getenv("STEWARD_APP_BROWSER_EVIDENCE") != "" {
		evidence = filepath.Join(os.Getenv("STEWARD_APP_BROWSER_EVIDENCE"), "trace-workbench")
	}
	if evidence == "" {
		evidence = filepath.Join(t.TempDir(), "trace")
	}
	variant := "legacy"
	if managed {
		variant = "managed"
	}
	evidence = filepath.Join(evidence, variant)
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	script := `import {chromium} from '../../../guest-browser/node_modules/playwright-core/index.mjs';
import {mkdir,writeFile} from 'node:fs/promises';
const [url,evidence]=process.argv.slice(1);await mkdir(evidence,{recursive:true});
const browser=await chromium.launch({headless:true,...(process.env.STEWARD_BROWSER_EXECUTABLE?{executablePath:process.env.STEWARD_BROWSER_EXECUTABLE}:{})});
const results=[];
try{
 for(const mobile of [false,true])for(const theme of ['light','dark']){
  const context=await browser.newContext({hasTouch:mobile,viewport:mobile?{width:390,height:844}:{width:1440,height:1000},colorScheme:theme});
  await context.addInitScript(()=>{window.__traceCopied=[];Object.defineProperty(navigator,'clipboard',{configurable:true,value:{writeText:async value=>{window.__traceCopied.push(value)}}})});
  await context.request.post(url+'/session',{form:{token:'private-token-at-least-24-bytes',next:'/developer/observability'}});
  const page=await context.newPage(),errors=[];page.on('pageerror',error=>errors.push(error.message));
  await page.goto(url+'/developer/observability');
  await page.locator('[data-trace-column]:not([disabled])').first().waitFor({state:'attached'});
  if(await page.locator('[data-trace-program-revision]').count())await page.waitForFunction(()=>!!document.querySelector('iframe[src*="/api/developer/traces/program"]'));
  await page.waitForFunction(()=>document.querySelector('[data-activity-max]')?.textContent==='1'&&document.querySelectorAll('.obs-activity-axis span').length===(document.querySelector('.obs-activity-chart').clientWidth<500?2:5));
  if(await page.locator('[data-trace-program-revision]').count()&&await page.locator('iframe[src*="/api/developer/traces/program"]').count()!==1)throw Error('columns and activity did not share one frozen program');
  if(await page.locator('.obs-trace-list tbody tr').count()!==1)throw Error('nonempty original list missing');
  if(!mobile&&theme==='light'){
   await page.setViewportSize({width:390,height:844});await page.waitForFunction(()=>document.querySelector('.obs-filter-rail').open===false);
   await page.evaluate(()=>{dispatchEvent(new PageTransitionEvent('pagehide'));dispatchEvent(new PageTransitionEvent('pageshow',{persisted:true}))});
   await page.locator('[data-trace-column]:not([disabled])').first().waitFor({state:'attached'});
   await page.setViewportSize({width:1440,height:1000});await page.waitForFunction(()=>document.querySelector('.obs-filter-rail').open===true);
  }

  const activity=page.locator('[data-trace-activity]'),firstBar=activity.locator('[data-bucket="0"]'),rangeText=activity.locator('[data-activity-selection]');
  await firstBar.focus();await page.getByRole('tooltip').filter({hasText:'1 traces'}).waitFor();
  await page.keyboard.press('Shift+ArrowRight');await rangeText.waitFor();
  if(!(await rangeText.textContent()).includes('1 traces'))throw Error('keyboard selected count changed');
  await page.keyboard.press('ArrowLeft');await rangeText.waitFor({state:'hidden'});
  await page.keyboard.press('ArrowRight');await page.waitForFunction(()=>document.activeElement?.getAttribute('data-bucket')==='1');
  await page.keyboard.press('Enter');await page.keyboard.press('Home');await page.waitForFunction(()=>document.activeElement?.getAttribute('data-bucket')==='0');
  if(new URL(page.url()).searchParams.has('since'))throw Error('empty bucket navigated');
  await page.keyboard.press('Enter');await page.waitForURL(target=>target.searchParams.get('since')==='2026-10-29T01:00:00Z'&&target.searchParams.get('until')==='2026-10-29T01:00:20Z');
  if(await page.locator('[data-trace-program-revision]').count()&&!new URL(page.url()).searchParams.has('reader_revision'))throw Error('activity navigation lost frozen reader');
  await page.goBack();await page.locator('[data-trace-column]:not([disabled])').first().waitFor({state:'attached'});await page.waitForFunction(()=>document.querySelector('[data-activity-max]')?.textContent==='1');
  if(mobile){
   await firstBar.tap();const explore=activity.locator('[data-activity-explore]');await explore.waitFor();await rangeText.waitFor();
   if(new URL(page.url()).searchParams.has('since')||!(await rangeText.textContent()).includes('1 traces'))throw Error('touch did not retain preview before confirmation');
   await explore.tap();await page.waitForURL(target=>target.searchParams.get('since')==='2026-10-29T01:00:00Z');
   await page.goBack();await page.waitForFunction(()=>document.querySelector('[data-activity-max]')?.textContent==='1');
   const touchBox=await activity.locator('.obs-activity-chart').boundingBox(),cdp=await context.newCDPSession(page),touchY=touchBox.y+touchBox.height/2;
   await cdp.send('Input.dispatchTouchEvent',{type:'touchStart',touchPoints:[{x:touchBox.x+touchBox.width*.9,y:touchY}]});
   for(let i=1;i<=8;i++)await cdp.send('Input.dispatchTouchEvent',{type:'touchMove',touchPoints:[{x:touchBox.x+touchBox.width*(.9-.8*i/8),y:touchY}]});
   await cdp.send('Input.dispatchTouchEvent',{type:'touchEnd',touchPoints:[]});
   await page.waitForFunction(()=>{const a=document.querySelector('[data-activity-explore]');return a&&!a.hidden&&new URL(a.href).searchParams.get('until')==='2026-10-29T01:01:00Z'});
   if(!(await rangeText.textContent()).includes('1 traces')||new URL(page.url()).searchParams.has('since'))throw Error('reverse touch brush lost persistent bounds or navigated');
   await page.keyboard.press('Escape');await rangeText.waitFor({state:'hidden'});
  }else{
   const chart=await activity.locator('.obs-activity-chart').boundingBox();
   await page.mouse.move(chart.x+chart.width*.9,chart.y+chart.height/2);await page.mouse.down();await page.mouse.move(chart.x+chart.width*.1,chart.y+chart.height/2,{steps:8});await page.mouse.up();
   await page.waitForURL(target=>target.searchParams.get('since')==='2026-10-29T01:00:00Z'&&target.searchParams.get('until')==='2026-10-29T01:01:00Z');
   await page.goBack();await page.waitForFunction(()=>document.querySelector('[data-activity-max]')?.textContent==='1');
  }
  await page.mouse.move(0,0);await page.locator('[data-trace-column]:not([disabled])').first().waitFor({state:'attached'});
  await page.getByRole('button',{name:'Columns',exact:true}).click();
  const toggle=page.locator('[data-trace-column="0"]');await toggle.uncheck();
  await page.waitForFunction(()=>document.querySelector('.obs-trace-list col').hidden);
  if(!await page.evaluate(()=>JSON.parse(localStorage.getItem('steward.trace.columns.v5'))[0]===false))throw Error('accepted column preference was not saved');
  await page.reload();await page.locator('[data-trace-column]:not([disabled])').first().waitFor({state:'attached'});await page.waitForFunction(()=>document.querySelector('.obs-trace-list col').hidden);
  await page.getByRole('button',{name:'Columns',exact:true}).click();
  await toggle.check();await page.waitForFunction(()=>!document.querySelector('.obs-trace-list col').hidden);await page.getByRole('button',{name:'Columns',exact:true}).click();
  await page.screenshot({path:evidence+'/'+(mobile?'mobile':'desktop')+'-'+theme+'-list.png',fullPage:true,animations:'disabled'});
  await page.locator('.obs-trace-list tbody tr td').filter({hasText:'Review report'}).click({position:{x:2,y:2}});const peek=page.getByRole('dialog',{name:'Trace details',exact:true});await peek.waitFor();
  await peek.locator('[data-json-mode]:not([disabled])').first().waitFor({state:'attached'});
  if(!await peek.getByRole('button',{name:'Previous trace',exact:true}).isDisabled()||!await peek.getByRole('button',{name:'Next trace',exact:true}).isDisabled())throw Error('single Trace neighbor navigation escaped list');
  await page.goto(url+'/developer/observability/root-public?obs=generation:g');
  const toggleTree=page.locator('[data-observation-toggle]').first(),child=page.locator('[data-observation-id="generation:g"]');
  await toggleTree.waitFor();await page.waitForFunction(()=>!document.querySelector('[data-observation-toggle]').disabled);
  if(await page.locator('[data-trace-program-revision]').count()&&await page.locator('iframe[src*="/api/developer/traces/program"]').count()!==1)throw Error('tree and timeline did not share one frozen program');
  if(await page.locator('.observation-node').count()!==2)throw Error('nonempty original tree missing');
  await toggleTree.click();await child.waitFor({state:'hidden'});
  const search=page.getByRole('searchbox',{name:'Search observations'});await search.fill('Model call');
  await child.waitFor({state:'visible'});await page.waitForFunction(()=>document.querySelector('[data-observation-toggle]').disabled);
  await search.fill('');await page.waitForFunction(()=>!document.querySelector('[data-observation-toggle]').disabled);await toggleTree.click();await child.waitFor({state:'visible'});
  const jsonButton=page.locator('[data-json-mode="json"]'),formattedButton=page.locator('[data-json-mode="formatted"]');
  await jsonButton.waitFor();await page.waitForFunction(()=>!document.querySelector('[data-json-mode="json"]').disabled);await jsonButton.click();
  if(await jsonButton.getAttribute('aria-pressed')!=='true'||await formattedButton.getAttribute('aria-pressed')!=='false')throw Error('JSON mode recipe not applied');
  const copyInput=page.locator('[data-copy-json="input"]');await copyInput.click();
  await page.waitForFunction(()=>document.querySelector('[data-copy-json="input"]').dataset.copyLabel==='Copied');
  if(!await page.evaluate(()=>window.__traceCopied.at(-1)===document.querySelector('[data-json-source="input"]').textContent))throw Error('copy changed original body bytes');
  await page.waitForFunction(()=>document.querySelector('[data-copy-json="input"]').getAttribute('aria-label')==='Copy input'&&!document.querySelector('[data-copy-json="input"]').dataset.copyLabel);
  await copyInput.click();await page.waitForFunction(()=>document.querySelector('[data-copy-json="input"]').dataset.copyLabel==='Copied');
  await page.evaluate(()=>{dispatchEvent(new PageTransitionEvent('pagehide'));dispatchEvent(new PageTransitionEvent('pageshow',{persisted:true}))});
  await page.waitForFunction(()=>!document.querySelector('[data-copy-json="input"]').disabled&&!document.querySelector('[data-copy-json="input"]').dataset.copyLabel&&document.querySelector('[data-copy-json="input"]').getAttribute('aria-label')==='Copy input');
  await page.locator('[data-observation-id="agent:root-public"] a.tree-heading').click();await page.waitForURL(target=>target.searchParams.get('obs')==='agent:root-public');
  await page.waitForFunction(()=>!document.querySelector('[data-json-mode="json"]').disabled&&document.querySelector('[data-json-mode="json"]').getAttribute('aria-pressed')==='true');
  await page.locator('[data-observation-id="generation:g"] a.tree-heading').click();await page.waitForURL(target=>target.searchParams.get('obs')==='generation:g');
  await page.waitForFunction(()=>!document.querySelector('[data-json-mode="json"]').disabled&&document.querySelector('[data-json-mode="json"]').getAttribute('aria-pressed')==='true');await formattedButton.click();
  await page.getByRole('link',{name:'Provider request',exact:true}).click();await page.waitForURL(target=>target.searchParams.get('tab')==='wire');
  const wire=page.locator('[data-wire-reader]'),reader=wire.locator('.obs-wire-reader');await reader.waitFor();
  if(await reader.locator('h3').textContent()!=='Request 1 · fixture-model'||!(await reader.innerText()).includes('USER')||!(await reader.textContent()).includes('fixture_tool'))throw Error('provider request recipe lost original sections');
  await page.screenshot({path:evidence+'/'+(mobile?'mobile':'desktop')+'-'+theme+'-wire.png',fullPage:true,animations:'disabled'});
  const savedWire=await wire.locator('[data-wire-source]').textContent();await page.locator('[data-json-mode="json"]').click();await wire.locator('[data-wire-source]').waitFor();await reader.waitFor({state:'hidden'});
  if(await wire.locator('[data-wire-source]').textContent()!==savedWire)throw Error('request mode changed original wire bytes');
  await page.locator('[data-json-mode="formatted"]').click();await reader.waitFor();await wire.locator('[data-wire-source]').waitFor({state:'hidden'});
  await page.goBack();await page.waitForURL(target=>target.searchParams.get('tab')!=='wire');await wire.waitFor({state:'detached'});await page.waitForFunction(()=>!document.querySelector('[data-json-mode="formatted"]').disabled);
  const timeline=page.locator('[data-split-view]'),axis=page.locator('.obs-timeline-axis');
  await page.waitForFunction(()=>document.querySelector('[data-split-view]').hasAttribute('data-time-window-start'));
  const original=await timeline.evaluate(node=>({start:Number(node.dataset.timeWindowStart),end:Number(node.dataset.timeWindowEnd)}));
  await page.locator('[data-timeline-action="in"]').click();
  await page.waitForFunction(span=>{const n=document.querySelector('[data-split-view]');return Number(n.dataset.timeWindowEnd)-Number(n.dataset.timeWindowStart)<span},original.end-original.start).catch(async error=>{await page.screenshot({path:evidence+'/failed-reader-return.png',fullPage:true});throw Error(String(error)+' '+JSON.stringify(await page.evaluate(()=>({frames:document.querySelectorAll('iframe').length,dataset:{...document.querySelector('[data-split-view]').dataset},buttons:[...document.querySelectorAll('[data-timeline-action]')].map(button=>({action:button.dataset.timelineAction,disabled:button.disabled})),mode:[...document.querySelectorAll('[data-json-mode]')].map(button=>({key:button.dataset.jsonMode,disabled:button.disabled})),active:document.activeElement?.outerHTML}))))});
  const zoomedStart=await timeline.getAttribute('data-time-window-start');await axis.focus();await page.keyboard.press('ArrowRight');
  await page.waitForFunction(start=>document.querySelector('[data-split-view]').dataset.timeWindowStart!==start,zoomedStart);
  await page.locator('[data-timeline-action="fit"]').click();
  await page.waitForFunction(span=>{const n=document.querySelector('[data-split-view]');return Number(n.dataset.timeWindowEnd)-Number(n.dataset.timeWindowStart)===span},original.end-original.start);
  await page.locator('[data-timeline-action="in"]').dblclick();
  await page.waitForFunction(span=>{const n=document.querySelector('[data-split-view]');return Number(n.dataset.timeWindowEnd)-Number(n.dataset.timeWindowStart)===span/4},original.end-original.start);
  await page.locator('[data-timeline-action="fit"]').click();await page.waitForFunction(span=>{const n=document.querySelector('[data-split-view]');return Number(n.dataset.timeWindowEnd)-Number(n.dataset.timeWindowStart)===span},original.end-original.start);
  const box=await axis.boundingBox();const drag=async()=>{await page.mouse.move(box.x+box.width*.2,box.y+box.height/2);await page.mouse.down();await page.mouse.move(box.x+box.width*.8,box.y+box.height/2,{steps:8})};
  await drag();await page.waitForFunction(()=>document.querySelectorAll('.obs-timeline-brush').length===2);if(await page.locator('.obs-timeline-brush').count()!==2)throw Error('time-lane brush missing');await page.keyboard.press('Escape');await page.mouse.up();
  if(await page.locator('.obs-timeline-brush').count())throw Error('cancelled brush remained');
  await drag();await page.mouse.up();await page.waitForFunction(span=>{const n=document.querySelector('[data-split-view]');return Number(n.dataset.timeWindowEnd)-Number(n.dataset.timeWindowStart)<span},original.end-original.start);
  await page.locator('[data-timeline-action="fit"]').click();await page.waitForFunction(span=>{const n=document.querySelector('[data-split-view]');return Number(n.dataset.timeWindowEnd)-Number(n.dataset.timeWindowStart)===span},original.end-original.start);await page.mouse.move(0,0);
  const selectedLink=page.locator('.obs-tree [aria-current=true]');await selectedLink.hover();const tip=page.getByRole('tooltip').filter({hasText:'Duration 800.00ms'});await tip.waitFor();
  if(await tip.textContent()!=='Model call\ncompleted\nStart +200.00ms\nDuration 800.00ms')throw Error('original timeline tooltip changed');await page.screenshot({path:evidence+'/'+(mobile?'mobile':'desktop')+'-'+theme+'-tooltip.png',fullPage:false,animations:'disabled'});await page.mouse.move(0,0);
  const geometry=await page.locator('.obs-tree-pane').evaluate(node=>({width:node.getBoundingClientRect().width,font:getComputedStyle(node).fontSize,rows:node.querySelectorAll('.observation-node').length}));
  await page.screenshot({path:evidence+'/'+(mobile?'mobile':'desktop')+'-'+theme+'-detail.png',fullPage:true,animations:'disabled'});
  if(errors.length)throw Error(errors.join('\n'));results.push({mobile,theme,geometry});await context.close();
 }
 const native=await browser.newContext({javaScriptEnabled:false});await native.request.post(url+'/session',{form:{token:'private-token-at-least-24-bytes',next:'/developer/observability'}});const page=await native.newPage();await page.goto(url+'/developer/observability/root-public?obs=generation:g');if(await page.locator('.observation-node').count()!==2||!(await page.locator('body').innerText()).includes('Accepted <script>unsafe()</script>'))throw Error('original no-script facts unavailable');await native.close();
 await writeFile(evidence+'/report.json',JSON.stringify({results,native:true},null,2));
}finally{await browser.close()}`
	if out, err := exec.CommandContext(ctx, "node", "--input-type=module", "-e", script, server.URL, evidence).CombinedOutput(); err != nil {
		t.Fatalf("trace browser: %v\n%s", err, out)
	}
}

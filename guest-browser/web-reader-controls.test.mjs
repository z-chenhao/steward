import assert from 'node:assert/strict';
import {before,after,test} from 'node:test';
import {fileURLToPath} from 'node:url';
import {chromium} from 'playwright-core';
import {build} from '../web/node_modules/esbuild/lib/main.js';

let browser,script;
before(async()=>{
 browser=await chromium.launch({headless:true,...(process.env.STEWARD_BROWSER_EXECUTABLE?{executablePath:process.env.STEWARD_BROWSER_EXECUTABLE}:{})});
 const paths=['local-time','components/ui/sheet','components/ui/split-view','workers','computer','live-updates','markdown'];
 script=(await build({stdin:{contents:paths.map(p=>'export * from "./src/'+p+'"').join('\n'),resolveDir:fileURLToPath(new URL('../web/',import.meta.url))},jsx:'automatic',bundle:true,format:'iife',globalName:'ui',write:false})).outputFiles[0].text;
});
after(async()=>{await browser?.close()});
async function open(t,body,route){
 const context=await browser.newContext({timezoneId:'America/New_York',viewport:{width:1400,height:900}});t.after(()=>context.close());
 const page=await context.newPage();page.setDefaultTimeout(8000);
 await page.route('http://reader.test/**',async request=>{
  const path=new URL(request.request().url()).pathname;
  if(path==='/style.css')return request.fulfill({path:fileURLToPath(new URL('../internal/interaction/web/assets/style-base.css',import.meta.url)),contentType:'text/css'});
  if(path==='/')return request.fulfill({contentType:'text/html',body:'<!doctype html><html lang="en"><head><link rel="stylesheet" href="/style.css"></head><body>'+body+'</body></html>'});
  if(await route?.(path,request))return;
  return request.fulfill({status:404});
 });
 await page.goto('http://reader.test');await page.addScriptTag({content:script});return page;
}
test('unchanged fallback-hour instant and keyboard pane geometry survive a round trip',async t=>{
 const page=await open(t,'<form><input name="since" type="datetime-local" step="1" data-local-input="2026-11-01T06:30:00Z"></form><div id="split" style="display:grid;width:1200px" data-split-storage="width"><div>Tree</div><div>Detail</div></div>');
 const result=await page.evaluate(()=>{
  ui.enhanceLocalTimes();const form=document.querySelector('form');const unchanged=new FormData(form).get('since');form.since.value='2026-11-01T01:31';const changed=new FormData(form).get('since');
  const key=(root,k)=>root.querySelector('[role=separator]').dispatchEvent(new KeyboardEvent('keydown',{key:k,bubbles:true}));
  const split=document.querySelector('#split');let dispose=ui.enhanceSplitView(split,'Tree');const widths=[];
  for(const k of ['Home','End']){
   key(split,k);const original=split.style.gridTemplateColumns;dispose();dispose=ui.enhanceSplitView(split,'Tree');widths.push([original,split.style.gridTemplateColumns]);
  }
  const sheet=ui.createSideSheet('Details',()=>{},{modal:false});sheet.open();const edge=()=>sheet.dialog.getBoundingClientRect().left;
  const before=edge();key(sheet.dialog,'ArrowRight');const right=edge();key(sheet.dialog,'ArrowLeft');return {unchanged,changed,widths,before,right,left:edge()};
 });
 assert.equal(result.unchanged,'2026-11-01T06:30:00.000Z');assert.equal(result.changed,'2026-11-01T05:31:00.000Z');
 assert.deepEqual(result.widths,[['200px 6px minmax(0px, 1fr)','200px 6px minmax(0px, 1fr)'],['954px 6px minmax(0px, 1fr)','954px 6px minmax(0px, 1fr)']]);
 assert.ok(result.right>result.before);assert.ok(Math.abs(result.left-result.before)<2);
});
test('computer and worker activity clear on denied access and reload after authentication',async t=>{
 let denied=false;
 const controls='<button data-panel-open="worker-panel">Workers</button><button data-panel-open="computer-panel">Computer</button><span data-worker-current></span><dialog id="worker-panel"><button data-panel-close>Close workers</button><div data-worker-status></div><div data-worker-list></div></dialog><dialog id="computer-panel"><button data-panel-close>Close computer</button><div data-computer-screen></div><div data-computer-placeholder></div><div data-computer-status></div><div data-computer-caption></div><ol data-computer-activity></ol></dialog>';
 const worker={id:'one',title:'Bound worker',role:'child',status:'running',created_at:'2026-01-01T00:00:00Z'};
 const page=await open(t,controls,async(path,req)=>{
  if(!path.startsWith('/api/'))return;
  if(denied){await req.fulfill({status:path.includes('computer')?403:401});return true}
  if(path.includes('workers'))await req.fulfill({json:{items:[worker],more:false}});
  else if(path.includes('status'))await req.fulfill({json:{phase:'ready',activity:[{id:'1',action:'click',status:'done',at:worker.created_at}]}});
  else await req.fulfill({contentType:'image/png',body:Buffer.from('iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mP8/x8AAusB9WlOHYQAAAAASUVORK5CYII=','base64')});
  return true;
 });
 await page.evaluate(()=>{ui.enhanceWorkers();ui.enhanceComputerView()});
 await page.getByRole('button',{name:'Workers'}).click();await page.locator('.worker-row').waitFor();
 denied=true;await page.getByText('Sign in to view Worker activity.',{exact:true}).waitFor();assert.equal(await page.locator('.worker-row').count(),0);
 await page.getByText('Close workers').click();denied=false;await page.getByRole('button',{name:'Workers'}).click();await page.locator('.worker-row').waitFor();assert.match(await page.locator('.worker-row').innerText(),/Bound worker/);
 await page.getByRole('button',{name:'Computer',exact:true}).click();await page.locator('[data-computer-screen] img').waitFor();assert.equal(await page.locator('[data-computer-activity] strong').textContent(),'Click');
 denied=true;await page.locator('[data-computer-status]').getByText('Sign in to view Computer.').waitFor();
 assert.equal(await page.locator('[data-computer-screen] img,[data-computer-activity] li').count(),0);assert.equal(await page.locator('[data-computer-caption]').innerText(),'Read only');
});
test('a burst of streamed Markdown paints once with complete final content',async t=>{
 const page=await open(t,'<main data-live-view="/"><article id="conversation-draft" hidden><div data-draft-text></div></article></main>',async(path,req)=>{if(path==='/api/updates'){await req.fulfill({status:304});return true}});
 const metrics=await page.evaluate(async()=>{
  window.EventSource=class extends EventTarget{constructor(){super();window.stream=this}close(){}};
  ui.startLiveUpdates().start();
  const chunks=Array.from({length:96},(_,i)=>'**row '+i+'**\n\n');const target=document.querySelector('[data-draft-text]');
  let parses=0;const create=document.createElement.bind(document);document.createElement=(name,...args)=>{if(name==='template')parses++;return create(name,...args)};
  const start=performance.now();for(const text of chunks)stream.dispatchEvent(new MessageEvent('reply',{data:JSON.stringify({key:'a'.repeat(32),state:'draft',text})}));
  const dispatchMs=performance.now()-start;await new Promise(requestAnimationFrame);
  const paints=parses;document.createElement=create;const expected=document.createElement('div');ui.renderDraftMarkdown(expected,chunks.join(''));
  return {paints,dispatchMs,identical:target.innerHTML===expected.innerHTML};
 });
 t.diagnostic('96 chunks, '+metrics.paints+' parses, synchronous dispatch '+metrics.dispatchMs.toFixed(2)+' ms');
 assert.equal(metrics.identical,true);assert.equal(metrics.paints,1);
});
test('Mermaid retries a failed loader, exposes its description and keeps its source readable',async t=>{
 let reads=0;
 const page=await open(t,'<div id="one"><pre><code class="language-mermaid">flowchart LR; A--&gt;B</code></pre></div><div id="two"><pre><code class="language-mermaid">flowchart LR; B--&gt;C</code></pre></div>',async(path,req)=>{
  if(path!=='/diagrams.js')return;
  if(++reads===1)await req.abort();else await req.fulfill({contentType:'text/javascript',body:`window.stewardDiagram=async()=>'<svg xmlns="http://www.w3.org/2000/svg" width="50" height="20"><title>Flow</title><desc>B to C</desc><text y="15">B → C</text></svg>'`});return true;
 });
 await page.evaluate(()=>ui.enhanceDiagrams(document.querySelector('#one')));await page.locator('#one pre[aria-label]').waitFor();
 await page.evaluate(()=>ui.enhanceDiagrams(document.querySelector('#two')));await page.getByRole('img',{name:'Flow · B to C'}).waitFor();
 await page.locator('#two summary').focus();await page.keyboard.press('Enter');assert.equal(await page.locator('#two code').isVisible(),true);assert.equal(reads,2);
});

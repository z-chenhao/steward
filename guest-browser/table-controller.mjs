import assert from 'node:assert/strict';
import {mkdir,readFile,writeFile} from 'node:fs/promises';
import {chromium} from 'playwright-core';
import {waitForUIState} from './ui-state.mjs';
const [url,out]=process.argv.slice(2);
const original=JSON.parse(await readFile(new URL('../internal/plugin/intelligentui/testdata/original-table.json',import.meta.url),'utf8'));
await mkdir(out,{recursive:true});
const browser=await chromium.launch({headless:true,...(process.env.STEWARD_BROWSER_EXECUTABLE?{executablePath:process.env.STEWARD_BROWSER_EXECUTABLE}:{})});
try{
 const styles=[];
 const context=await browser.newContext({locale:'en-US',acceptDownloads:true});
 await context.request.post(url+'/session',{form:{token:'private-token-at-least-24-bytes'},maxRedirects:0});
 const page=await context.newPage();page.setDefaultTimeout(5000);await page.setViewportSize({width:1400,height:900});
 await page.goto(url+'/fixture');
 const frame=page.frameLocator('iframe[title="Table view"]');
 const surface=page.locator('dialog.answer-surface').filter({has:page.locator('iframe[title="Table view"]')});
 const menu=async label=>{await surface.getByRole('button',{name:'Options',exact:true}).click();await surface.getByRole('menuitem',{name:label,exact:true}).click()};
 await frame.getByRole('button',{name:'Amount',exact:true}).waitFor();
 // Menu remains disabled until the actual restore handshake finishes.
 await surface.getByRole('button',{name:'Options',exact:true}).click();await surface.getByRole('menuitem',{name:'Reset',exact:true}).waitFor();
 await surface.getByRole('menuitem',{name:'Reset',exact:true}).click();
 await frame.getByRole('button',{name:'Amount',exact:true}).click();
 await waitForUIState(page,'ui:original#0',s=>s.state.sort===1&&s.state.direction===1&&s.model_context.sort==='Frozen Amount'&&s.model_context.title==='Frozen Table title');
 assert.deepEqual(await frame.locator('tbody tr').allTextContents(),original.stages.find(s=>s.name==='amount-ascending').rows);
 await frame.getByRole('button',{name:'Amount',exact:true}).click();
 await waitForUIState(page,'ui:original#0',s=>s.state.sort===1&&s.state.direction===-1);
 await menu('Filter');await surface.getByRole('searchbox',{name:'Filter rows'}).fill('same');
 await waitForUIState(page,'ui:original#0',s=>s.state.query==='same'&&s.state.sort===1);
 await page.waitForFunction(()=>document.querySelector('.openintelligentui-native:not([data-view-ref]) tbody')?.rows.length===2);
 await frame.locator('tbody tr').nth(1).waitFor();
 const download=page.waitForEvent('download');await menu('Download CSV');const file=await download;
 const bytes=await readFile(await file.path());
 // Independently captured original CSV includes BOM and original visible order.
 assert.deepEqual(bytes,Buffer.from(original.csv.filtered,'base64'));
 assert.equal(file.suggestedFilename(),original.props.title+'.csv');
 await frame.getByRole('button',{name:'Alpha',exact:true}).click();
 await waitForUIState(page,'ui:original#0',s=>s.state.selected==='Alpha');
 await page.waitForFunction(()=>document.querySelector('.openintelligentui-native:not([data-view-ref]) tbody')?.rows.length===1);
 await page.evaluate(()=>window.flushTable());
 const before=await frame.locator('.answer-table').evaluate(n=>n.outerHTML);
 const saved=await page.evaluate(async()=>await(await fetch('/api/ui/state?ref=ui:original%230')).json());
 await page.reload();await frame.locator('tbody tr').first().waitFor();
 await frame.getByRole('button',{name:'Alpha',exact:true}).waitFor();
 assert.equal(await frame.locator('.answer-table').evaluate(n=>n.outerHTML),before,'first recovered DOM without select/repair');
 const recovered=await page.evaluate(async()=>await(await fetch('/api/ui/state?ref=ui:original%230')).json());assert.deepEqual(recovered,saved,'recovery preserves original CAS/version');
 for(const [label,mode]of[['Expand','fullscreen'],['Open dialog','modal'],['Keep visible','pip']]){
  await menu(label);await page.waitForFunction(mode=>document.querySelector('dialog:has(iframe)')?.dataset.mode===mode,mode);
  await frame.locator('body').evaluate(()=>dispatchEvent(new KeyboardEvent('keydown',{key:'Escape',bubbles:true})));
  assert.equal(await surface.getAttribute('data-mode'),mode,'synthetic Escape refused');
  await frame.getByRole('button',{name:'Alpha',exact:true}).focus();await page.keyboard.press('Escape');
  await page.waitForFunction(()=>document.querySelector('dialog:has(iframe)')?.dataset.mode==='inline');
 }
 await menu('Reset');await waitForUIState(page,'ui:original#0',s=>s.state.sort===-1&&s.state.direction===1&&s.state.query===''&&s.state.selected==='');
 await frame.locator('tbody tr').nth(4).waitFor();
 for(const width of[1400,390]){await page.setViewportSize({width,height:900});for(const theme of['light','dark']){
  await page.evaluate(theme=>document.documentElement.classList.toggle('dark',theme==='dark'),theme);
  await frame.locator('html').evaluate((node,theme)=>new Promise(resolve=>{const check=()=>node.dataset.widgetTheme===theme?resolve():requestAnimationFrame(check);check()}),theme);
  await page.mouse.move(0,0);
  const finish=async node=>{await new Promise(resolve=>requestAnimationFrame(()=>requestAnimationFrame(resolve)));await Promise.all(node.getAnimations({subtree:true}).filter(a=>Number.isFinite(a.effect.getTiming().iterations)).map(a=>a.finished.catch(()=>{})))};
  await frame.locator('body').evaluate(finish);await page.locator('#projection').evaluate(finish);
  const properties=['font-family','font-size','font-weight','line-height','text-align','color','background-color','border-top-width','border-bottom-width','padding-top','padding-right','padding-bottom','padding-left','min-height','display','gap'];
  const measure=(nodes,properties)=>nodes.map(node=>{const style=getComputedStyle(node);return{tag:node.tagName,text:node.textContent,styles:Object.fromEntries(properties.map(key=>[key,style.getPropertyValue(key)]))}});
  const native=await page.locator('.openintelligentui-native:not([data-view-ref]) .answer-table :is(caption,th,td,button,.answer-table-source)').evaluateAll(measure,properties);
  const managed=await frame.locator('.answer-table :is(caption,th,td,button,.answer-table-source)').evaluateAll(measure,properties);
  assert.ok(native.length>20);assert.deepEqual(managed,native,'original computed nonempty styles '+width+' '+theme);
  styles.push({width,theme,managed,native});
  await page.screenshot({path:out+'/controller-'+theme+'-'+width+'.png'});
 }}
 // An unsolicited frame export cannot initiate a fetch/download.
 let extraDownloads=0;page.on('download',()=>extraDownloads++);
 await frame.locator('body').evaluate(()=>parent.postMessage({channel:'steward-table-view',type:'export-rows',id:'not-requested',rows:[0]},'*'));
 await page.evaluate(()=>window.flushTable());assert.equal(extraDownloads,0);
 await page.route('**/ui/table?**',route=>new URL(route.request().url()).searchParams.get('format')==='export'?route.fulfill({status:404,body:'revoked'}):route.continue());
 await menu('Download CSV');await surface.getByRole('alert').filter({hasText:'Export failed.'}).waitFor();assert.equal(extraDownloads,0,'failed fresh authorization cannot download');
 await page.unroute('**/ui/table?**');
 const retained=await page.evaluate(async()=>await(await fetch('/api/ui/state?ref=ui:original%230')).json());
 // Refuse malformed frozen descriptions, rather than constructing model prose.
 await page.route('**/ui/table?**',route=>new URL(route.request().url()).searchParams.get('format')==='context'?route.fulfill({contentType:'application/json',body:'[]'}):route.continue());
 await page.reload();await page.getByText('Original Table fallback',{exact:true}).waitFor();assert.equal(await page.locator('iframe[title="Table view"]').count(),0);
 assert.deepEqual(await page.evaluate(async()=>await(await fetch('/api/ui/state?ref=ui:original%230')).json()),retained);
 await page.unroute('**/ui/table?**');
 await page.route('**/api/ui/state?**',route=>new URL(route.request().url()).searchParams.get('ref')==='ui:original#0'?route.fulfill({status:503,body:'unavailable'}):route.continue());
 await page.reload();await page.getByText('Original Table fallback',{exact:true}).waitFor();assert.equal(await page.locator('iframe[title="Table view"]').count(),0,'failed state load cannot restore defaults');
 await page.unroute('**/api/ui/state?**');
 await page.route('**/ui/table?**',route=>route.fulfill({status:404,body:'unavailable'}));await page.reload();
 await page.getByText('Original Table fallback',{exact:true}).waitFor({timeout:12000});
 assert.equal(await page.locator('iframe[title="Table view"]').count(),0,'managed failure is semantic fallback');
 await writeFile(out+'/styles.json',JSON.stringify(styles,null,2));
 await writeFile(out+'/report.json',JSON.stringify({scope:'real projection/controller/HTTP/CAS/Worker with accepted-domain fixtures; not full App',frozenDescriptions:true,groupFiltersNativeTable:true,originalCSV:true,originalDOMAndCASRecovered:true,trustedEscape:true,syntheticEscapeRefused:true,reset:true,missingManagedViewSemanticOnly:true,computedStylesFourModes:true,unsolicitedExportIgnored:true,freshExportAuthorizationFailureRefused:true,invalidFrozenContextsRefused:true,failedStateReadRefused:true},null,2));
 await context.close();
}finally{await browser.close()}

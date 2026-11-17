import assert from 'node:assert/strict';
import {mkdir,readFile,writeFile} from 'node:fs/promises';
import {chromium} from 'playwright-core';
const [url,out]=process.argv.slice(2);
const original=JSON.parse(await readFile(new URL('../internal/plugin/intelligentui/testdata/original-table.json',import.meta.url),'utf8'));
await mkdir(out,{recursive:true});
const browser=await chromium.launch({headless:true,...(process.env.STEWARD_BROWSER_EXECUTABLE?{executablePath:process.env.STEWARD_BROWSER_EXECUTABLE}:{})});
try{
 const context=await browser.newContext({locale:'en-US'});await context.request.post(url+'/session',{form:{token:'private-token-at-least-24-bytes'},maxRedirects:0});
 const page=await context.newPage();page.setDefaultTimeout(5000);const requests=[];
 page.on('request',request=>requests.push(request.url()));
 await page.addInitScript(()=>{window.previewMessages=[];addEventListener('message',event=>{if(event.origin==='null')previewMessages.push(event.data)})});
 await page.goto(url+'/fixture');const frame=page.frameLocator('iframe[title="Table preview"]');
 await frame.locator('th[data-numeric=true]').waitFor();
 assert.deepEqual(await frame.locator('tbody tr').allTextContents(),original.stages[0].rows);
 await page.waitForFunction(()=>Number.parseInt(document.querySelector('iframe')?.style.height)>50);
 const source=await page.locator('iframe').getAttribute('src');
 const isolation=await frame.locator('body').evaluate(body=>({inert:body.inert,origin:self.origin,scripts:document.scripts.length,images:document.images.length,links:document.querySelectorAll('a[href]').length}));
 assert.deepEqual(isolation,{inert:true,origin:'null',scripts:1,images:0,links:0});
 const before=await frame.locator('.answer-table').evaluate(node=>node.outerHTML);
 await page.locator('iframe').evaluate(node=>{for(const channel of['steward-table-view','steward-plan-view'])for(const value of[{type:'restore',state:{sort:1,direction:-1},query:'same',selected:'Alpha'},{type:'reset'},{type:'capture',id:'forged-capture'},{type:'export',id:'forged-export'},{type:'group',query:'same',selected:'Alpha'}])node.contentWindow.postMessage({channel,...value},'*')});
 await frame.locator('body').evaluate(()=>{document.querySelector('th button').click();document.querySelector('td button').click()});
 await frame.locator('body').evaluate(()=>new Promise(resolve=>requestAnimationFrame(()=>requestAnimationFrame(resolve))));
 assert.equal(await frame.locator('.answer-table').evaluate(node=>node.outerHTML),before,'readonly commands and synthetic clicks cannot change the view');
 assert.equal(await page.evaluate(()=>previewMessages.some(value=>value.channel!=='steward-plan-view'||value.type!=='height')),false,'no state, selection or export bridge');
 for(const width of[1400,390]){await page.setViewportSize({width,height:900});for(const theme of['light','dark']){
  await page.evaluate(theme=>document.documentElement.classList.toggle('dark',theme==='dark'),theme);
  await frame.locator('html').evaluate((node,theme)=>new Promise(resolve=>{const check=()=>node.dataset.widgetTheme===theme?resolve():requestAnimationFrame(check);check()}),theme);
  await frame.locator('body').evaluate(async node=>{await document.fonts.ready;await Promise.all(node.getAnimations({subtree:true}).filter(a=>Number.isFinite(a.effect.getTiming().iterations)).map(a=>a.finished.catch(()=>{})))});
  const cells=await frame.locator('th,td').evaluateAll(nodes=>nodes.map(node=>({text:node.textContent,numeric:node.dataset.numeric,font:getComputedStyle(node).fontFamily,size:getComputedStyle(node).fontSize,lineHeight:getComputedStyle(node).lineHeight,align:getComputedStyle(node).textAlign,color:getComputedStyle(node).color})));
  const expected=original.styles.find(style=>style.width===width&&style.theme===theme);assert.ok(expected);assert.deepEqual(cells,expected.cells,'independent original preview cells '+width+' '+theme);
  await page.screenshot({path:out+'/preview-'+theme+'-'+width+'.png'});
 }}
 await page.evaluate(async()=>window.preview.receive(await(await fetch('/fixture-update')).json()));
 await page.waitForFunction(old=>document.querySelector('iframe')?.getAttribute('src')!==old,source);await frame.locator('th[data-numeric=true]').waitFor();
 assert.equal((await context.request.get(url+source)).status(),404,'stale revision denied');
 const current=await page.locator('iframe').getAttribute('src');
 await page.evaluate(async()=>window.preview.receive(await(await fetch('/fixture-discard')).json()));assert.equal(await page.locator('iframe').count(),0);
 assert.equal((await context.request.get(url+current)).status(),404,'discard cannot resurrect view');
 assert.equal(requests.some(request=>request.includes('/api/ui/state')||request.includes('/api/ui/action')||request.includes('/api/message')||request.includes('format=export')),false,'draft has no delivered grants');
 await writeFile(out+'/report.json',JSON.stringify({scope:'real createUIPreview/React/protected HTTP/driver/owning Worker; accepted-domain fixture, not full App',isolation,originalContentAndNumericColumns:true,independentComputedCellsFourModes:true,readonlyCommandsAndClicksRefused:true,onlyHeightBridge:true,noDeliveryGrants:true,staleRevisionDenied:true,discardUnmountsAndDenies:true},null,2));
 await context.close();
}finally{await browser.close()}

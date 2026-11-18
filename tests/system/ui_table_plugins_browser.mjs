import assert from 'node:assert/strict';
import {mkdir,readFile,writeFile} from 'node:fs/promises';
import {chromium} from '../../guest-browser/node_modules/playwright-core/index.mjs';
import {waitForUIState} from '../../guest-browser/ui-state.mjs';
const[url,out,phase,ref='',caption='Original table evidence']=process.argv.slice(2);await mkdir(out,{recursive:true});
const original=JSON.parse(await readFile(new URL('../../internal/plugin/intelligentui/testdata/original-table.json',import.meta.url),'utf8'));
const browser=await chromium.launch({headless:true,...(process.env.STEWARD_BROWSER_EXECUTABLE?{executablePath:process.env.STEWARD_BROWSER_EXECUTABLE}:{})});
let page;
try{
 const context=await browser.newContext({locale:'en-US',viewport:{width:1400,height:980},reducedMotion:'reduce',acceptDownloads:true});
 page=await context.newPage();page.setDefaultTimeout(12000);
 if(phase==='restart')await page.route('**/api/ui/state?**',async route=>{if(new URL(route.request().url()).searchParams.get('ref')===ref.replace(/#1$/,'#0'))await new Promise(resolve=>setTimeout(resolve,500));await route.continue()});
 await page.goto(url);
 await page.getByLabel('Workspace access token',{exact:true}).fill(process.env.STEWARD_WEB_TEST_TOKEN);await page.getByRole('button',{name:'Open workspace'}).click();
 // Read the retained component as a user before asynchronous frames resize.
 await page.waitForFunction(()=>document.querySelector('.composer textarea')?.classList.contains('resize-none'));
 if(!phase.includes('preview')){await page.mouse.move(4,200);await page.mouse.wheel(0,-100)}
 const previewMode=phase.endsWith('preview'),literal=phase==='literal-preview';
 const selector=previewMode?'iframe[title="Table preview"]':'iframe[title="Table view"][src*="'+encodeURIComponent(ref)+'"]';
 const frame=page.frameLocator(selector);await frame.locator(literal?'th[data-numeric=false]':'th[data-numeric=true]').waitFor();const detectedAt=Date.now();
 assert.ok((await frame.locator('caption').innerText()).startsWith(caption),'original owning caption');
 const isolation=await frame.locator('body').evaluate(body=>({origin:self.origin,inert:body.inert,scripts:document.scripts.length,images:document.images.length}));
 assert.deepEqual(isolation,{origin:'null',inert:previewMode,scripts:1,images:0});
 const source=await page.locator(selector).getAttribute('src');
 if(previewMode){
  assert.equal(new URL(source,url).searchParams.get('component'),'1');assert.deepEqual(await frame.locator('tbody tr').allTextContents(),literal?Array.from({length:30},()=>"'".repeat(2000)):original.stages[0].rows);
  await page.screenshot({path:out+'/preview.png'});await writeFile(out+'/report.json',JSON.stringify({phase,source,detectedAt,isolation,actualApp:true,originalOrdinal:1,originalRows:true,literalBudgetPreserved:literal},null,2));
 }else{
  const groupRef=ref.replace(/#1$/,'#0'),outer=page.locator('section[data-view-ref='+JSON.stringify(ref)+']');
  const openMenu=async label=>{
   await outer.getByRole('button',{name:'Options',exact:true}).click();
   return outer.getByRole('menuitem',{name:label,exact:true});
  };
  const downloadCSV=async()=>{
   const item=await openMenu('Download CSV');
   await item.click({trial:true});
   const [file]=await Promise.all([page.waitForEvent('download'),item.click()]);
   return readFile(await file.path());
  };
  if(phase.startsWith('formal')){
   assert.deepEqual(await downloadCSV(),Buffer.from(original.csv.all,'base64'),'original complete CSV retains formula shielding, signed numbers, Unicode and quotes');
   await frame.getByRole('button',{name:'Amount',exact:true}).click();await frame.locator('th[aria-sort=ascending]').waitFor();
   await frame.getByRole('button',{name:'Amount',exact:true}).click();await frame.locator('th[aria-sort=descending]').waitFor();
   await (await openMenu('Filter')).click();await outer.getByRole('searchbox',{name:'Filter rows'}).fill('same');
   await waitForUIState(page,groupRef,state=>state.state.query==='same');
   await frame.locator('tbody').evaluate(node=>new Promise((resolve,reject)=>{const deadline=performance.now()+5000;const check=()=>node.rows.length===2?resolve():performance.now()>deadline?reject(Error('original query did not filter Table')):setTimeout(check,25);check()}));
   assert.deepEqual(await downloadCSV(),Buffer.from(original.csv.filtered,'base64'),'original authorized CSV bytes');
   await frame.getByRole('button',{name:'Alpha',exact:true}).click();
   await waitForUIState(page,groupRef,state=>state.state.query==='same'&&state.state.selected==='Alpha');
  }
  await frame.locator('button.answer-cell-select:not([disabled])').first().waitFor();
  await frame.getByRole('button',{name:'Alpha',exact:true}).waitFor();
  await frame.locator('tbody').evaluate(node=>new Promise((resolve,reject)=>{const deadline=performance.now()+5000;const check=()=>node.rows.length===1?resolve():performance.now()>deadline?reject(Error('original selection not restored')):setTimeout(check,25);check()}));
  const saved=await waitForUIState(page,ref,state=>state.state.sort===1&&state.state.direction===-1&&state.model_context.sort==='Amount'&&state.model_context.title===original.props.title);
  const group=await waitForUIState(page,groupRef,state=>state.state.query==='same'&&state.state.selected==='Alpha');
  const dom=await frame.locator('.answer-table').evaluate(node=>node.outerHTML);
  const reply=await context.request.get(url+'/ui/table?'+new URLSearchParams({ref,format:'export',rows:'1'}));assert.equal(reply.status(),200);assert.deepEqual((await reply.json()).rows,[original.props.rows[1]]);
  for(const width of[1400,390])for(const theme of['light','dark']){
   await page.setViewportSize({width,height:980});await page.evaluate(theme=>document.documentElement.classList.toggle('dark',theme==='dark'),theme);
   await frame.locator('html[data-widget-theme='+theme+']').waitFor();await outer.scrollIntoViewIfNeeded();await page.screenshot({path:out+'/table-'+theme+'-'+width+'.png'});
  }
  await writeFile(out+'/report.json',JSON.stringify({phase,source,detectedAt,isolation,actualApp:true,originalOrdinal:1,states:{table:saved,group},dom,authorizedOriginalExport:true,originalCSV:phase.startsWith('formal'),completeCSVWithFormulaShielding:phase.startsWith('formal'),readOnlyRecovery:!phase.startsWith('formal')},null,2));
 }
 await context.close();
}catch(error){if(page){await page.screenshot({path:out+'/failure.png'});await writeFile(out+'/failure.json',JSON.stringify(await page.evaluate(()=>({frames:[...document.querySelectorAll('iframe')].map(frame=>frame.src),body:document.body.innerText.slice(-4000)})),null,2))}throw error}
finally{await browser.close()}

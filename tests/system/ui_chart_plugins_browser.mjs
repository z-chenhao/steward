import assert from 'node:assert/strict';
import {mkdir,readFile,writeFile} from 'node:fs/promises';
import {chromium} from '../../guest-browser/node_modules/playwright-core/index.mjs';
import {waitForUIState} from '../../guest-browser/ui-state.mjs';
const[url,out,phase,ref='',caption='Trend']=process.argv.slice(2);await mkdir(out,{recursive:true});
const browser=await chromium.launch({headless:true,...(process.env.STEWARD_BROWSER_EXECUTABLE?{executablePath:process.env.STEWARD_BROWSER_EXECUTABLE}:{})});
let page;
try{
 const context=await browser.newContext({locale:'en-US',viewport:{width:1400,height:980},reducedMotion:'reduce',acceptDownloads:true});
 page=await context.newPage();page.setDefaultTimeout(12000);await page.goto(url);
 await page.getByLabel('Workspace access token',{exact:true}).fill(process.env.STEWARD_WEB_TEST_TOKEN);await page.getByRole('button',{name:'Open workspace'}).click();
 // Read the retained component as a user before asynchronous frames resize.
 await page.waitForFunction(()=>document.querySelector('.composer textarea')?.classList.contains('resize-none'));
 if(!phase.includes('preview')){await page.mouse.move(4,200);await page.mouse.wheel(0,-100)}
 const preview=phase.endsWith('preview'),variant=caption.startsWith('Chart v124:');
 const chart=preview?page.locator('section[data-openintelligentui="barChart"][inert]'):page.locator('section[data-openintelligentui="barChart"][data-view-ref='+JSON.stringify(ref)+']');
 await chart.locator('.recharts-bar-rectangle').nth(4).waitFor();await chart.locator('.native-answer-title').getByText(caption,{exact:true}).waitFor();const detectedAt=Date.now();
 // The owning version changes real axis geometry for five short categories.
 if(variant)await chart.locator('svg text[data-axis-category]').first().waitFor();else await chart.locator('svg text').filter({hasText:/^A$/}).first().waitFor();
 const horizontal=await chart.locator('svg text').evaluateAll(nodes=>{const box=label=>nodes.find(node=>(node.querySelector('title')?.textContent??node.textContent)===label)?.getBoundingClientRect();const a=box('A'),e=box('E');if(!a||!e)throw Error('original category axes missing');return Math.abs(a.y-e.y)>Math.abs(a.x-e.x)});assert.equal(horizontal,variant,'actual frozen category axis orientation');
 const policy=chart.locator('iframe[title="Chart policy"]');assert.equal(await policy.count(),1);
 const source=await policy.getAttribute('src');
 if(preview){
  assert.ok(source.startsWith('/ui/chart-preview?'));assert.equal(new URL(source,url).searchParams.get('component'),'1');assert.equal(await chart.evaluate(node=>node.inert),true);await chart.getByRole('button').first().click({force:true});assert.equal(await chart.locator('.answer-surface').getAttribute('data-mode'),'inline');
  await chart.screenshot({path:out+'/preview.png'});await writeFile(out+'/report.json',JSON.stringify({phase,detectedAt,source,actualApp:true,inert:true,variant,originalOrdinal:1},null,2));
 }else{
  assert.ok(source.startsWith('/ui/chart?'));
  const menu=async name=>{await chart.getByRole('button',{name:'Options',exact:true}).click();await page.getByRole('menuitem',{name,exact:true}).click()};
  if(phase.startsWith('formal')){
   await menu('Zoom');await chart.locator('input[type=range]').nth(1).fill('3');await chart.locator('.recharts-bar-rectangle').nth(4).waitFor({state:'detached'});
   await waitForUIState(page,ref,state=>state.state.end===3&&state.model_context.title===caption);
   const pending=page.waitForEvent('download');await menu('Download CSV');const file=await pending;
   assert.equal((await readFile(await file.path())).toString(),'\ufeff"Label","Value"\r\n"A","1"\r\n"B","2"\r\n"C","3"\r\n"D","4"');
   await page.reload();await chart.locator('.recharts-bar-rectangle').nth(4).waitFor();
  }
  assert.equal(await chart.locator('input[type=range]').count(),0,'zoom is local after reload');
  const states=await waitForUIState(page,ref,state=>state.state.end===3&&state.model_context.title===caption);
  const reply=await context.request.get(url+'/ui/chart?'+new URLSearchParams({ref,format:'export',rows:'4,0'}));assert.equal(reply.status(),200);assert.deepEqual((await reply.json()).rows,[['E',5],['A',1]]);
  await menu('Expand');assert.equal(await chart.locator('.answer-surface').getAttribute('data-mode'),'fullscreen');
  for(const width of[1400,390])for(const theme of['light','dark']){
   await page.setViewportSize({width,height:980});await page.evaluate(theme=>document.documentElement.classList.toggle('dark',theme==='dark'),theme);
   await chart.locator('.recharts-surface').evaluate(()=>new Promise(resolve=>requestAnimationFrame(()=>requestAnimationFrame(resolve))));await chart.locator('.answer-surface').screenshot({path:out+'/chart-'+width+'-'+theme+'.png'});
  }
  await page.keyboard.press('Escape');assert.equal(await chart.locator('.answer-surface').getAttribute('data-mode'),'inline');
  await writeFile(out+'/report.json',JSON.stringify({phase,detectedAt,source,actualApp:true,variant,states,originalOrdinal:1,owningLayout:true,authorizedOriginalExport:true},null,2));
 }
 await context.close();
}catch(error){if(page){await page.screenshot({path:out+'/failure.png'});await writeFile(out+'/failure.txt',await page.locator('body').innerText())}throw error}finally{await browser.close()}

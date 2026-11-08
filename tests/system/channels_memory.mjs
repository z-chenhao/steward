import {assertDataList} from './channels_controls.mjs';
import {writeFile} from 'node:fs/promises';
export async function memoryInspection({page,context,browser,url,evidence}) {
 await page.getByRole('navigation',{name:'Workspace'}).getByRole('link',{name:'Developer',exact:true}).click();
 await page.getByRole('navigation',{name:'Developer sections'}).getByRole('link',{name:'Memory',exact:true}).click();
 await page.locator('.memory-page').waitFor();
 await page.getByRole('button',{name:'Apply filters',exact:true}).waitFor({state:'hidden'});
 for(const layer of ['transcripts','notes','representations','injected']){
  await page.goto(url+'/developer/memory?layer='+layer);
  await page.locator('#memory-page[data-memory-ready]').waitFor();
  if(await page.locator('[data-memory-ref]').count()<1)throw Error('Empty memory layer '+layer);
  await page.locator('a.memory-open').first().click();
  const layerSheet=page.getByRole('dialog',{name:'Memory details',exact:true});
  await layerSheet.waitFor();
  if(!await layerSheet.locator('[data-memory-content]').count())throw Error('Missing memory layer detail '+layer);
  await page.setViewportSize({width:1440,height:1000});
  const body=await layerSheet.locator('.control-sheet-body').boundingBox(),pane=await layerSheet.locator('.memory-workbench').boundingBox();
  if(Math.abs(body.height-pane.height)>2)throw Error('Memory split panes do not fill the sheet height');
  await page.screenshot({path:evidence+'/memory-layer-'+layer+'-light.png',fullPage:true});
  await page.evaluate(()=>document.documentElement.classList.add('dark'));
  await page.screenshot({path:evidence+'/memory-layer-'+layer+'-dark.png',fullPage:true});
  await page.setViewportSize({width:390,height:844});
  if(await page.evaluate(()=>document.documentElement.scrollWidth>innerWidth))throw Error('Layer overflow '+layer);
  await page.screenshot({path:evidence+'/memory-layer-'+layer+'-mobile.png'});
  if(layer==='injected'){
   await layerSheet.getByRole('link',{name:'Research wiki',exact:true}).click();
   await layerSheet.getByRole('heading',{name:'Research wiki',exact:true}).waitFor();
   if(await page.getByRole('dialog',{name:'Memory details',exact:true}).count()!==1)throw Error('Related memory opened a second sheet');
  }

  await page.evaluate(()=>document.documentElement.classList.remove('dark'));
 }
 await page.setViewportSize({width:1440,height:1000});
 await page.goto(url+'/developer/memory');
 await page.locator('#memory-page[data-memory-ready]').waitFor();

 await page.getByRole('radio',{name:'Recall',exact:true}).check();await page.waitForURL(target=>target.searchParams.get('tier')==='recall');
 if(await page.locator('[data-memory-ref]').count()<1||await page.locator('[data-memory-ref] td:nth-child(2)').allTextContents().then(values=>values.some(value=>value.trim()!=='Memory notes')))throw Error('Memory filter did not apply automatically');
 await page.getByRole('radio',{name:'All tiers',exact:true}).check();await page.waitForURL(target=>!target.searchParams.get('tier'));

 if(await page.getByRole('link',{name:'Database',exact:true}).count())throw Error('Database navigation remains');
 const first=await page.locator('[data-memory-ref]').evaluateAll(rows=>rows.map(row=>row.dataset.memoryRef));
 if(first.length!==20)throw Error('Memory default page is not bounded');
 await page.getByRole('link',{name:'Older records →',exact:true}).click();
 const second=await page.locator('[data-memory-ref]').evaluateAll(rows=>rows.map(row=>row.dataset.memoryRef));
 if(!second.length||second.some(ref=>first.includes(ref)))throw Error('Memory cursor repeated records');
 await page.goto(url+'/developer/memory');
 await page.evaluate(()=>document.fonts.ready);
 await page.setViewportSize({width:1920,height:1080});
 await page.evaluate(()=>new Promise(resolve=>requestAnimationFrame(()=>requestAnimationFrame(resolve))));
 await assertDataList(page);
 await page.screenshot({path:evidence+'/memory-list-light-wide.png',fullPage:true});
 // Exercise a delayed bundle explicitly: native links remain usable, while
 // enhanced-sheet assertions wait for the actual feature initialization.
 let release;const held=new Promise(resolve=>{release=resolve});
 await page.route('**/app.js',async route=>{await held;await route.continue()});
 try{
  await page.goto(url+'/developer/memory',{waitUntil:'commit'});
  await page.locator('a.memory-open').first().waitFor();
  if(await page.locator('#memory-page[data-memory-ready]').count())throw Error('Memory reported ready before loading its handlers');
  release();await page.locator('#memory-page[data-memory-ready]').waitFor();
 }finally{release();await page.unroute('**/app.js')}
 await page.getByRole('searchbox',{name:'Search memories',exact:true}).fill('walking by the river');
 await Promise.all([page.waitForURL(target=>target.searchParams.get('search')==='walking by the river'),page.getByRole('button',{name:'Search memories',exact:true}).click()]);
 const listURL=page.url();
 if(await page.locator('[data-memory-ref]').count()!==1)throw Error('Memory content search failed');
 await page.locator('#memory-page[data-memory-ready]').waitFor();
 await page.locator('a.memory-open').click();
 const dialog=page.getByRole('dialog',{name:'Memory details',exact:true});
 await dialog.waitFor();
 await dialog.getByRole('link',{name:/Source message/}).first().click();
 await dialog.locator('[data-memory-source-content]').waitFor();
 if(!(await dialog.locator('[data-memory-content]').innerText()).includes('walking by the river')||!(await dialog.locator('[data-memory-source-content]').innerText()).includes('walking by the river'))throw Error('Memory or original source body missing');
 const geometry=await dialog.boundingBox();const viewport=page.viewportSize();
 if(Math.abs(geometry.width-viewport.width/2)>4)throw Error('Memory peek is not half width');
 const split=dialog.getByRole('separator',{name:'Resize memory sources',exact:true});
 await split.focus();const width=Number(await split.getAttribute('aria-valuenow'));await split.press('ArrowRight');
 if(Number(await split.getAttribute('aria-valuenow'))<=width)throw Error('Memory sources cannot resize');
 await split.press('ArrowLeft');
 await page.screenshot({path:evidence+'/memory-detail-light.png',fullPage:true});
 await page.evaluate(()=>{document.documentElement.classList.add('dark')});
 await page.screenshot({path:evidence+'/memory-detail-dark.png',fullPage:true});
 await page.setViewportSize({width:390,height:844});
 await page.evaluate(()=>new Promise(resolve=>requestAnimationFrame(()=>requestAnimationFrame(resolve))));
 if(await page.evaluate(()=>document.documentElement.scrollWidth>innerWidth))throw Error('Memory detail expanded mobile viewport');
 await page.screenshot({path:evidence+'/memory-detail-mobile.png'});
 await dialog.getByRole('button',{name:'Close details',exact:true}).click();
 // A closed dialog and the asynchronous History traversal are separate browser
 // events. Finish the roundtrip before starting another full-page navigation.
 await page.waitForURL(listURL);await dialog.waitFor({state:'detached'});
 await page.goto(url+'/developer/memory');
 await page.evaluate(()=>document.documentElement.classList.remove('dark'));
 for(const width of [320,390,760,761,1024,1400]){await page.setViewportSize({width,height:844});await assertDataList(page)}
 await page.setViewportSize({width:390,height:844});await assertDataList(page);
 await page.screenshot({path:evidence+'/memory-list-mobile.png',fullPage:true});
 await page.setViewportSize({width:1440,height:1000});
 await page.goto(url+'/developer/memory?status=superseded');
 await page.locator('#memory-page[data-memory-ready]').waitFor();
 await page.locator('a.memory-open').first().click();
 await page.locator('.memory-detail').waitFor();
 if(await page.locator('[data-memory-content]').count())throw Error('Superseded memory body was displayed');
 await page.getByRole('dialog').getByRole('link',{name:'Open trace →',exact:true}).click();
 await page.locator('#observability-workbench').waitFor();
 await page.goto(url+'/developer/memory?view=maintenance');
 if(!await page.locator('.memory-maintenance tbody tr').count()||!(await page.locator('.memory-maintenance').innerText()).includes('applied'))throw Error('Memory maintenance receipts missing');
 await maintenanceInspection({page,url,evidence});
 await page.goto(url+'/settings/preferences');
 await page.locator('[data-language-trigger]').click();
 await page.locator('[data-language-option="zh-CN"]').click();
 await page.locator('html[lang="zh-CN"]').waitFor();
 await page.goto(url+'/developer/memory');
 if(!await page.getByRole('heading',{name:'记忆',exact:true}).count())throw Error('Memory page did not localize');
 await assertDataList(page);
 await page.screenshot({path:evidence+'/memory-list-chinese.png',fullPage:true});
 await page.goto(url+'/settings/preferences');
 await page.locator('[data-language-trigger]').click();
 await page.locator('[data-language-option="en"]').click();
 await page.locator('html[lang="en"]').waitFor();
 const nativeContext=await browser.newContext({javaScriptEnabled:false,viewport:{width:1200,height:850}});
 try {
  await nativeContext.addCookies(await context.cookies());const native=await nativeContext.newPage();
  await native.goto(url+'/developer/memory');
  await native.getByRole('searchbox',{name:'Search memories',exact:true}).fill('walking by the river');
  await native.getByRole('button',{name:'Search memories',exact:true}).click();
  await native.locator('a.memory-open').click();await native.locator('[data-memory-content]').waitFor();
  await native.getByRole('link',{name:/Source message/}).first().click();
  if(!(await native.locator('[data-memory-source-content]').innerText()).includes('walking by the river'))throw Error('No-JS memory source navigation failed');
 } finally {await nativeContext.close()}
 await writeFile(evidence+'/memory-contract.json',JSON.stringify({firstPage:first.length,secondPage:second.length,sourceBody:true,resizable:true,halfWidth:true,noJavaScript:true,mobile:true},null,2));
}

// Shared by the real-domain channels flow and the priced/unknown presentation fixture.
export async function maintenanceInspection({page,url,evidence,priced=false}){
 await page.goto(url+'/developer/memory?view=maintenance');
 await page.locator('#memory-page[data-memory-ready]').waitFor();
 const list=page.locator('.memory-maintenance');
 if(!await list.locator('tbody tr a.memory-open').count())throw Error('Maintenance rows have no detail links');
 if(priced&&(!(await list.innerText()).includes('USD 0.12')||!(await list.innerText()).includes('Not priced')))throw Error('Priced/unknown maintenance values missing');
 await page.setViewportSize({width:1440,height:1000});await assertDataList(page);
 await page.screenshot({path:evidence+'/memory-maintenance-list-light.png',fullPage:true});
 await list.locator('.obs-table-scroll').evaluate(n=>n.scrollLeft=n.scrollWidth);
 await page.screenshot({path:evidence+'/memory-maintenance-list-costs.png',fullPage:true});
 await list.locator('tbody tr').first().locator('td').nth(2).click();
 const sheet=page.getByRole('dialog',{name:'Memory details',exact:true});await sheet.waitFor();
 await sheet.getByText('Documents written',{exact:true}).waitFor();
 const pane=await sheet.locator('.obs-detail-pane').boundingBox(),frame=await sheet.boundingBox();
 if(pane.width<frame.width-36)throw Error('Single-pane maintenance detail does not fill the sheet');
 if(priced&&!(await sheet.innerText()).includes('0.12'))throw Error('Detail cost missing');
 if(priced){
  const attempts=sheet.locator('.memory-generation-history details');
  if(await attempts.count()!==2)throw Error('Maintenance omitted model attempt history');
  await attempts.last().locator('summary').click();
  await sheet.getByText('Supporting quote is empty or is not an exact substring of its observation.',{exact:true}).waitFor();
 }
 await page.evaluate(()=>document.fonts.ready);await page.evaluate(()=>new Promise(r=>requestAnimationFrame(()=>requestAnimationFrame(r))));
 await writeFile(evidence+'/memory-maintenance-geometry.json',JSON.stringify(await sheet.locator('[data-copy-value]').last().evaluate(n=>({text:n.innerText,color:getComputedStyle(n).color,visibility:getComputedStyle(n).visibility,rect:n.getBoundingClientRect().toJSON(),value:n.querySelector('.control-identifier-value')?.getBoundingClientRect().toJSON()})),null,2));
 await page.screenshot({path:evidence+'/memory-maintenance-detail-light.png'});
 await page.evaluate(()=>document.documentElement.classList.add('dark'));
 await page.evaluate(()=>new Promise(r=>requestAnimationFrame(()=>requestAnimationFrame(r))));
 await page.screenshot({path:evidence+'/memory-maintenance-detail-dark.png'});
 await page.setViewportSize({width:390,height:844});
 if(await page.evaluate(()=>document.documentElement.scrollWidth>innerWidth))throw Error('Maintenance viewport overflow');
 await page.evaluate(()=>new Promise(resolve=>requestAnimationFrame(()=>requestAnimationFrame(resolve))));
 if(await page.getByRole('dialog',{name:'Memory details',exact:true}).count()!==1)throw Error('Maintenance opened duplicate sheets');
 await page.screenshot({path:evidence+'/memory-maintenance-detail-mobile.png'});
 if(priced){
  await sheet.getByRole('button',{name:'Next memory',exact:true}).click();
  await sheet.getByText('MEMORY_INVALID_PROPOSAL',{exact:true}).waitFor();
  await sheet.locator('.memory-generation-history details summary').click();
  await sheet.getByText('Failure details were not recorded for this older attempt.',{exact:true}).waitFor();
 }
 await sheet.getByRole('button',{name:'Close details',exact:true}).click();await sheet.waitFor({state:'detached'});
 await page.waitForURL(u=>u.searchParams.get('view')==='maintenance'&&!u.searchParams.has('ref'));
 await page.evaluate(()=>document.documentElement.classList.remove('dark'));
 await page.setViewportSize({width:1440,height:1000});
}

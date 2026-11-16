import assert from 'node:assert/strict';
import {settingsLayout} from './channels_capabilities.mjs';
import {contextHelp} from './channels_help.mjs';
import {selectValue} from './channels_controls.mjs';
import {mkdir,writeFile} from 'node:fs/promises';
import {chromium} from '../../guest-browser/node_modules/playwright-core/index.mjs';

const [url,broker,evidence,rawOptions]=process.argv.slice(2);
const options=rawOptions?JSON.parse(rawOptions):{};
await mkdir(evidence,{recursive:true});
const browser=await chromium.launch({headless:true,...(process.env.STEWARD_BROWSER_EXECUTABLE?{executablePath:process.env.STEWARD_BROWSER_EXECUTABLE}:{})});
try{
 const context=await browser.newContext({viewport:{width:1400,height:980}});
 await context.route('https://app.composio.dev/link/**',route=>route.fulfill({contentType:'text/html',body:'<h1>Owned OAuth test peer</h1><button>Approve account</button>'}));
 await context.request.post(url+'/session',{form:{token:options.token??'private-token-at-least-24-bytes',next:'/settings/apps'}});
 const page=await context.newPage();
 await page.goto(url+'/settings/apps');
 await page.locator('[data-apps-ready]').waitFor();
 if(!options.appsOnly){await settingsLayout(page,context,browser,url,evidence);await contextHelp(page,context,browser,url,evidence)}
 if(options.interfaceRevision){assert.equal(await page.locator('[data-apps]').getAttribute('data-app-interface-mode'),'managed');assert.equal(await page.locator('[data-apps]').getAttribute('data-app-interface-revision'),options.interfaceRevision)}
 await page.goto(url+'/settings/apps');await page.locator('[data-apps-ready]').waitFor();
 assert.equal(await page.locator('#composio-key-ref').count(),0);
 assert.equal(await page.locator('[name=auth_config]').count(),0);
 // Editorial discovery stays bounded; the full catalogue retains every app.
 const featured=['gmail','googledrive','googlecalendar','github','slack','notion'];
 assert.deepEqual(await page.locator('.apps-group').first().locator('[data-app-tile]').evaluateAll(nodes=>nodes.map(n=>n.dataset.appTile)),featured);
 assert((await page.locator('.apps-group').evaluateAll(groups=>groups.map(g=>g.querySelectorAll('[data-app-tile]').length))).every(count=>count<=6));
 const homeSlugs=await page.locator('[data-app-tile]').evaluateAll(nodes=>nodes.map(n=>n.dataset.appTile));assert.equal(new Set(homeSlugs).size,homeSlugs.length);
 await page.locator('.apps-group').nth(1).getByRole('link',{name:'View all: Communication and calendars',exact:true}).click();
 await page.waitForURL(next=>next.searchParams.get('collection')==='communication');
 assert.equal(await page.locator('[data-app-tile]').count(),10);
 const names=await page.locator('.app-copy strong').allTextContents();assert.deepEqual(names,[...names].sort((a,b)=>a.toLowerCase()<b.toLowerCase()?-1:1));
 await page.getByRole('link',{name:'Back to apps',exact:true}).click();
 await page.locator('.apps-tabs').getByRole('link',{name:'All apps',exact:true}).click();
 assert.equal(await page.locator('[data-app-tile]').count(),65);
 await page.locator('#app-category-picker').waitFor();
 await page.getByRole('combobox',{name:'Category',exact:true}).click();await page.getByRole('listbox').waitFor();
 await page.evaluate(()=>new Promise(resolve=>requestAnimationFrame(()=>requestAnimationFrame(resolve))));
 await page.keyboard.press('Escape');await page.getByRole('listbox').waitFor({state:'hidden'});
 assert.equal(await page.locator('[data-app-tile]').count(),65);
 await selectValue(page,page.locator('#app-category'),'communication');
 await page.waitForURL(next=>next.searchParams.get('category')==='communication');
 assert.equal(await page.locator('[data-app-tile]').count(),8);
 await selectValue(page,page.locator('#app-category'),'');await page.waitForURL(next=>next.searchParams.get('category')==='');
 assert.equal(await page.locator('[data-app-tile]').count(),65);

 await page.locator('.app-tile img').evaluateAll(images=>images.forEach(img=>img.loading='eager'));
 await page.waitForFunction(()=>Array.from(document.querySelectorAll('.app-tile img')).every(img=>img.complete&&img.naturalWidth>0));
 await page.goto(url+'/settings/apps');
 for(const language of ['en','zh-CN']){
  await context.request.post(url+'/settings/language',{form:{language}});await page.reload();
  await page.locator('[data-apps-ready]').waitFor();
  await page.locator('.app-tile img').evaluateAll(images=>images.forEach(img=>img.loading='eager'));
  await page.waitForFunction(()=>Array.from(document.querySelectorAll('.app-tile img')).every(img=>img.complete&&img.naturalWidth>0));
  for(const width of [1400,390,320]){
   await page.setViewportSize({width,height:980});await page.reload();await page.locator('[data-apps-ready]').waitFor();
   await page.locator('.app-tile img').evaluateAll(images=>images.forEach(img=>img.loading='eager'));
   await page.waitForFunction(()=>Array.from(document.querySelectorAll('.app-tile img')).every(img=>img.complete&&img.naturalWidth>0));
   await page.evaluate(()=>document.fonts.ready);
   assert.equal(await page.evaluate(()=>document.documentElement.scrollWidth>innerWidth),false,'Directory overflow');
   const columns=await page.locator('.apps-grid').first().evaluate(el=>getComputedStyle(el).gridTemplateColumns.split(' ').length);
   assert.equal(columns,width===1400?2:1);
   await page.screenshot({path:`${evidence}/directory-${language}-${width}.png`,fullPage:width>760});
  }
 }
 await page.evaluate(()=>localStorage.setItem('steward-theme','dark'));
 for(const width of [1400,320]){await page.setViewportSize({width,height:980});await page.reload();await page.locator('[data-apps-ready]').waitFor();await page.locator('.app-tile img').evaluateAll(images=>images.forEach(img=>img.loading='eager'));await page.waitForFunction(()=>Array.from(document.querySelectorAll('.app-tile img')).every(img=>img.complete&&img.naturalWidth>0));await page.evaluate(()=>document.fonts.ready);await page.screenshot({path:`${evidence}/directory-dark-${width}.png`,fullPage:width>760})}
 await page.evaluate(()=>localStorage.setItem('steward-theme','light'));
 await context.request.post(url+'/settings/language',{form:{language:'en'}});await page.setViewportSize({width:1400,height:980});await page.reload();
 await context.request.post(broker+'/test/configure-project');
 await page.reload();
 await page.locator('[data-app-tile=gmail]').getByRole('button',{name:'Connect Gmail',exact:true}).waitFor();
 assert.equal((await (await context.request.get(broker+'/test/count')).json()).created,0);
 await page.getByRole('link',{name:'App settings',exact:true}).click();
 assert.equal(await page.locator('.settings-navigation [aria-current=page]').getAttribute('href'),'/settings/apps');
 await page.locator('#composio-key-ref').fill('env:COMPOSIO_API_KEY');
 await page.getByRole('button',{name:'Verify and save',exact:true}).click();
 await page.getByText('App connection service saved.',{exact:true}).waitFor();
 await page.getByRole('link',{name:'Back to apps',exact:true}).click();
 await page.locator('[data-apps-ready]').waitFor();
 assert.equal(await page.locator('#composio-key-ref').count(),0);
 const count=async()=>(await (await context.request.get(broker+'/test/count')).json()).created;
 const tile=page.locator('[data-app-tile=gmail]');
 const detail=page.locator('[data-app-dialog]');
 const designReview=[];
 const detailRow=id=>detail.locator(`[data-app-account="${id}"]`);
 const pending=()=>detail.locator('[data-app-state=pending]').first();
 const disconnectAccount=async(id,cancelFirst=false)=>{
  const button=detailRow(id).getByRole('button',{name:'Disconnect',exact:true});await button.click();
  const confirmation=page.locator('dialog.settings-confirm');await confirmation.waitFor();
  assert.equal(await confirmation.getByRole('button',{name:'Cancel',exact:true}).evaluate(node=>node===document.activeElement),true);
  if(cancelFirst){await page.keyboard.press('Escape');assert.equal(await detailRow(id).getAttribute('data-app-state'),'active');await button.click();await confirmation.waitFor()}
  await confirmation.getByRole('button',{name:'Disconnect this account?',exact:true}).click();
 };

 const checkWidths=async(state)=>{
  for(const width of [1400,390,320]){
   await page.setViewportSize({width,height:980});
   assert.equal(await page.evaluate(()=>document.documentElement.scrollWidth>innerWidth),false,`Apps overflowed in ${state} at ${width}px`);
   const metrics=await detail.evaluate(node=>({radius:getComputedStyle(node).borderRadius,buttons:[...node.querySelectorAll('button,.button')].filter(el=>el.getClientRects().length).map(el=>({height:el.getBoundingClientRect().height,radius:getComputedStyle(el).borderRadius}))}));
   assert.equal(metrics.radius,'12px');assert(metrics.buttons.every(button=>button.radius==='8px'));
   if(width<=760)assert(metrics.buttons.every(button=>button.height>=44));
   designReview.push({state,width,...metrics});
   await page.screenshot({path:`${evidence}/apps-${state}-${width}.png`,fullPage:true});
  }
  await page.setViewportSize({width:1400,height:980});
 };
 assert.equal(await tile.locator('[name=alias]').isVisible(),false);
 const firstPopup=page.waitForEvent('popup');
 await tile.getByRole('button',{name:'Connect Gmail',exact:true}).click();
 const first=await firstPopup;await first.getByRole('heading',{name:'Owned OAuth test peer'}).waitFor();
 const firstRow=pending();await firstRow.waitFor();
 const firstID=await firstRow.getAttribute('data-app-account');
 await firstRow.getByRole('link',{name:'Open authorization page'}).waitFor();
 await checkWidths('pending');
 const repeatPopup=page.waitForEvent('popup');await firstRow.getByRole('link',{name:'Open authorization page'}).click();await(await repeatPopup).close();
 assert.equal(await count(),1);
 await first.getByRole('button',{name:'Approve account'}).click();await context.request.post(broker+'/test/activate?id=ca_1');
 await detail.locator(`[data-app-account="${firstID}"][data-app-state=active]`).waitFor({timeout:15000});
 await detail.locator('[data-app-add-account]').waitFor();
 assert.equal(await page.locator('#apps-sidebar [data-app-open=gmail] img').count(),1);
 await page.keyboard.press('Escape');await detail.waitFor({state:'hidden'});
 // Native dialog close events restore focus asynchronously; assert the eventual
 // focused opener rather than sampling before the queued close event.
 await tile.locator('[data-app-manage]').and(page.locator(':focus')).waitFor();
 await tile.locator('[data-app-manage]').click();await detail.waitFor();
 await checkWidths('connected');
 for(const width of [1400,320]){
  await page.setViewportSize({width,height:980});await page.evaluate(()=>{document.documentElement.classList.add('dark')});
  await page.screenshot({path:`${evidence}/detail-dark-${width}.png`,fullPage:true});
 }
 await page.evaluate(()=>{document.documentElement.classList.remove('dark');document.documentElement.style.zoom='2'});
 const zoom=await page.evaluate(()=>({viewport:innerWidth,scroll:document.documentElement.scrollWidth,dialog:document.querySelector('[data-app-dialog]').getBoundingClientRect().toJSON(),overflow:[...document.querySelectorAll('body *')].filter(el=>{const r=el.getBoundingClientRect();return r.width>0&&r.right>innerWidth+1}).slice(0,12).map(el=>({tag:el.tagName,cls:el.className,rect:el.getBoundingClientRect().toJSON()}))}));
 await writeFile(evidence+'/zoom.json',JSON.stringify(zoom,null,2));
 await page.screenshot({path:`${evidence}/detail-zoom-320.png`,fullPage:true});
 assert.equal(await detail.evaluate(node=>node.scrollWidth>node.clientWidth),false,'Zoomed detail content overflowed');
 assert(zoom.dialog.left>=0&&zoom.dialog.right<=zoom.viewport,'Zoomed dialog left viewport');
 await page.screenshot({path:`${evidence}/detail-zoom-320.png`,fullPage:true});
 await page.evaluate(()=>{document.documentElement.style.zoom=''});await page.setViewportSize({width:1400,height:980});

 await detail.locator('[data-app-add-account] summary').click();
 await detail.getByRole('button',{name:'Connect account',exact:true}).click();assert.equal(await count(),1);
 await detail.locator('[name=alias]').fill('work');
 await page.evaluate(()=>{window.appsOriginalOpen=window.open;window.open=()=>null});
 await detail.getByRole('button',{name:'Connect account',exact:true}).click();
 await detail.getByText('The browser blocked the authorization tab. Use Open authorization page to continue.',{exact:true}).waitFor();
 const secondRow=pending();await secondRow.waitFor();
 const secondID=await secondRow.getAttribute('data-app-account');
 const secondLink=secondRow.getByRole('link',{name:'Open authorization page'});await secondLink.waitFor();
 assert.equal(await secondLink.getAttribute('href'),'https://app.composio.dev/link/ca_2');
 assert.equal(await count(),2);
 await context.request.post(broker+'/test/activate?id=ca_2');
 await detail.locator(`[data-app-account="${secondID}"][data-app-state=active]`).waitFor({timeout:15000});
 await disconnectAccount(secondID,true);
 await detailRow(secondID).waitFor({state:"detached"});
 assert.equal(await detailRow(firstID).getAttribute('data-app-state'),'active');
 await context.request.post(broker+'/test/expire?id=ca_1');
 await detailRow(firstID).getByRole('button',{name:'Refresh status',exact:true}).click();
 await detail.locator(`[data-app-account="${firstID}"][data-app-state=expired]`).waitFor();
 await page.evaluate(()=>{window.open=window.appsOriginalOpen});
 const reconnectPopup=page.waitForEvent('popup');
 await detailRow(firstID).getByRole('button',{name:'Reconnect',exact:true}).click();await(await reconnectPopup).close();
 await pending().waitFor();
 const thirdID=await pending().getAttribute('data-app-account');
 assert.notEqual(thirdID,firstID);assert.equal(await count(),3);
 // Deliver a valid earlier ACTIVE response after Disconnect. It must not
 // resurrect the account in the browser or overwrite the newer revision.
 let release,received;const held=new Promise(resolve=>{release=resolve}),oldReply=new Promise(resolve=>{received=resolve});
 await page.route('**/apps/configure',async route=>{
  const body=new URLSearchParams(route.request().postData()??'');
  if(body.get('action')==='check'&&body.get('connection')===thirdID){const response=await route.fetch();received();await held;await route.fulfill({response});return}
  await route.continue();
 });
 await context.request.post(broker+'/test/activate?id=ca_3');await oldReply;
 await disconnectAccount(thirdID);
 await detailRow(thirdID).waitFor({state:"detached"});release();
 await page.waitForTimeout(200);
 assert.equal(await page.locator(`[data-app-account="${thirdID}"]`).count(),0);
 assert.equal(await page.locator('#apps-sidebar [data-app-open=gmail]').count(),0);
 const storage=await page.evaluate(()=>JSON.stringify({local:{...localStorage},session:{...sessionStorage}}));
 assert(!storage.includes('app.composio.dev/link/')&&!storage.includes('COMPOSIO_API_KEY'));
 for(const language of ['en','zh-CN']){
  await context.request.post(url+'/settings/language',{form:{language}});
  for(const width of [1400,390,320]){
   await page.setViewportSize({width,height:980});await page.goto(url+'/settings/apps');
   await page.locator('[data-apps-ready]').waitFor();
   assert.equal(await page.evaluate(()=>document.documentElement.scrollWidth>innerWidth),false);
   await page.screenshot({path:`${evidence}/apps-${language}-${width}.png`,fullPage:true});
  }
 }
 await page.goto(url+'/settings/apps?view=connected');assert.equal(await page.locator('#app-search').count(),0);
 await context.request.post(url+'/settings/language',{form:{language:'en'}});
 await page.goto(url+'/settings/apps');await page.reload();assert.equal(await count(),3);
 const native=await browser.newContext({javaScriptEnabled:false,viewport:{width:390,height:980}});await native.addCookies(await context.cookies());
 const nativePage=await native.newPage();await nativePage.goto(url+'/settings/apps');
 await nativePage.locator('.apps-group').nth(1).getByRole('link',{name:'View all: Communication and calendars',exact:true}).click();
 assert.equal(await nativePage.locator('[data-app-tile]').count(),10);
 await nativePage.locator('.apps-tabs').getByRole('link',{name:'All apps',exact:true}).click();
 assert.equal(await nativePage.locator('[data-app-tile]').count(),1,'Native full directory must use live results');
 await nativePage.goto(url+'/settings/apps?app=gmail');
 await nativePage.getByRole('heading',{name:'Gmail',exact:true}).waitFor();await nativePage.getByRole('button',{name:'Connect',exact:true}).waitFor();
 await nativePage.screenshot({path:`${evidence}/native-detail-390.png`,fullPage:true});await native.close();


 if(options.stopCheck){
  const fourthPopup=page.waitForEvent('popup');await page.locator('[data-app-tile=gmail]').getByRole('button',{name:'Connect Gmail',exact:true}).click();await(await fourthPopup).close();
  await page.locator('[data-app-dialog] [data-app-state=pending]').waitFor();
  const fourthID=await page.locator('[data-app-dialog] [data-app-state=pending]').getAttribute('data-app-account');
  assert.equal(await count(),4);
  assert.equal((await context.request.post(broker+'/test/quarantine-interface')).status(),204);
  await page.getByText('App presentation is unavailable. Reload after the owner restores the plugin.',{exact:true}).first().waitFor({timeout:20000});
  assert.equal(await page.locator('[data-app-account]').count(),0);
  const response=await context.request.post(url+'/apps/configure',{form:{action:'connect',toolkit:'gmail'},headers:{Accept:'application/json'}});
  assert.equal(response.status(),200);const receipt=await response.json();assert.equal(receipt.presentation_unavailable,'true');assert.equal(receipt.connection_id,fourthID);assert.equal(receipt.authorization_url,'https://app.composio.dev/link/ca_4');
  const nativeReceipt=await context.request.post(url+'/apps/configure',{form:{action:'connect',toolkit:'gmail'}});assert.equal(nativeReceipt.status(),200);const body=await nativeReceipt.text();assert(body.includes('The account action was accepted.'));assert(body.includes('https://app.composio.dev/link/ca_4'));assert(!body.includes('data-apps '));
  assert.equal(await count(),4);
 }
 await writeFile(evidence+'/design-review.json',JSON.stringify(designReview,null,2));
 await writeFile(evidence+'/checks.json',JSON.stringify({nativeFallback:true,managedInterface:options.interfaceRevision??null,stoppedInterface:Boolean(options.stopCheck),sharedControls:true,disconnectConfirmation:true,officialLogos:65,groupedDirectory:true,featuredOrder:true,boundedPreviews:true,allApps:true,collectionNavigation:true,detailsDialog:true,connectedSidebar:true,projectSeparated:true,connect:true,popupFallback:true,reopenReusesLink:true,automaticStatus:true,multiAccount:true,reconnect:true,latePollFenced:true,created:await count(),noBrowserStorage:true,languages:['en','zh-CN'],widths:[1400,390,320]},null,2));
}finally{await browser.close()}

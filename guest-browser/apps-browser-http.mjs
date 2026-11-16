import assert from 'node:assert/strict';
import {chromium} from 'playwright-core';
import {mkdir,writeFile} from 'node:fs/promises';
const [url,out]=process.argv.slice(2);await mkdir(out,{recursive:true});
const browser=await chromium.launch({headless:true,...(process.env.STEWARD_BROWSER_EXECUTABLE?{executablePath:process.env.STEWARD_BROWSER_EXECUTABLE}:{})});
try{
 const context=await browser.newContext();
 assert.equal((await context.request.get(url+'/api/apps/browser?interface_revision=original')).status(),401);
 assert.equal((await context.request.post(url+'/session',{form:{token:'private-token-at-least-24-bytes'},maxRedirects:0})).status(),303);
 const page=await context.newPage();page.setDefaultTimeout(20000);
 await page.goto(url+'/fixture?ref=original');
 const projection=await page.evaluate(async()=>{
  const policy=await window.fixture;
  return policy.call({type:'project',accounts:[{slug:'mail',state:'active',public:false}],tiles:['mail','drive'],details:[{slug:'mail',suggested:false,no_auth:false,require_alias:false}]});
 });
 assert.equal(projection.type,'projection');assert.deepEqual(projection.tiles,[{manage_hidden:false,add_hidden:true},{manage_hidden:true,add_hidden:false}]);assert.deepEqual(projection.details[0].accounts,[0]);
 const frame=page.frames().find(f=>f.url().includes('/api/apps/browser'));
 assert.ok(frame);
 const isolation=await frame.evaluate(()=>({origin:self.origin,marker:globalThis.OWNING_PROGRAM_MARKER,scripts:document.scripts.length}));
 assert.equal(isolation.origin,'null');assert.equal(isolation.marker,undefined);assert.equal(isolation.scripts,1);
 await page.evaluate(async()=>{(await window.fixture).close()});assert.equal(await page.locator('iframe').count(),0);
 const rejected=[];
 for(const name of ['invalid','loop']){
  await page.goto(url+'/fixture?ref='+name);
  assert.equal(await page.evaluate(()=>window.fixture.then(()=>false,()=>true)),true);
  assert.equal(await page.locator('iframe').count(),0);rejected.push(name);
 }
 const orders={};
 for(const ref of ['original','reordered']){
  await page.goto(url+'/controls?ref='+ref);
  await page.locator('[data-apps][data-apps-ready]').waitFor();
  orders[ref]=await page.locator('[data-app-detail-accounts] [data-app-account]').evaluateAll(rows=>rows.map(row=>row.dataset.appAccount));
  assert.deepEqual(orders[ref],ref==='original'?['original_account','second_account']:['second_account','original_account']);
  assert.equal(await page.locator('iframe').count(),1);
 }
 await page.goto(url+'/controls?ref=omitted');
 await page.locator('[data-app-feedback]').filter({hasText:'presentation is unavailable'}).waitFor();
 assert.equal(await page.locator('[data-app-detail-accounts] [data-app-account]').count(),0);
 assert.equal(await page.locator('iframe').count(),0);
 let authorizationRequests=0;
 await context.route('https://app.composio.dev/**',route=>{authorizationRequests++;return route.fulfill({status:200,contentType:'text/html',body:'<title>Accepted synthetic authorization</title>'})});
 for(const ref of ['after-accept','after-finally','decision','bad-notice']){
 await page.goto(url+'/controls?ref='+ref);
 await page.locator('[data-apps][data-apps-ready]').waitFor();
 const popupReady=page.waitForEvent('popup');
 const replyReady=page.waitForResponse(response=>response.url().endsWith('/apps/configure'));
 await page.locator('[data-app-detail-accounts] [data-app-account=second_account] button[value=continue]').click();
 const reply=await replyReady;assert.equal(reply.status(),200,await reply.text());
 const popup=await popupReady;
 await popup.waitForURL('https://app.composio.dev/link/accepted-original');
 if(ref==='decision'){
  await page.locator('[data-app-feedback]').filter({hasText:'Independent action receipt.'}).waitFor();
  assert.equal(await page.locator('iframe').count(),1);
 }else{
  await page.locator('[data-app-feedback]').filter({hasText:'account action was accepted'}).waitFor();
  assert.equal(await page.locator('[data-app-feedback] a').getAttribute('href'),'https://app.composio.dev/link/accepted-original');
  assert.equal(await page.locator('iframe').count(),0);
 }
 await page.waitForTimeout(150);
 await popup.close();
 }
 assert.equal(authorizationRequests,4,'recovery navigated an accepted authorization URL twice');
 await page.goto(url+'/controls?ref=poll-paused');
 await page.locator('[data-apps][data-apps-ready]').waitFor();
 await page.locator('[data-app-feedback]').filter({hasText:'Independent polling paused.'}).waitFor();
 // Past the original five-second interval: Go separately asserts zero checks.
 await page.waitForTimeout(5500);
 await page.goto(url+'/controls?ref=manual-category');
 await page.locator('[data-apps][data-apps-ready]').waitFor();
 let categoryRequests=0;
 page.on('request',request=>{if(new URL(request.url()).pathname==='/settings/apps')categoryRequests++});
 const beforeCategory=page.url();
 await page.locator('[data-app-category-form] select').selectOption('');
 await page.waitForTimeout(200);
 assert.equal(page.url(),beforeCategory);assert.equal(categoryRequests,0);
 for(const ref of ['alias','alias-hidden']){
  await page.goto(url+'/controls?ref='+ref);
  await page.locator('[data-apps][data-apps-ready]').waitFor();
  await page.locator('[data-app-first] button').click();
  if(ref==='alias'){
   await page.locator('[data-app-dialog][open] [data-app-add-account][open] input[name=alias]').waitFor();
   assert.equal(await page.locator('[data-app-dialog] input[name=alias]').evaluate(input=>input===document.activeElement),true);
  }else{
   await page.locator('[data-app-feedback]').filter({hasText:'Choose a label'}).waitFor();
   await page.waitForTimeout(200);
   assert.equal(await page.locator('[data-app-dialog][open]').count(),0);
  }
 }
 for(const ref of ['bad-poll','bad-poll-expired']){
  await page.goto(url+'/controls?ref='+ref);
  await page.locator('[data-app-feedback]').filter({hasText:'presentation is unavailable'}).waitFor();
  assert.equal(await page.locator('iframe').count(),0);
  const links=page.locator('[data-app-feedback] a');
  if(ref==='bad-poll')assert.equal(await links.getAttribute('href'),'https://app.composio.dev/link/original');
  else assert.equal(await links.count(),0);
 }
 const styleChecks=[];
 for(const ref of ['original','layout'])for(const width of [1400,390])for(const theme of ['light','dark']){
  await page.setViewportSize({width,height:980});
  await page.evaluate(theme=>localStorage.setItem('steward-theme',theme),theme);
  await page.goto(url+'/controls?ref='+ref);
  await page.locator('[data-apps][data-apps-ready]').waitFor();
  await page.evaluate(()=>document.fonts.ready);
  const facts=await page.locator('[data-app-detail-accounts] [data-app-account]').first().evaluate(row=>({padding:getComputedStyle(row).paddingTop,font:getComputedStyle(row.querySelector('strong')).fontFamily,scheme:getComputedStyle(document.documentElement).colorScheme,buttons:[...row.querySelectorAll('button')].filter(button=>button.getClientRects().length).map(button=>button.getBoundingClientRect().height),overflow:document.documentElement.scrollWidth>innerWidth}));
  assert.equal(facts.padding,ref==='layout'?'24px':'16px');assert.match(facts.font,/Inter/);assert.equal(facts.scheme,theme);assert.equal(facts.overflow,false);assert(facts.buttons.length>0);assert(facts.buttons.every(height=>height>=(width===390?44:36)));
  await page.locator('[data-app-detail-accounts]').screenshot({path:out+'/style-'+ref+'-'+width+'-'+theme+'.png'});
  styleChecks.push({ref,width,theme,...facts});
 }
 const native=await browser.newContext({javaScriptEnabled:false,storageState:await context.storageState(),viewport:{width:390,height:980}});
 const nativePage=await native.newPage();
 for(const ref of ['original','layout']){
  await nativePage.goto(url+'/controls?ref='+ref);
  assert.equal(await nativePage.locator('iframe').count(),0);
  assert.equal(await nativePage.locator('[data-app-detail-accounts] [data-app-account]').first().evaluate(row=>getComputedStyle(row).paddingTop),ref==='layout'?'24px':'16px');
  assert.equal(await nativePage.locator('[data-app-detail-accounts] input[name=interface_revision]').first().getAttribute('value'),ref);
 }
 await native.close();
 await writeFile(out+'/checks.json',JSON.stringify({protectedRoute:true,originalProjection:true,opaqueOrigin:true,noDOMExecution:true,rejected,transportDisposal:true,actualOriginalDOMConsumer:true,owningActionNotice:true,owningPollPause:true,owningCategorySubmission:true,owningAliasRecovery:true,pendingReceiptRetainedOnPollPolicyFailure:true,expiredReceiptNotRevived:true,styleChecks,nativeIndependentStyles:true,noDuplicateAuthorizationNavigation:true,independentOrders:orders,omittedProjectionRejected:true,acceptedLinkRetainedAfterProjectionFailure:true,acceptedLinkRetainedAfterFinalSynchronizationFailure:true},null,2));
 await context.close();
}finally{await browser.close()}

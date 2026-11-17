import {chromium} from '../../guest-browser/node_modules/playwright-core/index.mjs';
import {mkdir,writeFile} from 'node:fs/promises';
import {appendFileSync} from 'node:fs';
import {waitForUIState} from '../../guest-browser/ui-state.mjs';
const [url,out,phase,ref,heading]=process.argv.slice(2);await mkdir(out,{recursive:true});
const browser=await chromium.launch({headless:true,...(process.env.STEWARD_BROWSER_EXECUTABLE?{executablePath:process.env.STEWARD_BROWSER_EXECUTABLE}:{})});
try{
 const context=await browser.newContext({reducedMotion:'reduce'}),page=await context.newPage();page.setDefaultTimeout(8000);
 const stage=value=>appendFileSync(out+'/stages.log',value+'\n');await page.setViewportSize({width:1400,height:980});
 stage('navigating');await page.goto(url);stage('login page');await page.getByLabel('Workspace access token',{exact:true}).fill(process.env.STEWARD_WEB_TEST_TOKEN);await page.getByRole('button',{name:'Open workspace'}).click();stage('authenticated');
 // Read the retained component as a user before asynchronous frames resize.
 await page.waitForFunction(()=>document.querySelector('.composer textarea')?.classList.contains('resize-none'));
 await page.mouse.move(4,200);await page.mouse.wheel(0,-100);
 if(phase.endsWith('preview')){
  const frame=page.frameLocator('iframe[title="Cards preview"]');await frame.locator('.native-card-title').filter({hasText:'Alpha'}).waitFor();if(await frame.locator('h3').textContent()!==heading)throw Error('preview changed owning version');
  const isolation=await frame.locator('body').evaluate(n=>({inert:n.inert,origin:self.origin,scripts:document.scripts.length,images:document.images.length}));
  if(!isolation.inert||isolation.origin!=='null'||isolation.scripts!==1||isolation.images!==0)throw Error('preview grants '+JSON.stringify(isolation));
  const source=await page.locator('iframe[title="Cards preview"]').getAttribute('src');if(!source.includes('component=1'))throw Error('preview lost original ordinal');
  await page.screenshot({path:out+'/preview.png'});await writeFile(out+'/report.json',JSON.stringify({scope:'actual App streamed incomplete JSON',originalOrdinal:1,isolation},null,2));
 }else{
  const outer=page.locator('section[data-view-ref='+JSON.stringify(ref)+']');
  await outer.scrollIntoViewIfNeeded();stage('original component visible');
  const frame=outer.frameLocator('iframe[title="Cards view"]');await frame.getByRole('button',{name:'Next card',exact:true}).waitFor();
  if(await frame.locator('h3').textContent()!==heading)throw Error('formal changed owning version');
  if(await outer.getAttribute('data-view-ref')!==ref)throw Error('formal original ref changed');
  {
   const image=frame.locator('img[src^="blob:"]');stage('waiting for image');await image.waitFor({timeout:8000});stage('image status '+JSON.stringify(await image.evaluate(n=>({complete:n.complete,width:n.naturalWidth}))));await image.evaluate(n=>{if(n.complete){if(!n.naturalWidth)throw Error('Original image failed');return}return new Promise((resolve,reject)=>{const timer=setTimeout(()=>reject(Error('Original image load timed out')),5000);n.addEventListener('load',()=>{clearTimeout(timer);n.naturalWidth?resolve():reject(Error('Original image has no pixels'))},{once:true});n.addEventListener('error',()=>{clearTimeout(timer);reject(Error('Original image failed'))},{once:true})})});stage('original image decoded');
   const decoded=await image.evaluate(n=>({width:n.naturalWidth,loading:n.loading,src:n.src,html:document.body.innerHTML}));if(decoded.width!==1||decoded.loading!=='eager'||!decoded.src.startsWith('blob:null/')||decoded.html.includes('artifact:'))throw Error('original Artifact blob lost '+JSON.stringify(decoded));
  }
  if(phase.startsWith('formal')){
   await context.route('https://example.com/**',route=>route.fulfill({contentType:'text/html',body:'Original destination'}));const pending=context.waitForEvent('page');await frame.getByRole('link',{name:'Details',exact:true}).click();const popup=await pending;await popup.waitForURL('https://example.com/alpha');await popup.close();
   await frame.getByRole('button',{name:'Next card',exact:true}).click();await waitForUIState(page,ref,state=>state.state.index===1&&state.model_context.visible_item==='Beta');
   await frame.getByRole('button',{name:'Beta',exact:true}).click();
  }
  await frame.locator('.native-carousel-controls span').filter({hasText:'2 / 2'}).waitFor();await frame.getByRole('button',{name:'Beta',exact:true}).getAttribute('aria-pressed').then(async value=>{if(value!=='true')await frame.locator('.native-card-title[aria-pressed=true]').filter({hasText:'Beta'}).waitFor()});
  const table=outer.locator('xpath=ancestor::*[@data-ui-spec]').frameLocator('iframe[title="Table view"]');await table.locator('tbody').evaluate(node=>new Promise((resolve,reject)=>{const deadline=performance.now()+5000;const check=()=>node.rows.length===1?resolve():performance.now()>deadline?reject(Error('original shared Table selection not restored')):setTimeout(check,25);check()}));await table.getByRole('button',{name:'Beta',exact:true}).waitFor();if(await table.getByRole('button',{name:'Alpha',exact:true}).count())throw Error('shared selection did not filter managed Table');
  if(phase.startsWith('formal')){
   const surface=outer.locator('dialog.answer-surface');
   for(const[label,mode]of[['Expand','fullscreen'],['Open dialog','modal'],['Keep visible','pip']]){
    await surface.getByRole('button',{name:'Options',exact:true}).click();await page.getByRole('menuitem',{name:label,exact:true}).click();
    await page.waitForFunction(({ref,mode})=>document.querySelector('section[data-view-ref='+JSON.stringify(ref)+'] dialog')?.dataset.mode===mode,{ref,mode});
    await frame.getByRole('button',{name:'Beta',exact:true}).focus();await page.keyboard.press('Escape');
    await page.waitForFunction(ref=>document.querySelector('section[data-view-ref='+JSON.stringify(ref)+'] dialog')?.dataset.mode==='inline',ref);
   }
  }
  const state=await waitForUIState(page,ref,state=>state.state.index===1&&state.model_context.visible_item==='Beta');
  await waitForUIState(page,ref.replace(/#1$/,'#0'),state=>state.model_context.selection==='Beta');
  stage('original CAS and group restored');
  for(const width of[1400,390]){await page.setViewportSize({width,height:980});for(const theme of['light','dark']){stage('screenshot '+width+' '+theme);await page.evaluate(theme=>document.documentElement.classList.toggle('dark',theme==='dark'),theme);await outer.scrollIntoViewIfNeeded();stage('target scrolled');await frame.locator('body').evaluate(()=>Promise.race([document.fonts.ready,new Promise((_,reject)=>setTimeout(()=>reject(Error('Draft font readiness timed out')),5000))]));stage('fonts ready');await frame.locator('body').evaluate(async n=>Promise.all(n.getAnimations({subtree:true}).filter(a=>Number.isFinite(a.effect.getTiming().iterations)).map(a=>a.finished.catch(()=>{}))));stage('frame transitions settled');await outer.evaluate(async n=>Promise.all((n.closest('.message')||n).getAnimations({subtree:true}).filter(a=>Number.isFinite(a.effect.getTiming().iterations)).map(a=>a.finished.catch(()=>{}))));stage('message transitions settled');await page.screenshot({path:out+'/cards-'+theme+'-'+width+'.png'})}}
  stage('screenshots complete');await writeFile(out+'/report.json',JSON.stringify({scope:'actual App/SQLite/Artifact/HTTP/React',ref,phase,heading,state,groupFiltersTable:true,originalArtifactDecoded:true,originalLinkOpened:phase.startsWith('formal'),trustedEscapeAllModes:phase.startsWith('formal'),coldRestored:phase==='restart'},null,2));
 }
 await context.close();
}finally{await browser.close()}

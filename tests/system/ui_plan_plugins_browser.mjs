import {chromium} from '../../guest-browser/node_modules/playwright-core/index.mjs';
import {mkdir,writeFile,readFile} from 'node:fs/promises';
const[url,evidence,phase,expectedRef='',expectedCaption='Plan:']=process.argv.slice(2);await mkdir(evidence,{recursive:true});
const originalCSS=await readFile(new URL('../../internal/plugin/intelligentui/testdata/original-plan.css',import.meta.url),'utf8');const originalHTML=JSON.parse(await readFile(new URL('../../internal/plugin/intelligentui/testdata/original-plan.json',import.meta.url),'utf8'))[0].expected.html;
const browser=await chromium.launch({headless:true,...(process.env.STEWARD_BROWSER_EXECUTABLE?{executablePath:process.env.STEWARD_BROWSER_EXECUTABLE}:{})});
let page;try{
 const context=await browser.newContext({viewport:{width:1400,height:900},reducedMotion:'reduce'});await context.addInitScript(()=>{window.planEvents=[];const Original=window.EventSource;window.EventSource=class extends Original{constructor(...args){super(...args);this.addEventListener('ui-preview',event=>{window.planEvents.push(JSON.parse(event.data))})}}});await context.addInitScript(()=>{
 if(parent===self){window.planInputEvents=[];addEventListener('message',event=>{if(event.origin==='null'&&event.data?.channel==='fixture-plan-input')planInputEvents.push(event.data)})}
 else if(location.pathname==='/ui/plan'||location.pathname==='/ui/table'){
  const id=Math.random().toString(36).slice(2);
  const record=event=>parent.postMessage({channel:'fixture-plan-input',id,at:Date.now(),type:event.type,target:event.target?.tagName,open:!!document.querySelector('details')?.open,source:location.href},'*');
  for(const type of['pointerdown','pointerup','click','toggle'])addEventListener(type,record,true);
  addEventListener('pagehide',record);
 }
});page=await context.newPage();await page.route('**/__original-plan.css',route=>route.fulfill({status:200,contentType:'text/css',body:originalCSS.replaceAll('.native-plan','#original-plan-baseline .native-plan')}));page.setDefaultTimeout(15000);
 await page.goto(url);await page.getByLabel('Workspace access token',{exact:true}).fill(process.env.STEWARD_WEB_TEST_TOKEN);await page.getByRole('button',{name:'Open workspace'}).click();await page.locator('#composer-root textarea').waitFor();
 // Read the retained component as a user before asynchronous frames resize.
 await page.waitForFunction(()=>document.querySelector('.composer textarea')?.classList.contains('resize-none'));
 await page.mouse.move(4,200);await page.mouse.wheel(0,-100);
 const selector=phase==='preview'?'iframe[src^="/ui/plan-preview?"]':'iframe[src^="/ui/plan?"]'+(expectedRef?'[src*="'+encodeURIComponent(expectedRef)+'"]':'');await page.locator(selector).waitFor();const frame=page.frameLocator(selector);await frame.locator('summary').waitFor();const detectedAt=Date.now();if(!(await frame.locator('summary').innerText()).trim().startsWith(expectedCaption))throw Error('wrong frozen Plan template');
 if(await page.locator(selector).count()!==1)throw Error('expected exactly one managed Plan');
 if(expectedRef&&new URL(await page.locator(selector).getAttribute('src'),url).searchParams.get('ref')!==expectedRef)throw Error('original component reference changed');
 if(phase==='preview'){
  if(!await frame.locator('body').evaluate(node=>node.inert))throw Error('preview gained interaction');
 }else{
  if(await frame.locator('body').evaluate(node=>node.inert))throw Error('formal details remained inert');
  // A neighboring managed Table now initializes asynchronously. Wait for its
  // real restoration/fonts/Host-applied height before sending a physical click
  // into the Plan below it. These are reads, not selection or state repairs.
  const tableFrames=page.locator('iframe[title="Table view"]');
  for(let index=0;index<await tableFrames.count();index++){
   const table=page.frameLocator('iframe[title="Table view"]').nth(index);
   await table.locator('button:not([disabled])').first().waitFor();await table.locator('body').evaluate(()=>document.fonts.ready);
   const deadline=Date.now()+5000;let settled=false;
   while(Date.now()<deadline){const bodyHeight=await table.locator('body').evaluate(node=>Math.ceil(node.getBoundingClientRect().height));const hostHeight=await tableFrames.nth(index).evaluate(node=>node.clientHeight);if(bodyHeight===hostHeight){settled=true;break}await new Promise(resolve=>setTimeout(resolve,25))}
   if(!settled)throw Error('original Table height was not applied by Host');
  }
  await frame.locator('body').evaluate(()=>document.fonts.ready);
  await frame.locator('summary').click();await frame.getByText('Ship safe core',{exact:true}).waitFor();
 }
 await page.evaluate(async html=>{const node=document.createElement('section');node.id='original-plan-baseline';node.className='openintelligentui-native';node.style.cssText='position:absolute;visibility:hidden;top:-10000px';node.innerHTML=html;document.body.append(node);await new Promise((resolve,reject)=>{const link=document.createElement('link');link.rel='stylesheet';link.href='/__original-plan.css';link.onload=resolve;link.onerror=reject;document.head.append(link)})},originalHTML);
 for(const width of [1400,390]){await page.setViewportSize({width,height:900});for(const theme of ['light','dark']){await page.evaluate(theme=>document.documentElement.classList.toggle('dark',theme==='dark'),theme);await frame.locator('html').evaluate(()=>new Promise(resolve=>requestAnimationFrame(()=>requestAnimationFrame(resolve))));if(await frame.locator('html').evaluate(node=>getComputedStyle(node).colorScheme)!==theme)throw Error('actual App theme transfer failed');const frameWidth=await page.locator(selector).evaluate(node=>node.getBoundingClientRect().width);const expected=await page.locator('#original-plan-baseline').evaluate((node,width)=>{node.style.width=width+'px';const summary=node.querySelector('summary'),style=getComputedStyle(summary);return {fontFamily:style.fontFamily,fontSize:style.fontSize,lineHeight:style.lineHeight,minHeight:style.minHeight,height:summary.getBoundingClientRect().height}},frameWidth);const actual=await frame.locator('summary').evaluate(node=>{const style=getComputedStyle(node);return {fontFamily:style.fontFamily,fontSize:style.fontSize,lineHeight:style.lineHeight,minHeight:style.minHeight,height:node.getBoundingClientRect().height}});if(JSON.stringify(actual)!==JSON.stringify(expected))throw Error('original typography/geometry changed: '+JSON.stringify({actual,expected,width}));await page.screenshot({path:`${evidence}/${phase}-${theme}-${width}.png`})}}
 const source=await page.locator(selector).getAttribute('src');await writeFile(evidence+'/report.json',JSON.stringify({phase,source,detectedAt,actualApp:true,managedFrame:true,expectedCaption,computedOriginalTypographyAndGeometry:true,inputEvents:await page.evaluate(()=>window.planInputEvents),themesAndWidths:[1400,390]},null,2));await context.close();
}catch(error){if(page){await page.screenshot({path:evidence+'/failure.png'});await writeFile(evidence+'/failure.json',JSON.stringify(await page.evaluate(()=>({events:window.planEvents,inputEvents:window.planInputEvents,frames:[...document.querySelectorAll('iframe')].map(node=>node.src),body:document.body.innerText.slice(-3000)})),null,2))}throw error}finally{await browser.close()}

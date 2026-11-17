import {chromium} from '../../guest-browser/node_modules/playwright-core/index.mjs';
import {mkdir,writeFile} from 'node:fs/promises';
import {waitForUIState} from '../../guest-browser/ui-state.mjs';
const[url,out,phase,ref]=process.argv.slice(2);await mkdir(out,{recursive:true});
const browser=await chromium.launch({headless:true,...(process.env.STEWARD_BROWSER_EXECUTABLE?{executablePath:process.env.STEWARD_BROWSER_EXECUTABLE}:{})});
try{
 const context=await browser.newContext({viewport:{width:1400,height:980},reducedMotion:'reduce'}),page=await context.newPage();page.setDefaultTimeout(10000);
 await page.goto(url);await page.getByLabel('Workspace access token',{exact:true}).fill(process.env.STEWARD_WEB_TEST_TOKEN);await page.getByRole('button',{name:'Open workspace'}).click();
 const outer=page.locator('section[data-view-ref='+JSON.stringify(ref)+']');await outer.waitFor();
 const frame=outer.frameLocator('iframe[title="Cards view"]'),cards=frame.locator('.native-carousel');await cards.waitFor();
 const image=cards.locator('img[src^="blob:"]');await image.waitFor();await image.evaluate(n=>{if(n.complete){if(!n.naturalWidth)throw Error('legacy image failed');return}return new Promise((resolve,reject)=>{const timer=setTimeout(()=>reject(Error('legacy image timed out')),5000);n.addEventListener('load',()=>{clearTimeout(timer);resolve()},{once:true});n.addEventListener('error',()=>{clearTimeout(timer);reject(Error('legacy image failed'))},{once:true})})});
 if(await image.evaluate(n=>n.naturalWidth)!==1)throw Error('legacy Artifact changed');
 const link=cards.getByRole('link',{name:'Details',exact:true});await link.waitFor();
 if(phase==='before'){await cards.getByRole('button',{name:'Next card',exact:true}).click();await waitForUIState(page,ref,state=>state.state.index===1);await cards.getByRole('button',{name:'Beta',exact:true}).click()}
 // No state update or selection is performed after the binary replacement.
 await cards.locator('.native-carousel-controls span').filter({hasText:'2 / 2'}).waitFor();await cards.locator('.native-card-title[aria-pressed=true]').filter({hasText:'Beta'}).waitFor();
 await page.waitForFunction(()=>document.querySelector('.answer-table tbody')?.textContent?.includes('Beta')&&!document.querySelector('.answer-table tbody')?.textContent?.includes('Alpha'));
 const state=await waitForUIState(page,ref,state=>state.state.index===1&&state.model_context.visible_item==='Beta');await waitForUIState(page,ref.replace(/#1$/,'#0'),state=>state.model_context.selection==='Beta');
 const table=page.locator('.answer-table');
 if(phase==='before'){
  await table.getByRole('button',{name:'Value',exact:true}).click();await table.getByRole('button',{name:'Value',exact:true}).click();
  await waitForUIState(page,ref.replace(/#1$/,'#0'),state=>state.state.sort===1&&state.state.direction===-1&&state.model_context.sort==='Value');
 }
 const tableHTML=await table.evaluate(node=>node.outerHTML);
 const tableState=await waitForUIState(page,ref.replace(/#1$/,'#0'),state=>state.state.sort===1&&state.state.direction===-1&&state.state.selected==='Beta');
 if(await page.locator('iframe[title="Table view"]').count())throw Error('historical Table adopted managed renderer');
 const html=await cards.evaluate(n=>{const copy=n.cloneNode(true);copy.querySelectorAll('img').forEach(image=>image.removeAttribute('src'));return copy.outerHTML});
 // Exercise the original authorized link after checking the recovered state.
 await context.route('https://example.com/**',route=>route.fulfill({contentType:'text/html',body:'Original destination'}));
 const pending=context.waitForEvent('page');await link.click();const popup=await pending;await popup.waitForURL('https://example.com/alpha');await popup.close();
 for(const theme of['light','dark']){await page.evaluate(theme=>document.documentElement.classList.toggle('dark',theme==='dark'),theme);await page.screenshot({path:out+'/native-'+theme+'.png'})}
 await writeFile(out+'/report.json',JSON.stringify({scope:'original managed UI1.13 Cards DOM Runtime snapshot; actual old/new Host comparison',html,state,tableHTML,tableState,originalImage:true,originalAuthorizedLinkOpened:true,groupFiltersActualTable:true,readOnlyRecovery:phase==='after'},null,2));await context.close();
}finally{await browser.close()}

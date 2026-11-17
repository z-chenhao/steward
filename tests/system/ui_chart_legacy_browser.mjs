import assert from 'node:assert/strict';
import {mkdir,readFile,writeFile} from 'node:fs/promises';
import {chromium} from '../../guest-browser/node_modules/playwright-core/index.mjs';
import {waitForUIState} from '../../guest-browser/ui-state.mjs';
const[url,out,phase,ref]=process.argv.slice(2);await mkdir(out,{recursive:true});
const browser=await chromium.launch({headless:true,...(process.env.STEWARD_BROWSER_EXECUTABLE?{executablePath:process.env.STEWARD_BROWSER_EXECUTABLE}:{})});
let page;
try{
 const context=await browser.newContext({viewport:{width:1400,height:980},reducedMotion:'reduce',acceptDownloads:true});page=await context.newPage();page.setDefaultTimeout(12000);await page.goto(url);
 await page.getByLabel('Workspace access token',{exact:true}).fill(process.env.STEWARD_WEB_TEST_TOKEN);await page.getByRole('button',{name:'Open workspace'}).click();
 const chart=page.locator('.answer-surface').filter({has:page.locator('.native-answer-title').filter({hasText:/^Trend$/})});await chart.locator('.recharts-bar-rectangle').nth(4).waitFor();
 assert.equal(await chart.locator('iframe').count(),0,'historical graph must keep native interpretation');
 const menu=async name=>{await chart.getByRole('button',{name:'Options',exact:true}).click();await page.getByRole('menuitem',{name,exact:true}).click()};
 if(phase==='before'){
  await menu('Zoom');await chart.locator('input[type=range]').nth(1).fill('3');await chart.locator('.recharts-bar-rectangle').nth(4).waitFor({state:'detached'});await waitForUIState(page,ref,state=>state.state.end===3);
  await page.reload();await chart.locator('.recharts-bar-rectangle').nth(4).waitFor();
 }
 assert.equal(await chart.locator('input[type=range]').count(),0);
 const states=await waitForUIState(page,ref,state=>state.state.end===3&&state.model_context.title==='Trend');
 await page.waitForFunction(()=>{const svg=[...document.querySelectorAll('svg.recharts-surface')].find(n=>n.querySelector('.recharts-bar-rectangle'));return svg&&Math.abs(svg.getBoundingClientRect().width-svg.closest('.recharts-responsive-container').clientWidth)<1});
 const geometry=await chart.locator('.recharts-bar-rectangle path').evaluateAll(nodes=>nodes.map(n=>n.getAttribute('d')));assert.equal(geometry.length,5);
 const pending=page.waitForEvent('download');await menu('Download CSV');const file=await pending;const csv=(await readFile(await file.path())).toString();assert.equal(csv,'\ufeff"Label","Value"\r\n"A","1"\r\n"B","2"\r\n"C","3"\r\n"D","4"\r\n"E","5"');
 await chart.screenshot({path:out+'/chart.png'});await writeFile(out+'/report.json',JSON.stringify({phase,geometry,states,csv,native:true},null,2));await context.close();
}catch(error){if(page){await page.screenshot({path:out+'/failure.png'});await writeFile(out+'/failure.txt',await page.locator('body').innerText())}throw error}finally{await browser.close()}

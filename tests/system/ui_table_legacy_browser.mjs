import assert from 'node:assert/strict';
import {mkdir,readFile,writeFile} from 'node:fs/promises';
import {chromium} from '../../guest-browser/node_modules/playwright-core/index.mjs';
import {waitForUIState} from '../../guest-browser/ui-state.mjs';
const[url,out,phase,ref]=process.argv.slice(2);await mkdir(out,{recursive:true});
const browser=await chromium.launch({headless:true,...(process.env.STEWARD_BROWSER_EXECUTABLE?{executablePath:process.env.STEWARD_BROWSER_EXECUTABLE}:{})});
try{
 const context=await browser.newContext({locale:'en-US',viewport:{width:1400,height:980},reducedMotion:'reduce',acceptDownloads:true}),page=await context.newPage();page.setDefaultTimeout(12000);
 await page.goto(url);await page.getByLabel('Workspace access token',{exact:true}).fill(process.env.STEWARD_WEB_TEST_TOKEN);await page.getByRole('button',{name:'Open workspace'}).click();
 const table=page.locator('.answer-table');await table.waitFor();assert.equal(await page.locator('iframe[title="Table view"]').count(),0);
 const outer=table.locator('xpath=ancestor::section[@data-openintelligentui="Table"]');
 const menu=async label=>{await outer.getByRole('button',{name:'Options',exact:true}).click();await outer.getByRole('menuitem',{name:label,exact:true}).click()};
 const groupRef=ref.replace(/#1$/,'#0');
 if(phase==='before'){
  await table.getByRole('button',{name:'Amount',exact:true}).click();await table.getByRole('button',{name:'Amount',exact:true}).click();
  await menu('Filter');await outer.getByRole('searchbox',{name:'Filter rows'}).fill('same');await table.getByRole('button',{name:'Alpha',exact:true}).click();
 }
 const saved=await waitForUIState(page,ref,state=>state.state.sort===1&&state.state.direction===-1&&state.model_context.sort==='Amount');
 const group=await waitForUIState(page,groupRef,state=>state.state.query==='same'&&state.state.selected==='Alpha');
 await table.locator('tbody').evaluate(node=>new Promise((resolve,reject)=>{const deadline=performance.now()+5000;const check=()=>node.rows.length===1?resolve():performance.now()>deadline?reject(Error('original native Table not restored')):setTimeout(check,25);check()}));
 const html=await table.evaluate(node=>node.outerHTML);
 const pending=page.waitForEvent('download');await menu('Download CSV');const file=await pending;const bytes=await readFile(await file.path());
 assert.deepEqual(bytes,Buffer.from('\ufeff"Name","Amount","Note"\r\n"Alpha","2","same"'));
 await page.screenshot({path:out+'/native-table.png'});
 await writeFile(out+'/report.json',JSON.stringify({html,states:{table:saved,group},csv:bytes.toString('base64'),readOnlyRecovery:phase==='after',scope:'actual retained UI1.13 native Table; no managed snapshot or renderer'},null,2));
 await context.close();
}finally{await browser.close()}

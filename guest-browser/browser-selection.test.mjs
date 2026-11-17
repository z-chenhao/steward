import {mkdtemp,readFile,realpath,rm,writeFile} from 'node:fs/promises';
import {tmpdir} from 'node:os';
import {join} from 'node:path';
import {execFile} from 'node:child_process';
import {promisify} from 'node:util';
import assert from 'node:assert/strict';
import test from 'node:test';
import {chromium} from 'playwright-core';
import {waitForDevToolsPort} from './readiness.mjs';
import {fileURLToPath} from 'node:url';
test('live browser selection proves exact original range without a truncated-source limit',{timeout:60000},async()=>{
const root=await realpath(await mkdtemp(join(tmpdir(),'steward-browser-selection-baseline-')));
const source=await readFile(new URL('../internal/computer/macosvm/browser.mjs',import.meta.url),'utf8');
const module=fileURLToPath(new URL('./node_modules/playwright-core/index.mjs',import.meta.url));
const exec=promisify(execFile);
const checks=[];
let browser;
try{
 browser=await chromium.launchPersistentContext(join(root,'profile'),{headless:true,args:['--remote-debugging-address=127.0.0.1','--remote-debugging-port=0']});
 const port=await waitForDevToolsPort(join(root,'profile'));
 const call=async(method,args={})=>JSON.parse((await exec(process.execPath,['--input-type=module','-e',source,JSON.stringify({module,port,root:join(root,'transfer'),method,args})],{timeout:15000,maxBuffer:12<<20})).stdout);
 const [{id:tabId}]=(await call('inventory')).tabs;
 const page=browser.pages()[0];
 await page.setContent('<input id="field" aria-label="Text">');
 for(const fixture of [
  {name:'ascii',value:'hello target suffix',wanted:'target',span:[6,12]},
  {name:'utf16-emoji-and-chinese',value:'🐱a target尾',wanted:'target',span:[4,10]},
  {name:'lone-surrogates-preserved-by-dom',value:'\ud800Xtarget\udfff',wanted:'target',span:[2,8]},
  {name:'full-live-value-beyond-observed-prefix',value:'x'.repeat(100000)+'target',wanted:'target',span:[100000,100006]},
  {name:'original-live-value-larger-than-plugin-input',value:'x'.repeat((8<<20)+1)+'target',wanted:'target',span:[(8<<20)+1,(8<<20)+7]},
  {name:'overlapping-only-second-match-retains-original-behavior',value:'aaab',wanted:'aa',span:[0,2]},
  {name:'duplicate-after-truncated-prefix',value:'target'+'x'.repeat(10000)+'target',wanted:'target',error:'UNKNOWN_SIDE_EFFECT'},
  {name:'non-overlapping-duplicate',value:'aaaa',wanted:'aa',error:'UNKNOWN_SIDE_EFFECT'},
  {name:'missing-source',value:'other',wanted:'target',error:'UNKNOWN_SIDE_EFFECT'},
 ]){
  await page.locator('#field').evaluate((el,value)=>{el.value=value;el.setSelectionRange(0,0);el.blur();},fixture.value);
  const observed=await call('observe',{tabId});
  const field=observed.elements.find(e=>e.label==='Text');
  const result=await call('selectText',{tabId,element_token:field.element_token,expectedNavigation:observed.navigation,value:fixture.wanted});
  const actual=await page.locator('#field').evaluate(el=>({start:el.selectionStart,end:el.selectionEnd,length:el.value.length,firstCodeUnit:el.value.charCodeAt(0),focused:document.activeElement===el}));
  if(fixture.error){assert.equal(result.error,fixture.error);assert.deepEqual([actual.start,actual.end],[0,0]);assert.equal(actual.focused,false);}
  else{assert.equal(result.effect,'dispatched');assert.deepEqual([actual.start,actual.end],fixture.span);assert.equal(actual.focused,true);}
  checks.push({name:fixture.name,observedLength:field.value.length,result,actual});
 }
 assert.equal(checks.length,9);
 if(process.env.STEWARD_BROWSER_SELECTION_EVIDENCE)await writeFile(process.env.STEWARD_BROWSER_SELECTION_EVIDENCE,JSON.stringify({scope:'unchanged fixed facade-v1 live selection primitive; exact original DOM value and node identity',playwright:'1.64.1',chromium:browser.browser()?.version(),checks},null,2)+'\n');
}finally{if(browser)await browser.close();await rm(root,{recursive:true,force:true,maxRetries:3,retryDelay:100});}

});

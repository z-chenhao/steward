import assert from 'node:assert/strict';
import {chromium} from 'playwright-core';
import {mkdir,writeFile} from 'node:fs/promises';
const[url,out]=process.argv.slice(2);await mkdir(out,{recursive:true});
const browser=await chromium.launch({headless:true,...(process.env.STEWARD_BROWSER_EXECUTABLE?{executablePath:process.env.STEWARD_BROWSER_EXECUTABLE}:{})});
try{
 const context=await browser.newContext({reducedMotion:'reduce'});
 assert.equal((await context.request.get(url+'/ui/cards?ref=ui:original%231')).status(),401);
 assert.equal((await context.request.post(url+'/session',{form:{token:'private-token-at-least-24-bytes'},maxRedirects:0})).status(),303);
 const page=await context.newPage();page.setDefaultTimeout(12000);
 await page.goto(url+'/fixture?ref='+encodeURIComponent('ui:original#1'));
 await page.waitForFunction(()=>messages.some(m=>m.type==='restored'));
 const frame=page.frameLocator('#cards');
 assert.equal(await frame.locator('.native-carousel-controls span').textContent(),'2 / 2');
 await frame.getByRole('button',{name:'Previous card',exact:true}).click();
 await page.waitForFunction(()=>messages.some(m=>m.type==='view-state'&&m.state.index===0));
 const isolation=await frame.locator('body').evaluate(()=>({origin:self.origin,domEscape:globalThis.DOM_ESCAPE,workerMarker:globalThis.WORKER_ONLY_MARKER,scripts:document.scripts.length,frames:document.querySelectorAll('iframe').length}));
 assert.equal(isolation.origin,'null');assert.equal(isolation.domEscape,undefined);assert.equal(isolation.workerMarker,undefined);assert.equal(isolation.frames,0);assert.equal(isolation.scripts,3);
 const rejected=[];
 for(const name of['invalid','unsolicited','loop']){
  // Observe close immediately on creation: a rejected Worker may terminate
  // before navigation finishes, so registering after goto loses the evidence.
  const spawned=new Promise(resolve=>page.once('worker',candidate=>{const state={candidate,closed:false};candidate.on('close',()=>state.closed=true);resolve(state)}));
  await page.goto(url+'/fixture?ref='+encodeURIComponent('ui:'+name+'#1'));
  const state=await spawned,candidate=state.candidate;
  await page.waitForFunction(()=>messages.some(m=>m.type==='failed'));
  assert.equal(await page.evaluate(()=>messages.some(m=>['select','view-state','image-request'].includes(m.type))),false);
  assert.equal(await page.evaluate(()=>1),1);
  // Chromium waits 2s before forcibly interrupting runaway script execution.
  // Allow shutdown/IPC margin; the Host watchdog remains unchanged.
  // https://raw.githubusercontent.com/chromium/chromium/main/third_party/blink/renderer/core/workers/worker_thread.cc
  if(!state.closed)await new Promise((resolve,reject)=>{const timer=setTimeout(()=>reject(Error(name+' failed Worker still running')),5000);candidate.once('close',()=>{clearTimeout(timer);resolve()})});
  assert.equal(await page.evaluate(()=>1),1);rejected.push(name);
 }
 await writeFile(out+'/report.json',JSON.stringify({scope:'actual protected Go HTTP/CSP, fixed DOM driver and owning Worker; accepted domain fixture, not managed App publication',browserVersion:browser.version(),isolation,originalRestoreAndPaging:true,rejected,failedWorkersTerminated:true,hostRemainsResponsive:true},null,2)+'\n');
 await context.close();
}finally{await browser.close()}

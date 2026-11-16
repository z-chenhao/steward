import {chromium} from 'playwright-core';
import {mkdir,writeFile} from 'node:fs/promises';
const[url,out]=process.argv.slice(2);await mkdir(out,{recursive:true});
const browser=await chromium.launch({headless:true,...(process.env.STEWARD_BROWSER_EXECUTABLE?{executablePath:process.env.STEWARD_BROWSER_EXECUTABLE}:{})});
try{
 const context=await browser.newContext({locale:'sv-SE',viewport:{width:1400,height:980},reducedMotion:'reduce'});
 if((await context.request.get(url+'/ui/table?ref=ui:original%231')).status()!==401)throw Error('anonymous Table allowed');
 if((await context.request.post(url+'/session',{form:{token:'private-token-at-least-24-bytes'},maxRedirects:0})).status()!==303)throw Error('session failed');
 const page=await context.newPage();page.setDefaultTimeout(12000);const spawned=page.waitForEvent('worker');await page.goto(url+'/fixture?ref='+encodeURIComponent('ui:original#1'));await spawned;
 await page.waitForFunction(()=>messages.some(m=>m.type==='restored'));
 const frame=page.frameLocator('#table');await frame.getByRole('button',{name:'Amount',exact:true}).click();await page.waitForFunction(()=>messages.some(m=>m.type==='view-state'&&m.state.sort===1&&m.state.direction===1));
 const rows=await frame.locator('tbody tr').allTextContents();if(JSON.stringify(rows)!==JSON.stringify(['Gamma=SUM(A1:A2)','Delta-3中文, "quoted"','Alpha2same','Beta10same','Alpha20@unsafe']))throw Error('owning Worker did not retain original sorting '+JSON.stringify(rows));
 const isolation=await frame.locator('body').evaluate(()=>({origin:self.origin,domEscape:globalThis.DOM_ESCAPE,workerMarker:globalThis.WORKER_ONLY_MARKER,scripts:document.scripts.length,frames:document.querySelectorAll('iframe').length}));
 if(isolation.origin!=='null'||isolation.domEscape||isolation.workerMarker||isolation.scripts!==1||isolation.frames)throw Error('candidate entered DOM '+JSON.stringify(isolation));
 // Only a real user key in the fixed DOM realm can request surface exit.
 await frame.getByRole('button',{name:'Amount',exact:true}).evaluate(n=>n.dispatchEvent(new KeyboardEvent('keydown',{key:'Escape',bubbles:true})));if(await page.evaluate(()=>messages.some(m=>m.type==='escape')))throw Error('synthetic Escape accepted');await frame.getByRole('button',{name:'Amount',exact:true}).focus();await page.keyboard.press('Escape');await page.waitForFunction(()=>messages.some(m=>m.type==='escape'));
 const rejected=[];
 for(const name of['invalid','unsolicited','loop']){
  const worker=page.waitForEvent('worker');await page.goto(url+'/fixture?ref='+encodeURIComponent('ui:'+name+'#1'));const candidate=await worker;let closed=false;candidate.on('close',()=>closed=true);
  await page.waitForFunction(()=>messages.some(m=>m.type==='failed'),null,{timeout:12000});
  await page.waitForFunction(()=>messages.every(m=>m.type!=='export-rows'&&m.type!=='select'&&m.type!=='view-state'));
  if(await page.evaluate(()=>1)!==1)throw Error('Host stopped responding');
  // Chromium gives runaway Workers 2s before force termination; allow IPC margin.
  // The production Host command watchdog is unchanged.
  if(!closed)await new Promise((resolve,reject)=>{const timer=setTimeout(()=>reject(Error('failed Worker not terminated')),5000);candidate.once('close',()=>{clearTimeout(timer);resolve()})});
  if(await page.evaluate(()=>1)!==1)throw Error('candidate blocked Host realm');rejected.push(name);
 }
 await writeFile(out+'/report.json',JSON.stringify({scope:'actual protected HTTP/CSP, fixed DOM driver and owning Worker; fixture accepted domain, not managed App publication',browserVersion:browser.version(),owningSort:true,isolation,syntheticEscapeRejected:true,trustedEscape:true,rejected,failedWorkersTerminated:true,hostRemainsResponsive:true},null,2));await context.close();
}finally{await browser.close()}

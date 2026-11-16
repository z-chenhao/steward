// Host-owned read-only original-document primitive. Candidate sees counts only.
import fs from 'node:fs';
import {pathToFileURL} from 'node:url';
const request=JSON.parse(process.argv[1]);
const a=request.args;
let browser;
const fail=code=>{throw Object.assign(new Error(code),{code});};
async function navigation(page){
 const session=await page.context().newCDPSession(page);
 try{return JSON.stringify([(await session.send('Page.getFrameTree')).frameTree.frame.loaderId,page.url()]);}
 finally{await session.detach();}
}
const units=text=>{
 const bytes=Buffer.alloc(text.length*2);
 for(let i=0;i<text.length;i++)bytes.writeUInt16LE(text.charCodeAt(i),i*2);
 return bytes.toString('base64');
};
async function main(){
 if(request.method!=='observe'||!Number.isInteger(request.port)||request.port<1024||request.port>65535||typeof a.tabId!=='string'||!a.tabId||typeof a.ax!=='boolean'||typeof a.screenshot!=='boolean')fail('INVALID_ARGUMENT');
 const manifest=JSON.parse(fs.readFileSync(new URL('./package.json',pathToFileURL(request.module)),'utf8'));
 if(manifest.name!=='playwright-core'||manifest.version!=='1.64.1')fail('PROVIDER_UNAVAILABLE');
 const {chromium}=await import(request.module);
 browser=await chromium.connectOverCDP(`http://127.0.0.1:${request.port}`,{timeout:5000});
 const context=browser.contexts()[0];
 if(!context)fail('PROVIDER_UNAVAILABLE');
 let page;
 for(const candidate of context.pages()){
  const session=await context.newCDPSession(candidate);
  try{if((await session.send('Target.getTargetInfo')).targetInfo.targetId===a.tabId)page=candidate;}
  finally{await session.detach();}
 }
 if(!page)fail('STALE_SURFACE_HANDLE');
 page.setDefaultTimeout(3000);
 const nav=await navigation(page);
 const document=(await page.evaluateHandle(()=>globalThis.document)).asElement();
 if(!document||await navigation(page)!==nav)fail('STALE_SURFACE_HANDLE');
 // Fixed acquisition ceilings are the existing private disclosure/resource
 // boundary. No label priority or display clipping is chosen in this program.
 const collect=(doc)=>{
  if(!doc.isConnected)throw new Error('STALE_SURFACE_HANDLE');
  const elements=[...doc.querySelectorAll('button,input:not([type=password]):not([type=hidden]),textarea,select,a,[role=button]')]
   .filter(el=>!(el.tagName==='INPUT'&&['password','hidden'].includes(el.type))&&el.getClientRects().length>0).slice(0,500);
  function css(el){
   const parts=[];
   while(el&&el.nodeType===1){
    const siblings=el.parentElement?[...el.parentElement.children].filter(s=>s.tagName===el.tagName):[el];
    parts.unshift(el.tagName.toLowerCase()+`:nth-of-type(${siblings.indexOf(el)+1})`);el=el.parentElement;
   }
   return parts.join(' > ');
  }
  return elements.map((el,index)=>({index,selector:css(el),role:el.tagName,aria:(el.getAttribute('aria-label')||'').slice(0,256),inner:(el.innerText||'').slice(0,256),placeholder:(el.getAttribute('placeholder')||'').slice(0,256),value:typeof el.value==='string'?el.value.slice(0,4096):undefined}));
 };
 const rows=a.ax?await document.evaluate(collect):[];
 let body='';
 if(a.ax){const element=await document.waitForSelector('body',{timeout:3000});try{body=(await element.innerText()).slice(0,16384);}finally{await element.dispose();}}
 const elements=[];
 if(a.ax){
  const dom=await context.newCDPSession(page);
  try{
   if(await navigation(page)!==nav)fail('STALE_SURFACE_HANDLE');
   const {root}=await dom.send('DOM.getDocument');
   for(const row of rows){
    const {nodeId}=await dom.send('DOM.querySelector',{nodeId:root.nodeId,selector:row.selector});
    if(!nodeId)fail('STALE_ELEMENT_REFERENCE');
    const {node}=await dom.send('DOM.describeNode',{nodeId});
    elements.push({index:row.index,element_token:JSON.stringify({selector:row.selector,backendNodeId:node.backendNodeId}),actions:[],role:row.role,value:row.value});
   }
  }finally{await dom.detach();}
  // Original source and the newly published references must describe the same
  // bounded document snapshot, including same-document element replacement.
  if(JSON.stringify(await document.evaluate(collect))!==JSON.stringify(rows))fail('STALE_ELEMENT_REFERENCE');
 }
 const viewport=await document.evaluate(doc=>{
  if(!doc.isConnected)throw new Error('STALE_SURFACE_HANDLE');
  const w=doc.defaultView;return {x:w.scrollX,y:w.scrollY,width:w.innerWidth,height:w.innerHeight,scale:w.devicePixelRatio};
 });
 const png=a.screenshot?await page.screenshot({type:'png',scale:'css',timeout:10000}):undefined;
 if(await navigation(page)!==nav||!await document.evaluate(doc=>doc.isConnected))fail('STALE_SURFACE_HANDLE');
 return {elements,navigation:nav,window_bounds:viewport,geometry:JSON.stringify(viewport),screenshot_width:viewport.width,screenshot_height:viewport.height,screenshot_png_b64:png?.toString('base64'),source:{version:1,body:units(body),rows:rows.map(r=>({role:units(r.role),aria:units(r.aria),inner:units(r.inner),placeholder:units(r.placeholder),...(r.value===undefined?{}:{value:units(r.value)})}))}};
}
try{process.stdout.write(JSON.stringify(await main()));}
catch(error){
 const expired=/Execution context was destroyed|Cannot find context with specified id|STALE_SURFACE_HANDLE/.test(String(error.message));
 process.stdout.write(JSON.stringify({error:['INVALID_ARGUMENT','STALE_SURFACE_HANDLE','STALE_ELEMENT_REFERENCE'].includes(error.code)?error.code:expired?'STALE_SURFACE_HANDLE':'PROVIDER_UNAVAILABLE'}));
}finally{if(browser)await browser.close();}

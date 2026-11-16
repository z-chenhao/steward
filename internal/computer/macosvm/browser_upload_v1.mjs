// Fixed Host-owned FileChooser primitive. Business name/MIME choices arrive
// already approved; no candidate source, endpoint or evaluator enters here.
import fs from 'node:fs';
import {pathToFileURL} from 'node:url';
import {createHash} from 'node:crypto';
import {spawnSync} from 'node:child_process';
const request=JSON.parse(process.argv[1]);
const a=request.args;
let browser,document;
let mutating=false,clicked=false;
const fail=code=>{throw Object.assign(new Error(code),{code});};
async function navigation(page){
 const session=await page.context().newCDPSession(page);
 try{return JSON.stringify([(await session.send('Page.getFrameTree')).frameTree.frame.loaderId,page.url()]);}
 finally{await session.detach();}
}
async function main(){
 if(request.method!=='playwright.upload'||!Number.isInteger(request.port)||request.port<1024||request.port>65535)fail('INVALID_ARGUMENT');
 if(typeof a.selector!=='string'||!a.selector||a.selector.length>2048||a.selector.includes('>>')||!Number.isInteger(a.timeoutMs)||a.timeoutMs<1||a.timeoutMs>10000||typeof a.name!=='string'||!a.name||typeof a.mediaType!=='string'||!a.mediaType)fail('INVALID_ARGUMENT');
 const manifest=JSON.parse(fs.readFileSync(new URL('./package.json',pathToFileURL(request.module)),'utf8'));
 if(manifest.name!=='playwright-core'||manifest.version!=='1.64.1')fail('PROVIDER_UNAVAILABLE');
 const helper=fs.readFileSync(new URL('./steward-transfer.py',pathToFileURL(request.module)),'utf8');
 if(createHash('sha256').update(helper).digest('hex')!==request.helper_sha256)fail('PERMISSION_DENIED');
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
 page.setDefaultTimeout(a.timeoutMs);
 const nav=await navigation(page);
 if(!a.expectedNavigation||a.expectedNavigation!==nav)fail('STALE_SURFACE_HANDLE');
 document=(await page.evaluateHandle(()=>globalThis.document)).asElement();
 if(!document||await navigation(page)!==nav)fail('STALE_SURFACE_HANDLE');
 const deadline=Date.now()+a.timeoutMs;
 const remaining=()=>Math.max(1,deadline-Date.now());
 const transfer=spawnSync('/usr/bin/python3',['-c',helper],{input:JSON.stringify({root:request.root,path:a.path,operation:'read',overwrite:false,data:''}),encoding:'utf8',maxBuffer:12<<20,timeout:10000});
 let original;try{original=JSON.parse(transfer.stdout);}catch{fail('PERMISSION_DENIED');}
 if(transfer.status!==0||original.error)fail('PERMISSION_DENIED');
 const data=Buffer.from(original.data,'base64');
 const chooser=page.waitForEvent('filechooser',{timeout:10000});
 // The pending event also has an owner when selector/actionability fails.
 chooser.catch(()=>{});
 mutating=true;
 while(true){
  const element=await document.waitForSelector(`css=${a.selector}`,{state:'attached',strict:true,timeout:remaining()});
  try{await element.click({timeout:remaining()});clicked=true;break;}
  catch(error){
   if(!/Element is not attached to the DOM/.test(String(error.message))||Date.now()>=deadline)throw error;
   if(!await document.evaluate(node=>node.isConnected))fail('UNKNOWN_SIDE_EFFECT');
  }finally{await element.dispose();}
 }
 const selected=await chooser;
 // A completed click does not authorize a later document to receive bytes.
 // Its outcome remains unknown if the original document vanishes meanwhile.
 if(await navigation(page)!==nav||!await document.evaluate(node=>node.isConnected))fail('UNKNOWN_SIDE_EFFECT');
 await selected.setFiles({name:a.name,mimeType:a.mediaType,buffer:data});
 return {effect:'dispatched'};
}
try{process.stdout.write(JSON.stringify(await main()));}
catch(error){
 const known=['INVALID_ARGUMENT','PERMISSION_DENIED','STALE_SURFACE_HANDLE'];
 const expired=document&&/Execution context was destroyed|Cannot find context with specified id|Element is not attached to the DOM/.test(String(error.message));
 process.stdout.write(JSON.stringify({error:known.includes(error.code)?error.code:expired&&!clicked?'STALE_SURFACE_HANDLE':mutating?'UNKNOWN_SIDE_EFFECT':'PROVIDER_UNAVAILABLE'}));
}
finally{if(browser)await browser.close();}

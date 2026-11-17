import test from 'node:test';
import assert from 'node:assert/strict';
import {mkdtemp,realpath,readFile,rm,writeFile} from 'node:fs/promises';
import {tmpdir} from 'node:os';
import {join} from 'node:path';
import {fileURLToPath} from 'node:url';
import {execFile} from 'node:child_process';
import {promisify} from 'node:util';
import {createHash} from 'node:crypto';
import {chromium} from 'playwright-core';
import {waitForDevToolsPort} from './readiness.mjs';
const exec=promisify(execFile);
const source=await readFile(new URL('../internal/computer/macosvm/browser_snapshot_v1.mjs',import.meta.url),'utf8');
const legacy=await readFile(new URL('../internal/computer/macosvm/browser.mjs',import.meta.url),'utf8');
const observed=JSON.parse(await readFile(new URL('../tests/fixtures/computer-browser-projection.json',import.meta.url),'utf8'));
const text=raw=>Buffer.from(raw,'base64').toString('utf16le');

test('fixed browser snapshot: original UTF16, bounded sources, identities, screenshot and private input exclusion',{timeout:60000},async()=>{
 const root=await realpath(await mkdtemp(join(tmpdir(),'steward-browser-projection-')));
 const module=fileURLToPath(new URL('./node_modules/playwright-core/index.mjs',import.meta.url));
 let context;
 try{
  context=await chromium.launchPersistentContext(join(root,'profile'),{headless:true,args:['--remote-debugging-address=127.0.0.1','--remote-debugging-port=0']});
  const port=await waitForDevToolsPort(join(root,'profile'));
  const page=context.pages()[0];
  await page.setContent('<body>原文🧭<input id="value" aria-label="优先标签" placeholder="占位标签"><button id="button">提交</button><textarea id="empty" placeholder="empty"></textarea></body>');
  await page.locator('#value').evaluate(el=>{el.value='a\ud800b🧭\n';el.setAttribute('aria-label','名'.repeat(255)+'🧭');});
  const call=async(program,args)=>JSON.parse((await exec(process.execPath,['--input-type=module','-e',program,JSON.stringify({method:'observe',args:{tabId,ax:true,screenshot:false,...args},module,port})],{timeout:16000,maxBuffer:12<<20})).stdout);
  const session=await context.newCDPSession(page);const tabId=(await session.send('Target.getTargetInfo')).targetInfo.targetId;await session.detach();
  const original=await call(legacy,{});
  const captured=await call(source,{});assert(!captured.error,JSON.stringify(captured));
  assert.equal(captured.navigation,original.navigation);
  assert.equal(captured.source.rows.length,3);
  assert.equal(text(captured.source.rows[0].aria).length,256);
  assert.equal(text(captured.source.rows[0].aria).charCodeAt(255),0xd83e);
  assert.equal(text(captured.source.rows[0].value).charCodeAt(1),0xd800,'isolated original code unit was normalized');
  const observedDefault=text(captured.source.body)+'\n'+captured.source.rows.map((r,i)=>`[${i}] ${text(r.role)} ${JSON.stringify(text(r.aria)||text(r.inner)||text(r.placeholder)||text(r.role).slice(0,256))}${r.value===undefined?'':' = '+JSON.stringify(text(r.value))}`).join('\n');
  assert.equal(observedDefault,original.tree_markdown,'structured source cannot reproduce frozen facade-v1 display');
  assert.equal(observed.program_sha256,createHash('sha256').update(source).digest('hex'));
  assert.deepEqual(captured.source,observed.source,'recorded official-artifact source drifted');
  assert.equal(original.tree_markdown,observed.expected);
  assert.equal(context.browser()?.version(),observed.chromium);
  const before=captured.elements.map(e=>JSON.parse(e.element_token));
  assert(before.every(t=>Number.isInteger(t.backendNodeId)&&t.selector));
  await page.locator('#value').evaluate(el=>el.replaceWith(el.cloneNode(true)));
  const replacement=await call(source,{});assert.notEqual(JSON.parse(replacement.elements[0].element_token).backendNodeId,before[0].backendNodeId,'replaced element reused old identity');
  await page.evaluate(()=>{
   for(const type of ['password','hidden']){
    const input=document.createElement('input');input.type=type;input.setAttribute('role','button');input.value='PRIVATE_SECRET_'+type;input.setAttribute('aria-label','PRIVATE_LABEL_'+type);document.body.append(input);
   }
  });
  const privateSafe=await call(source,{});
  assert.equal(privateSafe.elements.length,3,'role selector bypassed input disclosure fence');
  assert(!JSON.stringify(privateSafe).includes('PRIVATE_'));
  for(const row of privateSafe.source.rows)assert(![row.aria,row.inner,row.placeholder,row.value].some(v=>v!==undefined&&text(v).includes('PRIVATE_')));
  const shot=await call(source,{ax:false,screenshot:true});
  assert.deepEqual(shot.elements,[]);assert.deepEqual(shot.source.rows,[]);assert.equal(text(shot.source.body),'');
  assert(Buffer.from(shot.screenshot_png_b64,'base64').subarray(0,8).equals(Buffer.from([137,80,78,71,13,10,26,10])));
  const missing=JSON.parse((await exec(process.execPath,['--input-type=module','-e',source,JSON.stringify({method:'observe',args:{tabId:'missing',ax:true,screenshot:false},module,port})],{timeout:16000})).stdout);
  assert.equal(missing.error,'STALE_SURFACE_HANDLE');
  if(process.env.STEWARD_BROWSER_PROJECTION_EVIDENCE)await writeFile(process.env.STEWARD_BROWSER_PROJECTION_EVIDENCE,JSON.stringify({playwright:'1.64.1',chromium:context.browser()?.version(),program_sha256:createHash('sha256').update(source).digest('hex'),source:captured.source,expected:original.tree_markdown,checks:['default matches frozen facade-v1 byte for byte','isolated and clipped UTF16 retained','same-document replacement changes native identity','password/hidden role=button cannot bypass disclosure fence','screenshot-only omits source data','unknown original tab rejected']},null,2)+'\n');
 }finally{if(context)await context.close();await rm(root,{recursive:true,force:true,maxRetries:3,retryDelay:100});}
});

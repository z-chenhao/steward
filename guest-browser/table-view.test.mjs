import assert from 'node:assert/strict';
import {readFile} from 'node:fs/promises';
import test from 'node:test';
import {chromium} from 'playwright-core';

const original=JSON.parse(await readFile(new URL('../internal/plugin/intelligentui/testdata/original-table.json',import.meta.url),'utf8'));
const chinese=JSON.parse(await readFile(new URL('../internal/interaction/web/locales/zh-CN.json',import.meta.url),'utf8'));
const worker=await readFile(new URL('../internal/plugin/intelligentui/view/table-worker.js',import.meta.url),'utf8');
const driver=await readFile(new URL('../internal/interaction/web/view/table-driver.js',import.meta.url),'utf8');
const produced=process.env.STEWARD_TEST_TABLE_RESULT?JSON.parse(await readFile(process.env.STEWARD_TEST_TABLE_RESULT,'utf8')):null;

for(const sample of[{locale:'en-US',props:original.props,stages:original.stages},...original.locale_cases]){
 test('Table owning Worker preserves original DOM and ordering: '+(sample.id??sample.locale),{timeout:30000},async()=>{
  const browser=await chromium.launch({headless:true,...(process.env.STEWARD_BROWSER_EXECUTABLE?{executablePath:process.env.STEWARD_BROWSER_EXECUTABLE}:{})});
  try{
   const context=await browser.newContext({locale:sample.locale}),page=await context.newPage();page.setDefaultTimeout(5000);
   await page.setContent('<iframe id="table" sandbox="allow-scripts" style="width:800px;height:600px"></iframe><iframe id="sibling" sandbox="allow-scripts"></iframe>');
   let html=produced?.[sample.id??sample.locale]?.html;
   if(!html){const icon=sample.stages.find(s=>s.name==='amount-ascending').html.match(/<svg[^>]*>[\s\S]*?<\/svg>/)[0];html=sample.stages[0].html.replace('<section ', '<section data-table-facts="'+Buffer.from(JSON.stringify(sample.props)).toString('base64')+'" ');const end=html.lastIndexOf('</section>');html=html.slice(0,end)+'<template data-table-sort-icon>'+icon+'</template>'+html.slice(end)}
   await page.evaluate(({html,driver,code})=>{window.messages=[];window.siblingSent=false;addEventListener('message',event=>{if(event.source===document.querySelector('#sibling').contentWindow&&event.data==='sibling-sent')window.siblingSent=true;if(event.source===document.querySelector('#table').contentWindow&&event.origin==='null')messages.push(event.data)});document.querySelector('#table').srcdoc='<!doctype html><html lang="en" data-compact-max-width="760"><head><meta http-equiv="Content-Security-Policy" content="default-src \'none\'; script-src \'nonce-table-test\'; worker-src blob:; connect-src \'none\'; img-src \'none\'; frame-src \'none\'; form-action \'none\'; base-uri \'none\'"></head><body>'+html+'<script nonce="table-test">('+driver+')('+JSON.stringify(code).replaceAll('<','\\u003c')+');</script></body></html>'},{html,driver,code: 'const originalPost=postMessage;postMessage=value=>value.type==="restored"?setTimeout(()=>originalPost(value),80):originalPost(value);\n'+(produced?.[sample.id??sample.locale]?.worker??worker)});
   await page.waitForFunction(()=>messages.some(m=>m.type==='ready'));
   const frame=page.frameLocator('#table');
   const post=value=>page.evaluate(value=>document.querySelector('#table').contentWindow.postMessage({channel:'steward-table-view',...value},'*'),value);
   await post({type:'theme',locale:await page.evaluate(()=>Intl.Collator().resolvedOptions().locale),language:'en'});
   let restored=false,barrier=0;
   const settle=async()=>{if(!restored)return;const id='barrier-'+(++barrier);await post({type:'capture',id});await page.waitForFunction(id=>messages.some(m=>m.id===id),id)};
   const canonical=markup=>page.evaluate(markup=>{const doc=new DOMParser().parseFromString(markup,'text/html');const walk=n=>n.nodeType===1?[n.tagName,[...n.attributes].map(a=>[a.name,a.value]).sort((a,b)=>a[0].localeCompare(b[0])),[...n.childNodes].map(walk)]:[n.nodeType,n.textContent];return walk(doc.body.firstElementChild)},markup);
   const check=async name=>{await settle();const expected=sample.stages.find(s=>s.name===name);assert.ok(expected,'independent original stage '+name);assert.deepEqual(await frame.locator('tbody tr').allTextContents(),expected.rows,name+' raw textContent preserves CRLF/NUL before DOM parser normalization');assert.deepEqual(await canonical(await frame.locator('.answer-table').evaluate((n,restored)=>{const copy=n.cloneNode(true);if(!restored)copy.querySelectorAll('button[disabled]').forEach(button=>button.removeAttribute('disabled'));return copy.outerHTML},restored)),await canonical(expected.html),name+' exact original DOM, including numeric/ARIA and visible order')};
   assert.equal(await frame.getByRole('button',{name:'Amount',exact:true}).isDisabled(),true,'rendered control stays disabled until restore');await frame.getByRole('button',{name:'Amount',exact:true}).evaluate(button=>button.click());assert.equal(await page.evaluate(()=>messages.some(m=>m.type==='view-state')),false,'no interaction before restore');
   await page.locator('#sibling').evaluate(node=>{node.srcdoc='<script>parent.frames[0].postMessage({channel:"steward-table-view",type:"restore",state:{sort:1,direction:-1},query:"same"},"*");parent.postMessage("sibling-sent","*")</script>'});
   await page.waitForFunction(()=>siblingSent);await frame.locator('body').evaluate(()=>new Promise(resolve=>requestAnimationFrame(()=>requestAnimationFrame(resolve))));assert.equal(await page.evaluate(()=>messages.some(m=>m.type==='restored')),false);await check('initial');
   // Only the trusted parent can finish restore.
   await post({type:'capture',id:'before-restore'});
   await page.evaluate(()=>{const frame=document.querySelector('#table').contentWindow;for(const value of[{type:'restore',state:{},query:'same',selected:''},{type:'group',query:'no-original-row-matches',selected:''},{type:'group',query:'',selected:''}])frame.postMessage({channel:'steward-table-view',...value},'*')});await page.waitForFunction(()=>messages.some(m=>m.type==='restored'));restored=true;assert.deepEqual(await frame.locator('tbody tr').allTextContents(),sample.stages[0].rows,'first restored acknowledgement includes latest group received during delayed Worker restore');await check('initial');
   if(sample.locale!=='en-US'){await frame.getByRole('button',{name:'Name',exact:true}).click();await page.waitForFunction(()=>messages.some(m=>m.type==='view-state'&&m.state.sort===0));await check('name-ascending')}
   await frame.getByRole('button',{name:'Amount',exact:true}).click();await page.waitForFunction(()=>messages.some(m=>m.type==='view-state'&&m.state.sort===1&&m.state.direction===1));await check('amount-ascending');
   await frame.getByRole('button',{name:'Amount',exact:true}).click();await page.waitForFunction(()=>messages.some(m=>m.type==='view-state'&&m.state.sort===1&&m.state.direction===-1));await check('amount-descending');
   await post({type:'group',query:'same',selected:''});await frame.locator('tbody tr').nth(1).waitFor();await check('query-same');
   await post({type:'export',id:'original-csv-rows'});await page.waitForFunction(()=>messages.some(m=>m.id==='original-csv-rows'));assert.deepEqual((await page.evaluate(()=>messages.find(m=>m.id==='original-csv-rows'))).rows,[0,1]);
   await post({type:'reset'});await page.waitForFunction(()=>messages.some(m=>m.type==='reset-group'));await check('reset');
   await frame.getByRole('button',{name:'Alpha',exact:true}).first().click();await page.waitForFunction(()=>messages.some(m=>m.type==='select'&&m.row===1));await check('reset');
   await post({type:'group',query:'',selected:'Alpha'});await check('selected-alpha');
   if(sample.id==='en-US-text-boundaries'){await post({type:'group',query:'\r\n',selected:''});await settle();assert.deepEqual(await frame.locator('tbody tr').allTextContents(),['Text boundaries\uFEFF0x10line\r\nbreak \u0000end']);}
   await post({type:'group',query:'no-original-row-matches',selected:''});await frame.getByText('No rows to display.',{exact:true}).waitFor();assert.equal(await frame.locator('tbody tr').count(),0);await post({type:'export',id:'empty-export'});await page.waitForFunction(()=>messages.some(m=>m.id==='empty-export'));assert.deepEqual((await page.evaluate(()=>messages.find(m=>m.id==='empty-export'))).rows,[]);
   await post({type:'theme',language:'zh-CN'});await frame.getByText(chinese['No rows to display.'],{exact:true}).waitFor();assert.equal(await frame.locator('[role=region]').getAttribute('aria-label'),sample.props.title+' '+chinese.table);await post({type:'theme',language:'en'});
   await post({type:'reset'});await post({type:'capture',id:'final-reset'});await page.waitForFunction(()=>messages.some(m=>m.id==='final-reset'));await check('final-reset');
   await post({type:'restore',state:{sort:1,direction:-1},query:'same'});await post({type:'capture',id:'after-stale-restore'});await page.waitForFunction(()=>messages.some(m=>m.id==='after-stale-restore'));assert.deepEqual((await page.evaluate(()=>messages.find(m=>m.id==='after-stale-restore'))).state,{sort:-1,direction:1});
   assert.equal(await page.evaluate(()=>messages.some(m=>'model_context' in m||'ref' in m||'bytes' in m)),false,'runtime never authors model text, ref or arbitrary download bytes');
   await context.close();
  }finally{await browser.close()}
 });
}

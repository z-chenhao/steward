import assert from 'node:assert/strict';
import test from 'node:test';
import {createServer} from 'node:http';
import {fileURLToPath} from 'node:url';
import {chromium} from 'playwright-core';
import {build} from '../web/node_modules/esbuild/lib/main.js';

test('capability save keeps an edit that reverses the pending write',async()=>{
 const bundle=await build({entryPoints:[fileURLToPath(new URL('../web/src/capability-forms.ts',import.meta.url))],bundle:true,write:false,globalName:'capabilities'});
 const browser=await chromium.launch({headless:true,...(process.env.STEWARD_BROWSER_EXECUTABLE?{executablePath:process.env.STEWARD_BROWSER_EXECUTABLE}:{})});
 let server;
 try{
  const page=await browser.newPage();
  let value='A',version=1,release;
  const writes=[];
  let started;const received=new Promise(resolve=>{started=resolve});
  server=createServer(async(request,response)=>{
   if(request.method==='POST'){
    let raw='';for await(const chunk of request)raw+=chunk;
    const body=new URLSearchParams(raw);
    writes.push([body.get('root'),body.get('revision')]);
    if(writes.length===1)await new Promise(resolve=>{release=resolve;started()});
    value=body.get('root');version++;
    response.writeHead(303,{location:'/settings/capabilities/execution'});return response.end();
   }
   response.setHeader('Content-Type','text/html');response.end(`<form action="/capability/configure" method="post" data-capability-form><input name="enabled" type="checkbox" checked><input name="capability" value="execution"><input name="revision" value="${version}"><div data-capability-fields><input name="root" value="${value}"></div><div class="capability-submit"><button name="action" value="apply">Save</button></div><p data-capability-draft></p></form>`);
  });
  await new Promise(resolve=>server.listen(0,'127.0.0.1',resolve));
  await page.goto('http://127.0.0.1:'+server.address().port+'/settings/capabilities/execution');
  assert.equal(await page.getByRole('button',{name:'Save'}).isVisible(),true);
  await page.addScriptTag({content:bundle.outputFiles[0].text});await page.evaluate(()=>capabilities.enhanceCapabilityForms());
  assert.equal(await page.getByRole('button',{name:'Save'}).count(),0);
  const field=page.locator('input[name=root]');
  await field.fill('B');await field.press('Tab');await received;
  await page.waitForFunction(()=>document.querySelector('form').getAttribute('aria-busy')==='true');
  await field.fill('A');await field.press('Tab');release();
  await page.getByText('Saved.',{exact:true}).waitFor();
  assert.deepEqual(writes,[['B','1'],['A','2']]);
  await page.reload();assert.equal(await field.inputValue(),'A');
 }finally{await browser.close();server?.close()}
});

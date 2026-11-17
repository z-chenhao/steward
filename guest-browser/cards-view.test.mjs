import assert from 'node:assert/strict';
import {readFile} from 'node:fs/promises';
import test from 'node:test';
import {chromium} from 'playwright-core';

test('Cards owning Worker retains original state and group semantics in an opaque frame',{timeout:45000},async()=>{
  const original=JSON.parse(await readFile(new URL('../internal/plugin/intelligentui/testdata/original-cards.json',import.meta.url),'utf8'));
  const runtime=await readFile(new URL('../internal/plugin/intelligentui/view/cards-worker.js',import.meta.url),'utf8');
  const driver=await readFile(new URL('../internal/interaction/web/view/cards-driver.js',import.meta.url),'utf8');
  const styles=await readFile(new URL('../web/src/intelligent-ui.css',import.meta.url),'utf8');
  const browser=await chromium.launch({headless:true,...(process.env.STEWARD_BROWSER_EXECUTABLE?{executablePath:process.env.STEWARD_BROWSER_EXECUTABLE}:{})});
  try{
    const page=await browser.newPage({viewport:{width:1400,height:980}});
    await page.setContent('<iframe id="cards" sandbox="allow-scripts" style="width:800px;height:500px"></iframe><iframe id="sibling" sandbox="allow-scripts"></iframe>');
    await page.evaluate(({html,styles,runtime,driver})=>{
      window.messages=[];window.siblingSent=false;
      addEventListener('message',event=>{if(event.source===document.querySelector('#sibling').contentWindow&&event.data==='sibling-sent')window.siblingSent=true});
      addEventListener('message',event=>{if(event.source===document.querySelector('#cards').contentWindow&&event.origin==='null')window.messages.push(event.data)});
      document.querySelector('#cards').srcdoc='<meta http-equiv="Content-Security-Policy" content="default-src \'none\'; script-src \'nonce-cards\'; worker-src blob:; style-src \'unsafe-inline\'; connect-src \'none\'"><style>'+styles+'</style>'+html+'<script nonce="cards">('+driver+')('+JSON.stringify(runtime).replaceAll('<','\\u003c')+');</script>';
    },{html:original.initial_html,styles,runtime,driver});
    await page.waitForFunction(()=>window.messages.some(m=>m.type==='ready'));
    const frame=page.frameLocator('#cards');
    assert.equal(await frame.getByRole('button',{name:'Next card',exact:true}).isDisabled(),true);
    // A sibling can send to the opaque frame, but cannot restore its state.
    await page.locator('#sibling').evaluate(node=>{node.srcdoc='<script>parent.frames[0].postMessage({channel:"steward-cards-view",type:"restore",state:{index:1},selected:"Beta"},"*");parent.postMessage("sibling-sent","*")</script>'});
    await page.waitForFunction(()=>window.siblingSent);
    await frame.getByRole('button',{name:'Next card',exact:true}).isDisabled().then(value=>assert.equal(value,true));
    const post=value=>page.evaluate(value=>document.querySelector('#cards').contentWindow.postMessage({channel:'steward-cards-view',...value},'*'),value);
    await post({type:'restore',state:{index:1},selected:'Beta'});
    await page.waitForFunction(()=>window.messages.some(m=>m.type==='restored'));
    assert.equal(await frame.locator('.native-carousel').evaluate(node=>node.outerHTML),original.selected_html);
    await frame.getByRole('button',{name:'Previous card',exact:true}).click();
    await page.waitForFunction(()=>window.messages.some(m=>m.type==='view-state'&&m.state.index===0));
    const saved=await page.evaluate(()=>window.messages.filter(m=>m.type==='view-state').at(-1));
    assert.deepEqual(saved.state,{index:0});
    assert.equal('model_context' in saved,false);
    await frame.getByRole('button',{name:'Alpha',exact:true}).click();
    await page.waitForFunction(()=>window.messages.some(m=>m.type==='select'&&m.item===0));
    // Selection is accepted by Host first; the frame does not forge its own receipt.
    assert.equal(await frame.getByRole('button',{name:'Beta',exact:true}).getAttribute('aria-pressed'),'true');
    await post({type:'group',selected:'Alpha'});
    await frame.locator('.native-card-title[aria-pressed="true"]').filter({hasText:'Alpha'}).waitFor();
    await post({type:'capture',id:'flush-original-state'});
    await page.waitForFunction(()=>window.messages.some(m=>m.id==='flush-original-state'));
    const captured=await page.evaluate(()=>window.messages.find(m=>m.id==='flush-original-state'));
    assert.deepEqual(captured.state,{index:0});
    assert.equal('model_context' in captured,false);
    // A later restore cannot overwrite a locally edited index.
    await post({type:'restore',state:{index:1},selected:'Beta'});
    await post({type:'capture',id:'after-stale-restore'});
    await page.waitForFunction(()=>window.messages.some(m=>m.id==='after-stale-restore'));
    assert.equal(await frame.locator('.native-carousel-controls span').textContent(),'1 / 2');
  }finally{await browser.close()}
});

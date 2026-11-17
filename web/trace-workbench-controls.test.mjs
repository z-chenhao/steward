import assert from 'node:assert/strict';
import test from 'node:test';
import {readFileSync} from 'node:fs';
import {transformSync} from 'esbuild';
import {runInNewContext} from 'node:vm';
const contract={exports:{}};runInNewContext(transformSync(readFileSync(new URL('src/trace-workbench-controls-contract.ts',import.meta.url),'utf8'),{loader:'ts',format:'cjs',target:'es2022'}).code,{module:contract,exports:contract.exports});
const source=readFileSync(new URL('src/trace-workbench-controls.ts',import.meta.url),'utf8').replace(/^import .*;\n/gm,'');
const code=transformSync(source,{loader:'ts',format:'cjs',target:'es2022'}).code;
const worker=readFileSync(new URL('../internal/plugin/traceinspector/view/trace-worker.js',import.meta.url),'utf8');
const tick=()=>new Promise(resolve=>setImmediate(resolve));
function fixture(){
 const button=dataset=>({dataset,disabled:true,handlers:{},setAttribute(name,value){this[name]=value},addEventListener(type,fn){this.handlers[type]=fn}}),modes=['formatted','json'].map(jsonMode=>button({jsonMode})),copies=['input','output'].map(copyJson=>button({copyJson})),previous=button({}),next=button({}),bodies={input:{textContent:'private-input'},output:{textContent:'private-output'}};
 const root={dataset:{traceProgramRevision:'sha256:'+'a'.repeat(64)},isConnected:true,querySelectorAll:selector=>selector==='[data-json-mode]'?modes:copies,querySelector:selector=>bodies[selector.includes('input')?'input':'output']};
 const calls=[],clipboard=[],timers=new Map(),changes=[],renders=[],navigations=[];let closed=0,clipboardResolve;
 const entry={active:true,browser:Promise.resolve({call:request=>new Promise(resolve=>calls.push({request,resolve})),close:()=>closed++})};
 const module={exports:{}};runInNewContext(code,{module,exports:module.exports,AbortController,URL,document:{documentElement:{lang:'en'}},traceProgram:()=>entry,legacyTraceControls:()=>{throw Error('native fallback')},acceptTraceControlsPlan:contract.exports.acceptTraceControlsPlan,setReadOnlyJsonMode:(_,mode)=>renders.push(mode),copyText:body=>{clipboard.push(body);return new Promise(resolve=>{clipboardResolve=resolve})},setTimeout:(fn,ms)=>{const key={};timers.set(key,{fn,ms});return key},clearTimeout:key=>timers.delete(key)});
 let response;const self={postMessage:value=>{response=JSON.parse(JSON.stringify(value))}};runInNewContext(worker,{self});
 const links=[0,1,2].map(index=>({href:'http://localhost/developer/observability/'+index}));
 const stop=module.exports.enhanceTraceWorkbenchControls(root,'formatted',mode=>changes.push(mode),{links,index:1,previous,next,navigate:url=>navigations.push(url.href)});
 const reply=(modify=value=>value)=>{self.onmessage({data:{seq:1,...calls[0].request}});assert.equal(response.type,'workbench_controls');calls[0].resolve(modify(response.result))};
 return {calls,modes,copies,clipboard,changes,renders,navigations,timers,root,entry,previous,next,stop,reply,resolveClipboard:value=>clipboardResolve(value),get closed(){return closed}};
}
async function ready(f){await tick();f.reply();await tick()}
test('frozen controls recipe dispatches real mode, clipboard bytes and bounded neighbors without leaking bodies',async()=>{
 const f=fixture();await ready(f);assert.equal(f.calls[0].request.type,'workbench_controls');assert.ok(!JSON.stringify(f.calls[0].request).includes('private-'));
 f.modes[1].handlers.click();assert.equal(f.renders.at(-1),'json');assert.equal(f.changes.at(-1),'json');assert.equal(f.modes[1]['aria-pressed'],'true');
 f.copies[0].handlers.click();assert.deepEqual(f.clipboard,['private-input']);f.resolveClipboard(true);await tick();assert.equal(f.copies[0].title,'Copied');assert.equal(f.timers.size,1);
 const timer=[...f.timers.values()][0];assert.equal(timer.ms,1600);timer.fn();assert.equal(f.copies[0]['aria-label'],'Copy input');
 f.previous.onclick();f.next.onclick();assert.deepEqual(f.navigations,['http://localhost/developer/observability/0','http://localhost/developer/observability/2']);f.stop();
});
test('disposed controls ignore late initialization and clipboard completion and cancel reset timers',async()=>{
 const first=fixture();await tick();first.stop();first.reply();await tick();assert.ok(first.modes.every(button=>button.disabled));assert.equal(first.renders.length,0);
 const second=fixture();await ready(second);second.copies[0].handlers.click();second.stop();second.resolveClipboard(true);await tick();assert.equal(second.copies[0].title,undefined);assert.equal(second.timers.size,0);
 const third=fixture();await ready(third);third.copies[0].handlers.click();third.resolveClipboard(true);await tick();assert.equal(third.timers.size,1);third.stop();assert.equal(third.timers.size,0);assert.equal(third.next.onclick,null);assert.equal(third.copies[0]['aria-label'],'Copy input');assert.equal(third.copies[0].title,'');assert.equal(third.copies[0].dataset.copyLabel,undefined);
});
test('malformed controls do not partially enable modes, copy or navigation and never fall back',async()=>{
 for(const modify of [plan=>({...plan,next:99}),plan=>({...plan,reset_ms:Infinity}),plan=>({...plan,copied:{private:'diagnostic'}})]){
  const f=fixture();await tick();f.reply(modify);await tick();assert.equal(f.closed,1);assert.equal(f.entry.active,false);assert.equal(f.renders.length,0);assert.ok(f.modes.every(button=>button.disabled));assert.ok(f.copies.every(button=>button.disabled));assert.equal(f.clipboard.length,0);
 }
});

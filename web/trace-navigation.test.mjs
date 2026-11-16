import assert from 'node:assert/strict';
import test from 'node:test';
import {readFileSync} from 'node:fs';
import {transformSync} from 'esbuild';
import {runInNewContext} from 'node:vm';
const read=name=>readFileSync(new URL(name,import.meta.url),'utf8');
const load=(file,globals={})=>{const module={exports:{}};runInNewContext(transformSync(read(file).replace(/^import .*;\n/gm,''),{loader:'ts',format:'cjs'}).code,{module,exports:module.exports,...globals});return module.exports};
const contract=load('src/trace-page-controls-contract.ts'),legacy=load('src/legacy/trace-page-controls-v1.ts');
function fixture(){
 let reply,resolve;const calls=[];const root={dataset:{traceProgramRevision:'sha256:'+'a'.repeat(64)},isConnected:true};
 const entry={active:true,browser:Promise.resolve({call:input=>{calls.push(JSON.parse(JSON.stringify(input)));return new Promise(r=>{resolve=r})},close(){entry.closed=true}})};
 const self={postMessage:value=>{reply=JSON.parse(JSON.stringify(value))}};runInNewContext(read('../internal/plugin/traceinspector/view/trace-worker.js'),{self},{timeout:1000});
 const api=load('src/trace-page-controls.ts',{traceProgram:()=>entry,legacyTraceNavigation:legacy.legacyTraceNavigation,acceptTraceNavigationPlan:contract.acceptTraceNavigationPlan});
 return {root,entry,calls,read:input=>api.readTraceNavigation(root,input),reply:change=>{self.onmessage({data:{seq:1,...calls.at(-1)}});assert.equal(reply.error,undefined);resolve(change?change(reply.result):reply.result)}};
}
const tick=()=>new Promise(resolve=>setImmediate(resolve));
test('navigation retains original transition behavior with four boolean facts and no private values',async()=>{
 for(const list of [false,true])for(const current of [false,true])for(const same_trace of [false,true])for(const same_selection of [false,true]){
  if(!list&&!current)continue;
  const input={list,current,same_trace,same_selection},f=fixture(),pending=f.read(input);await tick();f.reply();
  assert.deepEqual(JSON.parse(JSON.stringify(await pending)),JSON.parse(JSON.stringify(legacy.legacyTraceNavigation(input))));assert.deepEqual(f.calls,[{type:'navigation',input}]);
 }
});
test('independent navigation plan changes presentation and retention without native fallback',async()=>{
 const f=fixture(),pending=f.read({list:true,current:false,same_trace:false,same_selection:false});await tick();f.reply(plan=>({...plan,presentation:'page',restore_focus:false,reveal_tabs:false}));const plan=await pending;assert.equal(plan.presentation,'page');assert.equal(plan.restore_focus,false);assert.equal(plan.reveal_tabs,false);
});
test('bad, detached and revoked navigation replies are rejected and close the owning program',async()=>{
 for(const change of [plan=>({...plan,presentation:'replace'}),plan=>({...plan,presentation:['sheet']}),plan=>({...plan,keep_filter:'private'}),plan=>({...plan,url:'/private'})]){
  const f=fixture(),pending=f.read({list:true,current:false,same_trace:false,same_selection:false}),rejected=assert.rejects(pending);await tick();f.reply(change);await rejected;await tick();assert.equal(f.entry.active,false);assert.equal(f.entry.closed,true);
 }
 for(const kind of ['detach','revoke']){const f=fixture(),pending=f.read({list:true,current:true,same_trace:true,same_selection:true}),rejected=assert.rejects(pending);await tick();if(kind==='detach')f.root.isConnected=false;else f.entry.active=false;f.reply();await rejected;await tick();assert.equal(f.entry.closed,true)}
});

import assert from 'node:assert/strict';
import test from 'node:test';
import {readFileSync} from 'node:fs';
import {transformSync} from 'esbuild';
import {runInNewContext} from 'node:vm';
const source=readFileSync(new URL('src/trace-columns.ts',import.meta.url),'utf8')
 .replace("import {traceProgram,disposeTraceProgram} from './trace-program';","const traceProgram=globalThis.fixtureProgram,disposeTraceProgram=globalThis.fixtureDispose;")
 .replace("import {legacyTraceColumns,legacyTraceColumnControls} from './legacy/trace-workbench-v1';","const legacyTraceColumns=()=>{throw Error('native fallback')},legacyTraceColumnControls=()=>{throw Error('native fallback')};");
const code=transformSync(source,{loader:'ts',format:'cjs',target:'es2022'}).code;
const tick=()=>new Promise(resolve=>setImmediate(resolve));
function fixture(){
 const pending=[],writes=[];let closed=0;
 const columns=Array.from({length:11},(_,index)=>({hidden:false,hasAttribute:name=>name==='data-trace-required'&&index===4}));
 const toggles=Array.from({length:10},()=>Object.assign(new EventTarget(),{checked:true,disabled:true}));
 const row={children:Array.from({length:11},()=>({hidden:false}))};
 const table={isConnected:true,style:{},querySelectorAll:selector=>selector==='col'?columns:[row]};
 const scope={querySelector:selector=>selector==='.obs-trace-list table'?table:{dataset:{traceProgramRevision:'sha256:'+'a'.repeat(64)}},querySelectorAll:()=>toggles};
 const module={exports:{}};runInNewContext(code,{module,exports:module.exports,AbortController,fixtureProgram:()=>({browser:Promise.resolve({call:request=>new Promise(resolve=>pending.push({request,resolve})),close:()=>closed++})}),fixtureDispose:()=>closed++,getComputedStyle:()=>({width:'100px'}),localStorage:{getItem:()=>null,setItem:(key,value)=>writes.push({key,value})}});
 const plan=()=>({visible:Array(10).fill(true),indexes:[0,1,2,3,5,6,7,8,9,10],storage_key:'steward.trace.columns.v5',persist:false});
 return {pending,writes,columns,toggles,table,plan,stop:module.exports.enhanceTraceColumns(scope),get closed(){return closed}};
}
async function ready(f){await tick();f.pending[0].resolve(Array(10).fill(true));await tick();f.pending[1].resolve(f.plan());await tick()}
test('managed column controls save only an accepted real checkbox change',async()=>{
 const f=fixture();await ready(f);assert.equal(f.writes.length,0);assert.equal(f.toggles[0].disabled,false);
 f.toggles[0].checked=false;f.toggles[0].dispatchEvent(new Event('change'));await tick();
 assert.equal(f.pending[2].request.type,'column_controls');assert.equal(f.pending[2].request.input.checked,false);assert.equal(f.writes.length,0);assert.equal(f.toggles[0].disabled,true);
 const plan=f.plan();plan.visible[0]=false;plan.persist=true;f.pending[2].resolve(plan);await tick();
 assert.equal(f.columns[0].hidden,true);assert.equal(f.columns[4].hidden,false);assert.equal(f.writes.length,1);assert.equal(JSON.parse(f.writes[0].value)[0],false);f.stop();assert.equal(f.closed,1);
});
test('column projection cannot hide the required column, write other storage, or persist initial load',async()=>{
 for(const change of [plan=>{plan.indexes[0]=4},plan=>{plan.storage_key='credential'},plan=>{plan.persist=true}]){
  const f=fixture();await tick();f.pending[0].resolve(Array(10).fill(true));await tick();const plan=f.plan();plan.visible[0]=false;change(plan);f.pending[1].resolve(plan);await tick();
  assert.equal(f.columns.some(column=>column.hidden),false);assert.equal(f.writes.length,0);assert.ok(f.toggles.every(toggle=>toggle.disabled));assert.equal(f.closed,1);
 }
});
test('disposed column program cannot apply late initialization or leave live controls',async()=>{
 const f=fixture();await tick();f.stop();f.pending[0].resolve(Array(10).fill(false));await tick();
 assert.equal(f.pending.length,1);assert.equal(f.closed,1);assert.equal(f.writes.length,0);assert.ok(f.toggles.every(toggle=>toggle.disabled));assert.equal(f.columns.some(column=>column.hidden),false);
});

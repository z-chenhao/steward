import assert from 'node:assert/strict';
import test from 'node:test';
import {runInNewContext} from 'node:vm';
// Bundle the real DOM adapter, replacing only the already independently tested
// opaque transport so replies can be released in a deterministic order.
import {readFileSync} from 'node:fs';
import {transformSync} from 'esbuild';
const source=readFileSync(new URL('src/trace-tree.ts',import.meta.url),'utf8')
 .replace("import {traceProgram,disposeTraceProgram} from './trace-program';","const traceProgram=globalThis.fixtureProgram,disposeTraceProgram=globalThis.fixtureDispose;")
 .replace("import {legacyRenderTraceTree} from './legacy/trace-workbench-v1';","const legacyRenderTraceTree=()=>{throw Error('unexpected native fallback')};")
 .replace("import {t} from './i18n';","const t=(text:string)=>text;");
const compiled=transformSync(source,{loader:'ts',format:'cjs',target:'es2022'}).code;
function fixture(){
 const pending=[];let closed=0;
 const browser={call:request=>new Promise(resolve=>pending.push({request,resolve})),close:()=>closed++};
 const root={dataset:{traceProgramRevision:'sha256:'+'a'.repeat(64)},isConnected:true};
 const button={disabled:true,attrs:{},setAttribute(key,value){this.attrs[key]=value}};
 const nodes=[{dataset:{observationId:'a',observationType:'agent'},hidden:false,querySelector:selector=>selector==='[data-observation-toggle]'?button:{textContent:'Root'}},{dataset:{observationId:'b',parentObservation:'a',observationType:'tool'},hidden:false,querySelector:selector=>selector==='[data-observation-toggle]'?null:{textContent:'Child'}}];
 const field={value:''},pane={closest:()=>root,querySelectorAll:()=>nodes,querySelector:()=>field};
 const entry={active:true,browser:Promise.resolve(browser)};
 const module={exports:{}};runInNewContext(compiled,{module,exports:module.exports,fixtureProgram:()=>entry,fixtureDispose:()=>{entry.active=false;browser.close()},document:{documentElement:{lang:'en'}}});
 const reply=(hidden=false)=>[{id:'a',hidden:false,disabled:false,expanded:'true',label:'Collapse node: Root'},{id:'b',hidden,disabled:null,expanded:null,label:null}];
 return {api:module.exports,pending,root,pane,field,nodes,button,reply,get closed(){return closed}};
}
const tick=()=>new Promise(resolve=>setImmediate(resolve));
test('managed tree ignores out-of-order replies and disposed-page replies',async()=>{
 const f=fixture();f.field.value='old';const first=f.api.renderTraceTree(f.pane,new Set());await tick();
 f.field.value='latest';const latest=f.api.renderTraceTree(f.pane,new Set());await tick();
 assert.equal(f.pending.length,2);assert.equal(f.pending[1].request.input.query,'latest');
 f.pending[1].resolve(f.reply(false));await latest;assert.equal(f.nodes[1].hidden,false);
 f.pending[0].resolve(f.reply(true));await first;assert.equal(f.nodes[1].hidden,false,'stale search overwrote newest visible row');
 const after=f.api.renderTraceTree(f.pane,new Set());await tick();f.api.disposeTraceTree(f.root);f.pending[2].resolve(f.reply(true));await after;await tick();
 assert.equal(f.nodes[1].hidden,false);assert.equal(f.closed,1);
});
test('managed tree validates the entire projection before applying any row and never falls back',async()=>{
 const f=fixture(),work=f.api.renderTraceTree(f.pane,new Set());await tick();
 const reply=f.reply(true);reply[0].hidden=true;reply[1].id='invented';f.pending[0].resolve(reply);await work;await tick();
 assert.equal(f.nodes[0].hidden,false);assert.equal(f.nodes[1].hidden,false);assert.equal(f.button.disabled,true);assert.equal(f.closed,1);
});

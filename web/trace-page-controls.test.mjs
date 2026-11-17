import assert from 'node:assert/strict';
import test from 'node:test';
import {readFileSync} from 'node:fs';
import {transformSync} from 'esbuild';
import {runInNewContext} from 'node:vm';
const read=path=>readFileSync(new URL(path,import.meta.url),'utf8'),contract={exports:{}};
runInNewContext(transformSync(read('src/trace-page-controls-contract.ts'),{loader:'ts',format:'cjs'}).code,{module:contract,exports:contract.exports});
const source=transformSync(read('src/trace-page-controls.ts').replace(/^import .*;\n/gm,''),{loader:'ts',format:'cjs'}).code,worker=read('../internal/plugin/traceinspector/view/trace-worker.js');
const tick=()=>new Promise(resolve=>setImmediate(resolve));
function fixture(){
 const calls=[],selected=[],listeners=[],rail={open:true},root={id:'observability-page',isConnected:true,dataset:{traceProgramRevision:'sha256:'+'a'.repeat(64)}};
 const scope={querySelector:()=>rail,addEventListener(type,fn,options){listeners.push({type,fn,options})}},window={innerWidth:1440,getSelection:()=>({toString:()=>window.selection??''}),addEventListener(type,fn,options){listeners.push({type,fn,options})}};
 const entry={active:true,browser:Promise.resolve({call:request=>new Promise(resolve=>calls.push({request,resolve})),close:()=>{entry.closed=true}})};
 class MouseEvent{};
 const module={exports:{}};runInNewContext(source,{module,exports:module.exports,AbortController,URL,MouseEvent,location:{origin:'http://localhost'},window,traceProgram:()=>entry,acceptTracePagePlan:contract.exports.acceptTracePagePlan,legacyTracePageControls:()=>{throw Error('native fallback')}});
 const enhance=()=>module.exports.enhanceTracePageControls(root,scope,true,url=>selected.push(url.href));let stop=enhance();
 let response;const self={postMessage:value=>{response=JSON.parse(JSON.stringify(value))}};runInNewContext(worker,{self});
 const reply=(index,modify=value=>value)=>{self.onmessage({data:{seq:index+1,...calls[index].request}});assert.equal(response.type,'page_controls');calls[index].resolve(modify(response.result))};
 const fire=(type,event)=>{for(const item of listeners)if(item.type===type&&!item.options.signal.aborted)item.fn(event)};
 const click=({kind='row',interactive=false,nested=false,button=0,ctrlKey=false,metaKey=false,shiftKey=false,altKey=false,selectedText='',target='',download='',href='http://localhost/developer/observability/trace'}={})=>{
  let focused=false,prevented=false;window.selection=selectedText;
  const anchor={href,target,download,focus(){focused=true}};
  const element={closest:selector=>selector==='#observability-workbench'?(nested?{}:null):selector.includes('tr[')?{querySelector:()=>anchor}:selector.includes('button,input')?(interactive?{}:null):kind==='direct'?anchor:null};
  fire('click',Object.assign(new MouseEvent(),{button,ctrlKey,metaKey,shiftKey,altKey,target:element,preventDefault(){prevented=true}}));return {focused,prevented};
 };
 return {root,rail,window,entry,calls,reply,click,selected,resize:width=>{window.innerWidth=width;fire('resize')},stop:()=>stop(),restore:()=>{stop=enhance()}};
}
async function ready(f){await tick();f.reply(0);await tick()}
test('actual page controls bind only original same-origin links and protect interactive/selection gestures',async()=>{
 const f=fixture();await ready(f);assert.equal(f.click().prevented,true);assert.equal(f.selected.length,1);assert.equal(f.click({kind:'direct'}).prevented,true);
 for(const facts of [{kind:'direct',ctrlKey:true},{kind:'direct',metaKey:true},{kind:'direct',shiftKey:true},{kind:'direct',altKey:true},{kind:'direct',button:1},{nested:true,kind:'direct'},{interactive:true},{selectedText:'private selection'},{kind:'direct',target:'_blank'},{kind:'direct',download:'download'},{kind:'direct',href:'https://foreign.example/private'},{kind:'direct',href:'http://localhost/settings'}])assert.equal(f.click(facts).prevented,false);
 assert.equal(f.selected.length,2);assert.ok(!JSON.stringify(f.calls).includes('private'));f.stop();assert.equal(f.click().prevented,false);
});
test('resize coalescing preserves latest native details interaction and retained wide preference on restore',async()=>{
 const f=fixture();await ready(f);f.resize(390);await tick();for(let i=0;i<50;i++)f.resize(400+i);assert.equal(f.calls.length,2);f.reply(1);await tick();assert.equal(f.rail.open,false);assert.equal(f.calls.length,3);f.rail.open=true;f.reply(2);await tick();assert.equal(f.rail.open,true);
 f.rail.open=false;f.stop();f.window.innerWidth=1440;f.restore();await tick();f.reply(3);await tick();assert.equal(f.rail.open,true);f.stop();
});
test('changed owning navigation plan can disable row enhancement without native fallback',async()=>{
 const f=fixture();await tick();f.reply(0,plan=>({...plan,row:false,kinds:[]}));await tick();assert.equal(f.click().prevented,false);assert.equal(f.click({kind:'direct'}).prevented,false);assert.equal(f.selected.length,0);f.stop();
});
test('disposed or malformed page projections cannot navigate or change rail state',async()=>{
 const f=fixture();await tick();f.stop();f.reply(0,plan=>({...plan,open:false}));await tick();assert.equal(f.rail.open,true);assert.equal(f.click().prevented,false);
 const bad=fixture();await tick();bad.reply(0,plan=>({...plan,kinds:['arbitrary-selector']}));await tick();assert.equal(bad.entry.active,false);assert.equal(bad.entry.closed,true);assert.equal(bad.rail.open,true);assert.equal(bad.click().prevented,false);
});

test('early canonical links wait for the frozen plan and cannot escape or use disposed plans',async()=>{
 const f=fixture();assert.equal(f.click({kind:'direct'}).prevented,true);assert.equal(f.selected.length,0);await ready(f);assert.equal(f.selected.length,1);f.stop();
 const stale=fixture();assert.equal(stale.click({kind:'direct'}).prevented,true);await tick();stale.stop();stale.reply(0);await tick();assert.equal(stale.selected.length,0);
});

test('early row activation waits for its owning plan and honors row deactivation',async()=>{
 const f=fixture();assert.equal(f.click().prevented,true);assert.equal(f.selected.length,0);await ready(f);assert.equal(f.selected.length,1);f.stop();
 const denied=fixture();assert.equal(denied.click().prevented,true);await tick();denied.reply(0,p=>({...p,row:false}));await tick();assert.equal(denied.selected.length,0);denied.stop();
});

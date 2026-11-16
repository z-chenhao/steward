import assert from 'node:assert/strict';
import test from 'node:test';
import {readFileSync} from 'node:fs';
import {transformSync} from 'esbuild';
import {runInNewContext} from 'node:vm';
const projection=transformSync(readFileSync(new URL('src/trace-activity-projection.ts',import.meta.url),'utf8'),{loader:'ts',format:'cjs',target:'es2022'}).code;
const source=readFileSync(new URL('src/trace-activity.ts',import.meta.url),'utf8')
 .replace(/^import .*;\n/gm,'')
 .replace('export function enhanceTraceActivity', 'function enhanceTraceActivity');
const code=transformSync(source+'\nmodule.exports={enhanceTraceActivity};',{loader:'ts',format:'cjs',target:'es2022'}).code;
const worker=readFileSync(new URL('../internal/plugin/traceinspector/view/trace-worker.js',import.meta.url),'utf8');
const tick=()=>new Promise(resolve=>setImmediate(resolve));
function fixture(){
 const node=()=>({hidden:true,style:{},handlers:{},isConnected:true,addEventListener(type,fn){this.handlers[type]=fn},removeAttribute(){},setAttribute(){},querySelector(){return null},getBoundingClientRect:()=>({left:0,top:0,width:100})});
 const buckets=[{at:'2026-11-08T00:00:00Z',until:'2026-11-08T00:00:20Z',count:1},{at:'2026-11-08T00:00:20Z',until:'2026-11-08T00:00:40Z',count:0},{at:'2026-11-08T00:00:40Z',until:'2026-11-08T00:01:00Z',count:2}];
 const plot=node(),brush=node(),explore=node(),text=node(),hint=node(),bars=buckets.map(()=>node()),root=node(),page={dataset:{traceProgramRevision:'sha256:'+'a'.repeat(64)}};
 const pending=[],navigations=[],tooltips=[];let captured=null,closed=0,disposed=0,focused=null;
 plot.setPointerCapture=id=>{captured=id};plot.hasPointerCapture=id=>captured===id;plot.releasePointerCapture=()=>{captured=null};
 root.closest=()=>page;root.dataset={traceActivity:JSON.stringify(buckets)};root.contains=()=>false;
 root.querySelector=selector=>({'.obs-activity-chart':plot,'.obs-activity-brush':brush,'[data-activity-explore]':explore,'[data-activity-selection]':text,'[data-activity-hint]':hint})[selector];root.querySelectorAll=()=>bars;plot.querySelectorAll=()=>bars;
 const entry={active:true,browser:Promise.resolve({call:request=>new Promise(resolve=>pending.push({request,resolve})),close:()=>closed++})};
 const module={exports:{}},document={...node(),documentElement:{lang:'en'}},window=node();
 const acceptModule={exports:{}};runInNewContext(projection,{module:acceptModule,exports:acceptModule.exports});
 const context={module,exports:module.exports,URL,AbortController,Node:class{},document,window,location:{href:'http://localhost/developer/observability?search=keep&role=main&cursor=old',assign:url=>navigations.push(url)},traceProgram:()=>entry,enhanceLegacyTraceActivity:()=>{throw Error('native fallback')},enhanceTraceActivityView:()=>()=>disposed++,createChartTooltip:()=>({show:detail=>tooltips.push(detail),hide(){},dispose(){}}),formatLocalTime:raw=>({full:raw}),acceptTraceActivityProjection:acceptModule.exports.acceptTraceActivityProjection};
 for(const [index,bar] of bars.entries())bar.focus=()=>{focused=index;bar.handlers.focus()};
 runInNewContext(code,context);const stop=module.exports.enhanceTraceActivity({querySelectorAll:()=>[root]});
 let response;const self={postMessage:value=>{response=JSON.parse(JSON.stringify(value))}};runInNewContext(worker,{self},{timeout:1000});
 const reply=(index,modify=value=>value)=>{const call=pending[index];self.onmessage({data:{seq:index+1,...call.request}});assert.equal(response.type,'activity');call.resolve(modify(response.result))};
 const pointer=(type,fraction=.1)=>plot.handlers[type]({button:0,pointerId:1,pointerType:'mouse',clientX:fraction*100,clientY:0,preventDefault(){}});
 return {pending,reply,pointer,plot,brush,explore,text,bars,document,window,root,entry,stop,navigations,tooltips,get closed(){return closed},get disposed(){return disposed},get captured(){return captured},get focused(){return focused}};
}
async function ready(f){await tick();f.reply(0);await tick()}
test('actual activity adapter coalesces queued moves and navigates exact frozen bounds with filters',async()=>{
 const f=fixture();await ready(f);f.pointer('pointerdown',.9);await tick();
 for(let i=0;i<100;i++)f.pointer('pointermove',.1+i*.001);
 f.pointer('pointerup');assert.equal(f.captured,null);assert.equal(f.pending.length,2);
 f.reply(1);await tick();assert.equal(f.pending.length,3);assert.ok(f.pending[2].request.input.event.fraction<.2);
 f.reply(2);await tick();assert.equal(f.pending.length,4);assert.equal(f.pending[3].request.input.event.kind,'up');f.reply(3);await tick();
 assert.equal(f.navigations.length,1);const url=f.navigations[0];assert.equal(url.searchParams.get('since'),'2026-11-08T00:00:00Z');assert.equal(url.searchParams.get('until'),'2026-11-08T00:01:00Z');assert.equal(url.searchParams.get('search'),'keep');assert.equal(url.searchParams.get('role'),'main');assert.equal(url.searchParams.get('reader_revision'),'sha256:'+'a'.repeat(64));assert.equal(url.searchParams.has('cursor'),false);f.stop();
});
test('cancel and pagehide discard already completed late navigation without native fallback',async()=>{
 for(const action of [f=>f.document.handlers.keydown({key:'Escape',preventDefault(){},stopPropagation(){}}),f=>f.window.handlers.pagehide()]){
  const f=fixture();await ready(f);f.pointer('pointerdown');await tick();f.reply(1);await tick();f.pointer('pointerup');await tick();assert.equal(f.pending[2].request.input.event.kind,'up');action(f);f.reply(2);await tick();assert.equal(f.navigations.length,0);assert.equal(f.captured,null);
  if(f.pending[3]){f.reply(3);await tick();assert.equal(f.brush.hidden,true)}f.stop();
 }
});
test('malformed geometry or invented hover navigation cannot partially change original DOM',async()=>{
 for(const modify of [value=>({...value,selection:{...value.selection,width:Infinity}}),value=>({...value,selection:{...value.selection,count:99}}),value=>({...value,navigate:{first:0,last:2}})]){
  const f=fixture();await ready(f);f.pointer('pointerdown');await tick();f.reply(1,modify);await tick();assert.equal(f.closed,1);assert.equal(f.entry.active,false);assert.equal(f.brush.hidden,true);assert.equal(f.navigations.length,0);assert.equal(f.disposed,1);
 }
});
test('rapid key events advance from accepted plugin focus without losing a queued step',async()=>{
 const f=fixture();await ready(f);f.bars[0].handlers.focus();await tick();f.reply(1);await tick();
 const event={key:'ArrowRight',shiftKey:false,preventDefault(){},stopPropagation(){}};f.bars[0].handlers.keydown(event);f.bars[0].handlers.keydown(event);await tick();f.reply(2);await tick();assert.equal(f.focused,1);f.reply(3);await tick();assert.equal(f.focused,2);f.stop();
});

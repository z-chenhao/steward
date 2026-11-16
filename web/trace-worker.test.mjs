import assert from 'node:assert/strict';
import test from 'node:test';
import {readFileSync} from 'node:fs';
import {runInNewContext} from 'node:vm';
import {transformSync} from 'esbuild';
const source=readFileSync(new URL('../internal/plugin/traceinspector/view/trace-worker.js',import.meta.url),'utf8');
const original=JSON.parse(readFileSync(new URL('../tests/fixtures/plugins/trace-workbench-original-v1/client-behavior.json',import.meta.url),'utf8'));
function program(){
 let reply;
 const self={postMessage:value=>{reply=JSON.parse(JSON.stringify(value))}};
 runInNewContext(source,{self},{timeout:1000});
 return request=>{reply=undefined;self.onmessage({data:request});return reply};
}
for(const type of ['tree','columns','bounded_window','zoom_window','bucket_selection','elapsed_label','time_micros','activity_geometry','activity_index'])test('candidate Trace Worker preserves executed original '+type,()=>{
 const call=program();let seq=0;
 for(const example of original[type]){
  const input=type==='columns'?example.storage:example.input;
  const reply=call({seq:++seq,type,input});
  assert.deepEqual(reply,{seq,type,result:example.expected},type+' case '+(seq-1));
 }
});
test('candidate Trace Worker rejects malformed or excessive input without echoing private diagnostics',()=>{
 const call=program();
 for(const request of [null,{},[],{seq:0,type:'columns',input:{}},{seq:1,type:'unknown',input:'private-marker'},{seq:1,type:'tree',input:{nodes:[],closed:[],query:5}},{seq:1,type:'tree',input:{nodes:Array(1001).fill({id:'x',name:'x',type:'tool'}),closed:[],query:''}},{seq:1,type:'columns',input:{credential:'private-marker'}},{seq:1,type:'bounded_window',input:{domain:{start:0,end:10},start:NaN,end:10}},{seq:1,type:'activity_geometry',input:[]},{seq:1,type:'time_micros',input:'private-marker'},{seq:1,type:'activity_index',input:{geometry:[{left:'private-marker',right:1}],fraction:0}},{seq:1,type:'bucket_selection',input:{count:-1,first:0,last:1}}]){
  const reply=call(request);
  assert.deepEqual(reply,{seq:Number.isSafeInteger(request?.seq)&&request.seq>0?request.seq:0,type:'failed'});
  assert.ok(!JSON.stringify(reply).includes('private-marker'));
 }
 // Rejected calls do not mutate the next valid projection.
 assert.deepEqual(call({seq:2,type:'columns',input:{}}),{seq:2,type:'columns',result:Array(10).fill(true)});
});

test('storage failures preserve original lazy-read precedence, including unread legacy keys',()=>{
 const client=readFileSync(new URL('../tests/fixtures/plugins/trace-workbench-original-v1/web__src__observability.ts.txt',import.meta.url),'utf8');
 const begin=client.indexOf('  let visible=toggles.map'),end=client.indexOf('  const render=()=>{',begin);
 const code='(()=>{const toggles=Array(10).fill(null);'+client.slice(begin,end)+';return visible})()';
 const v=n=>'steward.trace.columns.v'+n,alternating=Array.from({length:10},(_,i)=>i%2===0);
 const call=program();
 for(const [index,{storage,unavailable}] of [
  {storage:{[v(5)]:JSON.stringify(alternating)},unavailable:[v(2)]},
  {storage:{[v(4)]:JSON.stringify(alternating)},unavailable:[v(5)]},
  {storage:{[v(4)]:JSON.stringify(alternating)},unavailable:[v(2)]},
  {storage:{[v(2)]:JSON.stringify(alternating)},unavailable:[v(3)]},
  {storage:{},unavailable:[v(2),v(3),v(4),v(5)]},
 ].entries()){
  const expected=JSON.parse(JSON.stringify(runInNewContext(code,{localStorage:{getItem:key=>{if(unavailable.includes(key))throw Error('storage unavailable');return storage[key]??null}}})));
  assert.deepEqual(call({seq:index+1,type:'columns',input:storage,unavailable}),{seq:index+1,type:'columns',result:expected});
 }
});

test('column control projections preserve original executed checkbox events and persistence',()=>{
 const client=readFileSync(new URL('../tests/fixtures/plugins/trace-workbench-original-v1/web__src__observability.ts.txt',import.meta.url),'utf8');
 const begin=client.indexOf('  const columnIndexes='),end=client.indexOf('\n  render();',begin);
 const code=client.slice(begin,end)+'\n render();globalThis.toggleOriginal=(index,checked)=>{toggles[index].checked=checked;toggles[index].change()};globalThis.originalState=()=>({visible:visible.slice(),indexes:columnIndexes.slice()});';
 const call=program();let seq=0;
 for(const initial of [Array(10).fill(true),Array(10).fill(false),Array.from({length:10},(_,i)=>i%2===0)])for(let index=0;index<10;index++)for(const checked of [false,true]){
  const toggles=Array.from({length:10},()=>({checked:true,disabled:true,addEventListener(type,fn){assert.equal(type,'change');this.change=fn}}));
  const columns=Array.from({length:11},()=>({hidden:false})),row={children:Array.from({length:11},()=>({hidden:false}))},saved=[];
  const context={document:{querySelectorAll:()=>toggles},columns,widths:Array(11).fill(100),table:{querySelectorAll:()=>[row],style:{}},localStorage:{getItem:key=>key==='steward.trace.columns.v5'?JSON.stringify(initial):null,setItem:(key,value)=>saved.push({key,value})}};
  runInNewContext(transformSync(code,{loader:'ts',target:'es2022'}).code,context);assert.equal(saved.length,0);context.toggleOriginal(index,checked);
  const expected=JSON.parse(JSON.stringify(context.originalState()));assert.equal(saved.length,1);assert.deepEqual(JSON.parse(saved[0].value),expected.visible);
  const result=call({seq:++seq,type:'column_controls',input:{action:'toggle',visible:initial,index,checked}});
  assert.deepEqual(result,{seq,type:'column_controls',result:{...expected,storage_key:saved[0].key,persist:true}});
 }
 assert.equal(call({seq:++seq,type:'column_controls',input:{action:'render',visible:Array(10).fill(true)}}).result.persist,false);
 for(const input of [{action:'toggle',visible:Array(10).fill(true),index:10,checked:true},{action:'render',visible:[]},{action:'toggle',visible:Array(10).fill(true),index:0,checked:'true'}])assert.equal(call({seq:++seq,type:'column_controls',input}).type,'failed');
});

test('timeline window actions match the executed original button, key and wheel handlers',()=>{
 const directory=new URL('../tests/fixtures/plugins/trace-workbench-original-v1/',import.meta.url);
 const original=readFileSync(new URL('web__src__trace-timeline.ts.txt',directory),'utf8');
 const mathSource=readFileSync(new URL('web__src__trace-time.ts.txt',directory),'utf8'),math={exports:{}};
 runInNewContext(transformSync(mathSource,{loader:'ts',format:'cjs',target:'es2022'}).code,{module:math,exports:math.exports});
 const apply=original.slice(original.indexOf(' const apply='),original.indexOf(' const bounds='));
 const handlers=original.slice(original.indexOf(" for(const button of actions)button.addEventListener('click'"),original.indexOf(" pane.addEventListener('pointerdown'"));
 const code=transformSync(apply+handlers,{loader:'ts',target:'es2022'}).code;
 const call=program();let seq=0;
 for(const domain of [{start:0,end:1000},{start:1000000,end:2000000}])for(const current of [domain,{start:domain.start+(domain.end-domain.start)*.25,end:domain.start+(domain.end-domain.start)*.75},{start:domain.start,end:domain.start+10}]){
  const cases=[];
  for(const action of ['fit','in','out','selected'])for(const selected of [null,{start:domain.start+100,end:domain.start+200},{start:domain.start+150,end:null}])cases.push({kind:'button',action,selected});
  for(const key of ['ArrowLeft','ArrowRight','Home','+','-'])cases.push({kind:'key',key});
  for(const pan of [false,true])for(const delta_y of [-2,0,2])for(const delta_x of [0,2])for(const anchor of [0,.5,1])cases.push({kind:'wheel',pan,delta_y,delta_x,anchor});
  for(const action of cases){
   const callbacks={},buttons=['fit','in','out','selected'].map(name=>({dataset:{timelineAction:name},addEventListener:(_,handler)=>callbacks[name]=handler}));
   const selected=action.kind==='button'?action.selected:null;
   const context={signal:undefined,current:{...current},domain,actions:buttons,rows:selected?[{dataset:{start:String(selected.start),...(selected.end===null?{}:{end:String(selected.end)})},querySelector:()=>true}]:[],tooltip:{hide(){}},render(){},bounds:()=>({left:10,width:500}),axis:{addEventListener:(_,handler)=>callbacks.key=handler},pane:{addEventListener:(_,handler)=>callbacks.wheel=handler},boundedWindow:math.exports.boundedWindow,zoomWindow:math.exports.zoomWindow};
   runInNewContext(code,context);
   if(action.kind==='button')callbacks[action.action]();
   else if(action.kind==='key')callbacks.key({key:action.key,preventDefault(){}});
   else callbacks.wheel({ctrlKey:true,metaKey:false,shiftKey:action.pan,deltaY:action.delta_y,deltaX:action.delta_x,clientX:10+500*action.anchor,preventDefault(){}});
   const expected=JSON.parse(JSON.stringify(context.current));
   assert.deepEqual(call({seq:++seq,type:'timeline_window',input:{domain,current,action}}),{seq,type:'timeline_window',result:expected},JSON.stringify({domain,current,action}));
  }
 }
 for(const input of [{domain:{start:1,end:1},current:{start:1,end:2},action:{kind:'restore'}},{domain:{start:0,end:10},current:{start:0,end:10},action:{kind:'key',key:'Delete'}}])assert.equal(call({seq:++seq,type:'timeline_window',input}).type,'failed');
});

test('timeline geometry and tick density match execution of the original SVG render function',()=>{
 const directory=new URL('../tests/fixtures/plugins/trace-workbench-original-v1/',import.meta.url);
 const original=readFileSync(new URL('web__src__trace-timeline.ts.txt',directory),'utf8'),math={exports:{}};
 runInNewContext(transformSync(readFileSync(new URL('web__src__trace-time.ts.txt',directory),'utf8'),{loader:'ts',format:'cjs',target:'es2022'}).code,{module:math,exports:math.exports});
 const geometry=original.slice(original.indexOf(' const x='),original.indexOf(' const actions='));
 const render=original.slice(original.indexOf(' const render=()=>{'),original.indexOf(' const apply='));
 const describe=original.slice(original.indexOf(' const describe='),original.indexOf(' for(const row of rows){',original.indexOf(' const describe=')));
 const code=transformSync(geometry+render+describe+'\nrender();globalThis.describeOriginal=describe;',{loader:'ts',target:'es2022'}).code;
 const domain={start:0,end:1000000};
 const records=[{start:0,end:1000000,running:false,first_output:10000},{start:200000,end:800000,running:false,first_output:300000},{start:250000,end:null,running:true,first_output:null},{start:500000,end:null,running:false,first_output:500000},{start:250000,end:250000,running:false,first_output:null},{start:-100,end:-1,running:false,first_output:300000},{start:1000001,end:1000010,running:false,first_output:500000},{start:1000000,end:1000001,running:false,first_output:null},{start:0,end:0,running:false,first_output:0}];
 const call=program();let seq=0;
 for(const current of [domain,{start:250000,end:750000},{start:999000,end:1000000}])for(const width of [0,179,180,319,320,640])for(const selected of [false,true]){
  const svgs=records.map(()=>({children:[],replaceChildren(){this.children=[]},append(child){this.children.push(child)}}));
  const rows=records.map((row,index)=>({dataset:{start:String(row.start),...(row.end===null?{}:{end:String(row.end)}),...(row.first_output===null?{}:{firstOutput:String(row.first_output)})},hasAttribute:()=>row.running,querySelector:selector=>selector==='[aria-current]'?selected&&index===0:selector==='.obs-node-name'?{textContent:'Row '+index}:selector==='.obs-node-status'?{getAttribute:()=>row.running?'running':'completed'}:svgs[index]}));
  const axis={clientWidth:width,children:[],attrs:{},replaceChildren(){this.children=[]},append(child){this.children.push(child)},setAttribute(key,value){this.attrs[key]=value}},actions=['in','out','fit','selected'].map(action=>({dataset:{timelineAction:action},disabled:true}));
  let remembered='not called';
  const context={axis,rows,root:{dataset:{}},actions,current,domain,ns:'http://www.w3.org/2000/svg',elapsedLabel:math.exports.elapsedLabel,t:text=>text,remember:value=>{remembered=value},document:{createElement:tag=>({tag,textContent:''}),createElementNS:(_,tag)=>({tag,attrs:{},setAttribute(key,value){this.attrs[key]=value}})}};
  runInNewContext(code,context);
  const find=(svg,kind)=>svg.children.find(child=>child.attrs.class===kind);
  const expected={drag_threshold:Number(original.match(/Math\.abs\(drag.last-drag.start\)>(\d+)/)[1]),ticks:axis.children.map(child=>child.textContent),range_label:axis.attrs['aria-label'],grid:svgs[0].children.filter(child=>child.attrs.class==='obs-timeline-grid').map(child=>Number(child.attrs.x1)),full:remembered===null,buttons:Object.fromEntries(actions.map(button=>[button.dataset.timelineAction,button.disabled])),rows:svgs.map((svg,index)=>{const bar=find(svg,'obs-bar'),point=find(svg,'obs-point'),first=find(svg,'obs-first-output');return {bar:bar?{x:Number(bar.attrs.x),width:Number(bar.attrs.width)}:null,point:point?Number(point.attrs.x1):null,first_output:first?Number(first.attrs.x1):null,tooltip:context.describeOriginal(rows[index]).split('\n').slice(2)}})};
  assert.deepEqual(call({seq:++seq,type:'timeline',input:{domain,current,action:{kind:'restore'},width,has_selected:selected,language:'en',rows:records}}),{seq,type:'timeline',result:{window:current,projection:expected}},JSON.stringify({current,width,selected}));
 }
 assert.equal(call({seq:++seq,type:'timeline',input:{domain,current:domain,action:{kind:'restore'},width:500,has_selected:false,language:'zh-CN',rows:[]}}).result.projection.range_label,'距 Trace 开始的时间: 0.00ms – 1.00s');
});

test('timeline timing tooltip copy preserves original English and captured Chinese, including unknown ends',()=>{
 const directory=new URL('../tests/fixtures/plugins/trace-workbench-original-v1/',import.meta.url),read=name=>readFileSync(new URL(name,directory),'utf8');
 const original=read('web__src__trace-timeline.ts.txt'),begin=original.indexOf(' const describe='),end=original.indexOf(' for(const row of rows){',begin);
 const math={exports:{}};runInNewContext(transformSync(read('web__src__trace-time.ts.txt'),{loader:'ts',format:'cjs',target:'es2022'}).code,{module:math,exports:math.exports});
 const code=transformSync(original.slice(begin,end)+'\nglobalThis.describeOriginal=describe;',{loader:'ts',target:'es2022'}).code;
 const labels=JSON.parse(read('timeline-tooltip-labels-v1.json')),domain={start:1000000,end:2000000};
 const call=program();let seq=0;
 for(const language of ['en','zh-CN'])for(const record of [{start:1100000,end:1200000,running:false,first_output:1150000},{start:1100000,end:null,running:true,first_output:null},{start:1100000,end:null,running:false,first_output:0},{start:1100000,end:1100000,running:false,first_output:null},{start:1100000,end:1000000,running:false,first_output:null}]){
  const context={domain,elapsedLabel:math.exports.elapsedLabel,t:text=>language==='zh-CN'?(labels[text]??text):text};runInNewContext(code,context);
  const node={dataset:{start:String(record.start),...(record.end===null?{}:{end:String(record.end)}),...(record.first_output===null?{}:{firstOutput:String(record.first_output)})},hasAttribute:()=>record.running,querySelector:selector=>selector==='.obs-node-name'?{textContent:'Private node'}:{getAttribute:()=>record.running?'running':'completed'}};
  const expected=context.describeOriginal(node).split('\n').slice(2);
  const reply=call({seq:++seq,type:'timeline',input:{domain,current:domain,action:{kind:'restore'},width:240,has_selected:false,language,rows:[record]}});
  assert.deepEqual(reply.result.projection.rows[0].tooltip,expected,JSON.stringify({language,record}));
 }
});

test('timeline brush threshold, preview and committed window match original pointer handlers',()=>{
 const directory=new URL('../tests/fixtures/plugins/trace-workbench-original-v1/',import.meta.url),read=name=>readFileSync(new URL(name,directory),'utf8');
 const original=read('web__src__trace-timeline.ts.txt'),math={exports:{}};runInNewContext(transformSync(read('web__src__trace-time.ts.txt'),{loader:'ts',format:'cjs',target:'es2022'}).code,{module:math,exports:math.exports});
 const geometry=original.slice(original.indexOf(' const x='),original.indexOf(' const actions='));
 const apply=original.slice(original.indexOf(' const apply='),original.indexOf(' const bounds='));
 const at=original.slice(original.indexOf(' const at='),original.indexOf(' const describe='));
 const move=original.slice(original.indexOf(" pane.addEventListener('pointermove'"),original.indexOf(' const cancel='));
 const up=original.slice(original.indexOf(" pane.addEventListener('pointerup'"),original.indexOf(" pane.addEventListener('click'"));
 const code=transformSync(geometry+apply+at+move+up,{loader:'ts',target:'es2022'}).code;
 const fractions=[-.2,0,.2,.8,1,1.2],positions=fractions.flatMap(first=>fractions.map(last=>[first,last]));for(const delta of [4,5,6])positions.push([0,delta/500]);
 const epoch=1793235600000000,call=program();let seq=0;
 for(const {domain,current} of [{domain:{start:0,end:1000000},current:{start:0,end:1000000}},{domain:{start:0,end:1000000},current:{start:250000,end:750000}},{domain:{start:epoch,end:epoch+1000000},current:{start:epoch+250000,end:epoch+750000}}])for(const [first,last] of positions){
  const callbacks={},svg={children:[],querySelector:()=>null,append(child){this.children.push(child)}};let captured=false,label;
  const firstX=10+500*first,lastX=10+500*last;
  const context={domain,current:{...current},drag:{id:1,start:firstX,last:firstX,moved:false},suppressClick:false,signal:undefined,ns:'http://www.w3.org/2000/svg',bounds:()=>({left:10,width:500}),rows:[{querySelector:()=>svg}],tooltip:{show:text=>{label=text},hide(){}},render(){},boundedWindow:math.exports.boundedWindow,elapsedLabel:math.exports.elapsedLabel,pane:{addEventListener:(type,fn)=>callbacks[type]=fn,hasPointerCapture:()=>captured,setPointerCapture:()=>{captured=true},releasePointerCapture:()=>{captured=false}},document:{createElementNS:()=>({attrs:{},setAttribute(key,value){this.attrs[key]=value}})}};
  runInNewContext(code,context);callbacks.pointermove({pointerId:1,clientX:lastX,clientY:50});
  const moved=context.drag.moved,input={domain,current,first:(firstX-10)/500,last:(lastX-10)/500};
  const threshold=call({seq:++seq,type:'timeline',input:{domain,current,action:{kind:'restore'},width:200,has_selected:false,language:'en',rows:[]}}).result.projection.drag_threshold;
  assert.equal(moved,Math.abs(lastX-firstX)>threshold);
  if(moved){const rect=svg.children[0];assert.deepEqual(call({seq:++seq,type:'timeline_brush',input}),{seq,type:'timeline_brush',result:{x:Number(rect.attrs.x),width:Number(rect.attrs.width),label}})}else assert.equal(svg.children.length,0);
  callbacks.pointerup({pointerId:1});assert.equal(captured,false);
  if(moved){const reply=call({seq:++seq,type:'timeline',input:{domain,current,action:{kind:'brush',first:input.first,last:input.last},width:200,has_selected:false,language:'en',rows:[]}});assert.deepEqual(reply.result.window,JSON.parse(JSON.stringify(context.current)))}else assert.deepEqual(context.current,current);
 }
});

test('activity view preserves executed original axis threshold, times, format and maximum',()=>{
 const archived=readFileSync(new URL('../tests/fixtures/plugins/trace-workbench-original-v1/web__src__trace-activity.ts.txt',import.meta.url),'utf8');
 const begin=archived.indexOf('  const max=Math.max('),end=archived.indexOf('  const resize=',begin);
 assert.ok(begin>=0&&end>begin);
 const code=transformSync(archived.slice(begin,end)+';drawAxis();',{loader:'ts',target:'es2022'}).code,call=program();let seq=0;
 for(const duration of [0,1,59999,86399999,86400000,86400001,31536000000])for(const width of [0,390,499,500,1440]){
  const at='2026-11-08T00:00:00.000000Z',until=new Date(Date.parse(at)+duration).toISOString(),buckets=[{at,until,count:0},{at:until,until,count:7}];
  const times=[],modes=[],maximum={textContent:''},axis={replaceChildren(){times.length=0},append(node){times.push(node.textContent)}};
  runInNewContext(code,{buckets,plot:{clientWidth:width},root:{querySelector:selector=>selector==='[data-activity-max]'?maximum:axis},document:{documentElement:{lang:'en'},createElement:()=>({textContent:''})},formatLocalTime:(time,mode)=>{modes.push(mode);return {text:time}}});
  const result=call({seq:++seq,type:'activity_view',input:{buckets,width}});
  assert.deepEqual(result,{seq,type:'activity_view',result:{maximum:Number(maximum.textContent),axis:{mode:modes[0],times}}});
 }
 for(const input of [{buckets:[],width:500},{buckets:[{at:'private-marker',until:'private-marker',count:1}],width:500},{buckets:[{at:'2026-11-08',until:'2026-11-09',count:-1}],width:500}])assert.equal(call({seq:++seq,type:'activity_view',input}).type,'failed');
});

test('activity state machine preserves original mouse, touch and keyboard executions',()=>{
 const directory=new URL('../tests/fixtures/plugins/trace-workbench-original-v1/',import.meta.url);
 const archived=readFileSync(new URL('web__src__trace-activity.ts.txt',directory),'utf8'),math={exports:{}};
 runInNewContext(transformSync(readFileSync(new URL('web__src__trace-time.ts.txt',directory),'utf8'),{loader:'ts',format:'cjs',target:'es2022'}).code,{module:math,exports:math.exports});
 const originalCode=transformSync(archived.replace(/^import .*;\n/gm,'').replace('export function enhanceTraceActivity','function enhanceTraceActivity').replace('  disposers.push(',"  globalThis.snapshot=()=>({state:{drag:drag?{first:drag.first,last:drag.last,touch:drag.touch}:null,pinned,selected,keyboard_start:keyboardStart},brush:{hidden:brush.hidden,left:brush.style.left,width:brush.style.width},persistent:!selectionText.hidden,detail:selectionText.textContent,tooltip:globalThis.tip,navigations:globalThis.navigations.slice(),focus:globalThis.focused});\n  disposers.push(")+'\nenhanceTraceActivity(globalThis.scope);',{loader:'ts',target:'es2022'}).code;
 const call=program();let seq=0,steps=0;
 const base=Date.parse('2026-11-08T00:00:00Z');
 for(const counts of [[2,0,3],[0,0,0],[0,7,0]])for(const touch of [false,true]){
  const buckets=counts.map((count,index)=>({at:new Date(base+index*20000).toISOString(),until:new Date(base+(index+1)*20000).toISOString(),count}));
  const node=()=>({hidden:true,style:{},handlers:{},addEventListener(type,fn){this.handlers[type]=fn},removeAttribute(){},setAttribute(){},querySelector(){return null}});
  const brush=node(),explore=node(),selectionText=node(),hint=node(),axis={replaceChildren(){},append(){}},max={textContent:''},plot=node();
  plot.getBoundingClientRect=()=>({left:0,width:100});plot.clientWidth=600;plot.setPointerCapture=()=>{};plot.hasPointerCapture=()=>false;
  const context={...math.exports,AbortController,URL,Node:class{},tip:null,navigations:[],focused:null,document:{documentElement:{lang:'en'},createElement:()=>({}),addEventListener(){}},ResizeObserver:class{observe(){}disconnect(){}},formatLocalTime:raw=>({full:raw,text:raw}),t:key=>key,location:{href:'http://localhost/developer/observability?search=keep&cursor=old',assign:value=>context.navigations.push({first:value.searchParams.get('since'),last:value.searchParams.get('until')})},createChartTooltip:()=>({show:detail=>{context.tip=detail},hide:()=>{context.tip=null},dispose(){}})};
  const bars=buckets.map((_,index)=>Object.assign(node(),{getBoundingClientRect:()=>({left:index*30,top:0}),focus(){context.focused=index;this.handlers.focus()}}));
  plot.querySelectorAll=()=>bars;plot.querySelector=()=>brush;
  const root={dataset:{traceActivity:JSON.stringify(buckets)},contains:()=>false,querySelector:selector=>({'.obs-activity-chart':plot,'[data-activity-explore]':explore,'[data-activity-selection]':selectionText,'[data-activity-hint]':hint,'[data-activity-max]':max,'.obs-activity-axis':axis})[selector]};
  context.scope={querySelectorAll:()=>[root]};runInNewContext(originalCode,context);
  let state=null,expectedNavigations=0;
  const send=(event)=>{
   const response=call({seq:++seq,type:'activity',input:{buckets,state,event,language:'en'}});assert.equal(response.type,'activity');state=response.result.state;
   const original=JSON.parse(JSON.stringify(context.snapshot())),{focused,...actual}=state;
   assert.deepEqual(actual,original.state,'state after '+JSON.stringify(event));
   const selected=response.result.selection;
   assert.equal(selected===null,original.brush.hidden);
   if(selected){assert.equal(selected.left*100+'%',original.brush.left);assert.equal(selected.width*100+'%',original.brush.width);assert.equal(selected.persistent,original.persistent);assert.equal(selected.explore,!explore.hidden);assert.equal(`${buckets[selected.first].at} – ${buckets[selected.last].until}\n${selected.count} traces`,original.detail)}
   const tip=response.result.tooltip;
   if(tip!==false)assert.equal(tip===null?null:`${buckets[tip.first].at} – ${buckets[tip.last].until}\n${tip.count} traces`,original.tooltip);
   const nav=response.result.navigate;expectedNavigations+=nav?1:0;assert.equal(original.navigations.length,expectedNavigations);if(nav)assert.deepEqual(original.navigations.at(-1),{first:buckets[nav.first].at,last:buckets[nav.last].until});
   steps++;
  };
  send({kind:'init'});
  const pointer=(type,fraction)=>{plot.handlers[type]({button:0,pointerId:1,pointerType:touch?'touch':'mouse',clientX:fraction*100,clientY:0,preventDefault(){}})};
  for(const [start,end] of [[.1,.9],[.9,.1],[.5,.5],[-.2,1.2]]){
   pointer('pointerdown',start);send({kind:'down',fraction:start,touch});
   pointer('pointermove',end);send({kind:'move',fraction:end,touch,drag:true});
   pointer('pointerup',end);send({kind:'up'});
   // Escape the current bar, using the actual original key handler.
   bars[0].handlers.keydown({key:'Escape',preventDefault(){},stopPropagation(){}});send({kind:'clear'});
  }
  bars[0].focus();send({kind:'focus',index:0});
  for(const [key,shift] of [['ArrowRight',true],['ArrowRight',true],['ArrowLeft',false],['End',false],['ArrowRight',false],['Home',false],['ArrowLeft',true],['Enter',false],['Escape',false]]){
   const index=context.focused;bars[index].handlers.keydown({key,shiftKey:shift,preventDefault(){},stopPropagation(){}});send({kind:'key',index,key,shift});
  }
 }
 assert.equal(steps,162);
});

test('workbench controls preserve executed original modes, copy feedback and neighbor decisions',async()=>{
 const original=readFileSync(new URL('../tests/fixtures/plugins/trace-workbench-original-v1/web__src__observability.ts.txt',import.meta.url),'utf8');
 const modeLine=original.split('\n').find(line=>line.includes(' const setMode='));
 const copyLine=original.split('\n').find(line=>line.includes(' const copy=target?.closest'));
 const copyGate=original.split('\n').find(line=>line.includes('button.disabled=!root.querySelector'));
 const begin=original.indexOf('    const links=Array.from('),end=original.indexOf('\n   }',begin),navCode=original.slice(begin,end);
 assert.ok(modeLine&&copyLine&&begin>=0&&end>begin);
 const call=program();let seq=0;
 for(const mode of ['formatted','json'])for(const hasInput of [false,true])for(const hasOutput of [false,true])for(const count of [0,1,3])for(let index=-1;index<count;index++){
  const result=call({seq:++seq,type:'workbench_controls',input:{mode,input:hasInput,output:hasOutput,language:'en',index,count}}).result;
  const buttons=['formatted','json'].map(key=>({dataset:{jsonMode:key},disabled:true,setAttribute(name,value){this[name]=value}}));let actualMode;
  runInNewContext(transformSync('const jsonMode='+JSON.stringify(mode)+';'+modeLine+';setMode(scope);',{loader:'ts',target:'es2022'}).code,{scope:{querySelectorAll:()=>buttons},setReadOnlyJsonMode:(_,value)=>{actualMode=value}});
  assert.deepEqual(result.states[mode],{mode:actualMode,formatted:buttons[0]['aria-pressed']==='true',json:buttons[1]['aria-pressed']==='true'});assert.ok(buttons.every(button=>!button.disabled));
  const copyButtons=['input','output'].map(copyJson=>({dataset:{copyJson},disabled:true}));
  runInNewContext(transformSync(copyGate,{loader:'ts',target:'es2022'}).code,{root:{querySelectorAll:()=>copyButtons,querySelector:selector=>(selector.includes('input')?hasInput:hasOutput)?{}:null}});
  assert.deepEqual(result.copies.map(copy=>copy.disabled),copyButtons.map(button=>button.disabled));
  const links=Array.from({length:count},(_,i)=>({href:'http://localhost/developer/observability/'+i})),sheet={previous:{},next:{}},navigations=[];
  runInNewContext(transformSync(navCode,{loader:'ts',target:'es2022'}).code,{URL,document:{querySelectorAll:()=>links},url:new URL('http://localhost/developer/observability/'+index),sheet,select:url=>navigations.push(url.href)});
  assert.equal(result.previous===null,sheet.previous.disabled);assert.equal(result.next===null,sheet.next.disabled);
  if(result.previous!==null){sheet.previous.onclick();assert.equal(navigations.at(-1),links[result.previous].href)}
  if(result.next!==null){sheet.next.onclick();assert.equal(navigations.at(-1),links[result.next].href)}
 }
 for(const key of ['input','output'])for(const copied of [false,true]){
  const button={dataset:{copyJson:key},disabled:false,title:'',closest:()=>({querySelector:()=>({textContent:'private-body'})}),setAttribute(name,value){this[name]=value}},timers=[];
  runInNewContext(transformSync(copyLine,{loader:'ts',target:'es2022'}).code,{target:{closest:()=>button},copyText:async body=>{assert.equal(body,'private-body');return copied},t:key=>key,setTimeout:(fn,ms)=>timers.push({fn,ms})});
  await new Promise(resolve=>setImmediate(resolve));const plan=call({seq:++seq,type:'workbench_controls',input:{mode:'formatted',input:true,output:true,language:'en',index:-1,count:0}}).result;
  assert.equal(button.title,copied?plan.copied:plan.unavailable);
  if(copied){assert.equal(timers[0].ms,plan.reset_ms);timers[0].fn();assert.equal(button['aria-label'],plan.copies.find(copy=>copy.key===key).reset_label)}
 }
});

test('page controls preserve original filter transitions and link eligibility recipes',()=>{
 const original=readFileSync(new URL('../tests/fixtures/plugins/trace-workbench-original-v1/web__src__observability.ts.txt',import.meta.url),'utf8');
 const begin=original.indexOf("  const rail=document.querySelector"),end=original.indexOf('  const table=',begin);assert.ok(begin>=0&&end>begin);
 const code=transformSync(original.slice(begin,end),{loader:'ts',target:'es2022'}).code,call=program();let seq=0;
 for(const initial of [true,false]){
  const rail={open:initial},compact={matches:false,addEventListener(_name,fn){this.changed=fn}};
  runInNewContext(code,{document:{querySelector:()=>rail},matchMedia:query=>{assert.equal(query,'(max-width:760px)');return compact}});
  let state={compact:null,wide_open:initial},previousOpen=initial;
  const widths=[1440,800,761,760,390,600,800,1440,390,1440];
  for(const width of widths){const next=width<=760;if(next!==compact.matches){compact.matches=next;compact.changed()}
   const reply=call({seq:++seq,type:'page_controls',input:{list:true,width,open:previousOpen,state}});
   assert.equal(reply.type,'page_controls');assert.equal(reply.result.open,rail.open);state=reply.result.state;previousOpen=rail.open;
   // Native user choice inside a fixed band remains untouched until crossing.
   rail.open=!rail.open;previousOpen=rail.open;const same=call({seq:++seq,type:'page_controls',input:{list:true,width,open:rail.open,state}});assert.equal(same.result.open,rail.open);state=same.result.state;
  }
 }
 for(const list of [false,true]){
  const plan=call({seq:++seq,type:'page_controls',input:{list,width:390,open:true,state:{compact:null,wide_open:true}}}).result;
  const line=original.split('\n').find(line=>line.includes("let anchor=target?.closest<HTMLAnchorElement>(listPage?"));let selector;
  runInNewContext(transformSync(line,{loader:'ts',target:'es2022'}).code,{listPage:list,target:{closest:value=>{selector=value;return null}}});
  const bindings={select:'a.obs-select',trace:'a.trace-open',identifier:'a.control-identifier-link',context:'.context-reader a'};
  assert.equal(plan.kinds.map(kind=>bindings[kind]).join(','),selector);assert.equal(plan.row,list);assert.equal(plan.focus_row,true);
 }
});

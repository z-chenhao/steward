'use strict';
// Private Trace workbench decisions. No DOM, storage, network or raw model
// bodies are required. The Host binds returned indexes to its own snapshot.
(()=>{
function boundedWindow(domain, start, end) {
  const span = Math.min(domain.end - domain.start, Math.max(1, end - start));
  const left = Math.max(domain.start, Math.min(start, domain.end - span));
  return { start: left, end: left + span };
}
function zoomWindow(domain, current, factor, anchor = 0.5) {
  const span = (current.end - current.start) * factor, pivot = current.start + (current.end - current.start) * anchor;
  return boundedWindow(domain, pivot - span * anchor, pivot + span * (1 - anchor));
}
function bucketSelection(count, first, last) {
  return { first: Math.max(0, Math.min(count - 1, Math.min(first, last))), last: Math.max(0, Math.min(count - 1, Math.max(first, last))) };
}
function elapsedLabel(microseconds) {
  const ms = Math.max(0, microseconds / 1e3);
  return ms < 1e3 ? `${ms.toFixed(2)}ms` : `${(ms / 1e3).toFixed(2)}s`;
}
function timeMicros(raw) {
  const fraction = raw.match(/\.(\d+)(?:Z|[+-]\d{2}:\d{2})$/)?.[1] ?? "";
  return Date.parse(raw) * 1e3 + Number(fraction.padEnd(6, "0").slice(3, 6));
}
function activityGeometry(buckets) {
  const start = timeMicros(buckets[0].at), end = timeMicros(buckets.at(-1).until), span = Math.max(1, end - start);
  return buckets.map((bucket) => ({ left: (timeMicros(bucket.at) - start) / span, right: (timeMicros(bucket.until) - start) / span }));
}
function activityIndex(geometry, fraction) {
  const index = geometry.findIndex((bucket) => fraction < bucket.right);
  return index < 0 ? geometry.length - 1 : index;
}

function activityInteraction(input){
 const buckets=input.buckets,event=input.event,initial=()=>({drag:null,pinned:false,selected:null,keyboard_start:null,focused:null});
 let state=input.state===null?initial():{...input.state,drag:input.state.drag?{...input.state.drag}:null};
 let tooltip=false,focus=null,navigate=null,labels=null;
 const detail=(first,last=first)=>({first,last,count:buckets.slice(first,last+1).reduce((sum,bucket)=>sum+bucket.count,0),label:input.language==='zh-CN'?'条 Trace':'traces'});
 const preview=(first,last,touch,anchor=null)=>{state.selected=bucketSelection(buckets.length,first,last);state.pinned=touch;tooltip={...detail(state.selected.first,state.selected.last),anchor}};
 const go=range=>{if(detail(range.first,range.last).count>0)navigate=range};
 const indexAt=fraction=>activityIndex(activityGeometry(buckets),Math.max(0,Math.min(1,fraction)));
 switch(event.kind){
 case 'init':state=initial();labels=buckets.map((_,index)=>detail(index));focus=Math.max(0,buckets.findIndex(bucket=>bucket.count>0));tooltip=null;break;
 case 'clear':state=initial();tooltip=null;break;
 case 'outside':if(state.pinned){state=initial();tooltip=null}break;
 case 'focus':state.focused=event.index;tooltip={...detail(event.index),anchor:event.index};break;
 case 'blur':case 'leave':if(!state.pinned&&!state.drag)tooltip=null;break;
 case 'key':{
  const index=state.focused??event.index,key=event.key;
  if(key==='Escape'){state=initial();tooltip=null;break}
  if(key==='Enter'){go(state.selected??{first:index,last:index});break}
  const next=key==='ArrowLeft'?Math.max(0,index-1):key==='ArrowRight'?Math.min(buckets.length-1,index+1):key==='Home'?0:buckets.length-1;
  if(event.shift&&state.keyboard_start===null)state.keyboard_start=index;
  if(!event.shift)state=initial();
  state.focused=next;focus=next;tooltip={...detail(next),anchor:next};
  if(state.keyboard_start!==null)preview(state.keyboard_start,next,true,next);
  break;
 }
 case 'down':{const index=indexAt(event.fraction);state.drag={first:index,last:index,touch:event.touch};preview(index,index,event.touch);break}
 case 'move':{
  const index=indexAt(event.fraction);
  if(state.drag&&event.drag){state.drag.last=index;preview(state.drag.first,index,state.drag.touch)}
  else if(!state.pinned&&!event.touch)tooltip={...detail(index),anchor:index};
  break;
 }
 case 'up':{
  const saved=state.drag;state.drag=null;
  if(saved){const range=bucketSelection(buckets.length,saved.first,saved.last);if(saved.touch)preview(range.first,range.last,true);else go(range)}
  break;
 }
 }
 let selection=null;
 if(state.selected){const range=state.selected,geometry=activityGeometry(buckets);selection={...detail(range.first,range.last),left:geometry[range.first].left,width:geometry[range.last].right-geometry[range.first].left,persistent:state.pinned,explore:state.pinned&&detail(range.first,range.last).count>0}}
 return {state,selection,tooltip,focus,navigate,labels};
}
function validActivityBuckets(buckets){
 return Array.isArray(buckets)&&buckets.length>0&&buckets.length<=48&&buckets.every(bucket=>record(bucket)&&text(bucket.at,128)&&text(bucket.until,128)&&number(Date.parse(bucket.at))&&number(Date.parse(bucket.until))&&Number.isSafeInteger(bucket.count)&&bucket.count>=0);
}
function validActivityInteraction(input){
 if(!record(input)||!validActivityBuckets(input.buckets)||!['en','zh-CN'].includes(input.language)||!record(input.event))return false;
 const n=input.buckets.length,index=value=>Number.isSafeInteger(value)&&value>=0&&value<n,range=value=>record(value)&&index(value.first)&&index(value.last)&&value.first<=value.last,s=input.state,e=input.event;
 if(s!==null&&(!record(s)||s.focused!==null&&!index(s.focused)||typeof s.pinned!=='boolean'||s.keyboard_start!==null&&!index(s.keyboard_start)||s.selected!==null&&!range(s.selected)||s.drag!==null&&(!record(s.drag)||!index(s.drag.first)||!index(s.drag.last)||typeof s.drag.touch!=='boolean')))return false;
 if(s===null&&!['init','clear'].includes(e.kind))return false;
 switch(e.kind){
 case 'init':case 'clear':case 'outside':case 'blur':case 'leave':case 'up':return true;
 case 'focus':return index(e.index);
 case 'key':return index(e.index)&&typeof e.shift==='boolean'&&['ArrowLeft','ArrowRight','Home','End','Enter','Escape'].includes(e.key);
 case 'down':return number(e.fraction)&&typeof e.touch==='boolean';
 case 'move':return number(e.fraction)&&typeof e.touch==='boolean'&&typeof e.drag==='boolean';
 }
 return false;
}

function timelineBrush(domain,current,first,last){
 const span=current.end-current.start,at=fraction=>current.start+Math.max(0,Math.min(1,fraction))*span,x=time=>(time-current.start)/span*1000;
 const start=at(Math.min(first,last)),end=at(Math.max(first,last));
 return {x:x(start),width:Math.abs(x(at(last))-x(at(first))),label:elapsedLabel(start-domain.start)+' – '+elapsedLabel(end-domain.start),start,end};
}
function timelineTooltip(row,domain,language){
 const labels=language==='zh-CN'?{start:'开始',duration:'耗时',ongoing:'仍在执行，尚无结束时间',unknown:'未记录结束时间',first:'首个模型输出'}:{start:'Start',duration:'Duration',ongoing:'Ongoing; end time not recorded',unknown:'End time not recorded',first:'First model output'};
 const lines=[labels.start+' +'+elapsedLabel(row.start-domain.start)];
 lines.push(row.end!==null?labels.duration+' '+elapsedLabel(row.end-row.start):row.running?labels.ongoing:labels.unknown);
 if(row.first_output!==null)lines.push(labels.first+' +'+elapsedLabel(row.first_output-domain.start));
 return lines;
}
function timelineProjection(input){
 const {domain,current,rows,width,has_selected,language}=input;
 const steps=width>=320?4:width>=180?2:1,x=time=>(time-current.start)/(current.end-current.start)*1000;
 const ticks=Array.from({length:steps+1},(_,i)=>elapsedLabel(current.start-domain.start+(current.end-current.start)*i/steps));
 const full=current.start===domain.start&&current.end===domain.end;
 return {ticks,drag_threshold:5,range_label:(language==='zh-CN'?'距 Trace 开始的时间':'Time since trace start')+': '+elapsedLabel(current.start-domain.start)+' – '+elapsedLabel(current.end-domain.start),grid:[0,250,500,750,1000],full,buttons:{in:false,out:full,fit:full,selected:!has_selected},rows:rows.map(row=>{
  const result={bar:null,point:null,first_output:null,tooltip:timelineTooltip(row,domain,language)},start=row.start,end=row.end!==null?row.end:row.running?domain.end:null;
  if(end!==null&&end>start){
   if(end<current.start||start>current.end)return result;
   result.bar={x:Math.max(0,x(start)),width:Math.max(1,Math.min(1000,x(end))-Math.max(0,x(start)))};
  }else if(start>=current.start&&start<=current.end)result.point=x(start);
  if(row.first_output!==null&&row.first_output>=current.start&&row.first_output<=current.end)result.first_output=x(row.first_output);
  return result;
 })};
}
function tree(nodes,rawQuery,closedIDs,language){
 const byId=new Map(nodes.map(node=>[node.id,node]));
 const query=rawQuery.toLocaleLowerCase().trim(),closed=new Set(closedIDs),visible=new Set();
 for(const node of nodes)if(!query||[node.name,node.type,node.id].join(' ').toLocaleLowerCase().includes(query)){
  let current=node,blocked=false;
  for(let depth=0;current&&depth<=nodes.length;depth++){current=byId.get(current.parent);if(current&&closed.has(current.id))blocked=true}
  if(query){current=node;for(let depth=0;current&&depth<=nodes.length;depth++){visible.add(current);current=byId.get(current.parent)}}else if(!blocked)visible.add(node);
 }
 return nodes.map(node=>{
  const button=nodes.some(child=>child.parent===node.id);
  const expanded=query?nodes.some(child=>child.parent===node.id&&visible.has(child)):!closed.has(node.id);
  return {id:node.id,hidden:!visible.has(node),disabled:button?!!query:null,expanded:button?String(expanded):null,label:button?(language==='zh-CN'?(expanded?'收起节点':'展开节点'):(expanded?'Collapse node':'Expand node'))+': '+node.name:null};
 });
}
function columns(storage,unavailable=[]){
 const read=key=>{if(unavailable.includes(key))throw Error('storage unavailable');return storage[key]??'null'};
 let visible=Array(10).fill(true);
 try{
  let saved=JSON.parse(read('steward.trace.columns.v5'));
  if(saved===null){
   let legacy=JSON.parse(read('steward.trace.columns.v4'));
   if(legacy===null){
    let previous=JSON.parse(read('steward.trace.columns.v3'));
    if(previous===null){const older=JSON.parse(read('steward.trace.columns.v2'));if(Array.isArray(older)&&older.length===10)previous=[...older.slice(0,5),true,...older.slice(5)]}
    if(Array.isArray(previous)&&previous.length===11&&previous.every(value=>typeof value==='boolean'))legacy=[previous[0],previous[1],previous[9],...previous.slice(3,9),previous[10]];
   }
   if(Array.isArray(legacy)&&legacy.length===10&&legacy.every(value=>typeof value==='boolean'))saved=[legacy[9],...legacy.slice(0,9)];
  }
  if(Array.isArray(saved)&&saved.length===10&&saved.every(value=>typeof value==='boolean'))visible=saved;
 }catch{}
 return visible;
}
const record=value=>value!==null&&typeof value==='object'&&!Array.isArray(value);
const number=value=>typeof value==='number'&&Number.isFinite(value);
const window=value=>record(value)&&number(value.start)&&number(value.end);
const text=(value,max)=>typeof value==='string'&&value.length<=max;
function project(r){
 const input=r.input;
 switch(r.type){
 case 'navigation':{
  if(!record(input)||Object.keys(input).length!==4||['list','current','same_trace','same_selection'].some(key=>typeof input[key]!=='boolean'))throw Error('invalid navigation');
  return {presentation:input.current?'replace':'sheet',keep_filter:input.same_trace,keep_tree_scroll:input.same_trace,keep_detail_scroll:input.same_selection,restore_focus:true,reveal_tabs:true};
 }
 case 'page_controls':{
  if(!record(input)||typeof input.list!=='boolean'||!number(input.width)||input.width<0||typeof input.open!=='boolean'||!record(input.state)||input.state.compact!==null&&typeof input.state.compact!=='boolean'||typeof input.state.wide_open!=='boolean')throw Error('invalid page controls');
  const state={...input.state};let open=input.open;
  if(input.list){const compact=input.width<=760;if(compact!==state.compact){if(compact){state.wide_open=open;open=false}else open=state.wide_open;state.compact=compact}}
  return {open,state,kinds:input.list?['select','trace','identifier','context']:['select','identifier','context'],row:input.list,focus_row:true};
 }
 case 'request_reader':{
  if(!record(input)||!['en','zh-CN'].includes(input.language)||Object.keys(input).length!==1)throw Error('invalid request reader');
  const zh=input.language==='zh-CN';
  return {sections:['system','messages','tools','parameters'],expanded:{system:false,messages:'last',tools:false,tool:false,parameters:false},textRules:{separator:'\n',skipEmpty:true},singleMessage:{roleField:'role',role:'user',contentField:'content'},uppercaseRole:true,capture:['attempt','body'],body:'body',omitted:'omitted',model:'model',instructions:['instructions','system'],messages:['messages','input','contents'],roles:['role','type'],text:'text',content:['content','parts'],tools:'tools',definition:'function',name:'name',exclude:['instructions','system','messages','input','contents','tools'],excerpt:90,labels:{indexPrefix:'[',indexSuffix:'] ',roleSuffix:' · ',request:zh?'请求':'Request',missing:zh?'未记录请求正文。':'Request body was not captured.',system:zh?'系统指令':'System instructions',message:'Message',tool:'Tool',tools:zh?'工具':'Tools',parameters:zh?'参数':'Parameters'}};
 }
 case 'workbench_controls':{
  if(!record(input)||!['formatted','json'].includes(input.mode)||typeof input.input!=='boolean'||typeof input.output!=='boolean'||!['en','zh-CN'].includes(input.language)||!Number.isSafeInteger(input.count)||input.count<0||input.count>1000||!Number.isSafeInteger(input.index)||input.index< -1||input.index>=input.count)throw Error('invalid workbench controls');
  const zh=input.language==='zh-CN';
  return {initial:input.mode,states:{formatted:{mode:'formatted',formatted:true,json:false},json:{mode:'json',formatted:false,json:true}},copies:[{key:'input',disabled:!input.input,reset_label:zh?'复制输入':'Copy input'},{key:'output',disabled:!input.output,reset_label:zh?'复制输出':'Copy output'}],copied:zh?'已复制':'Copied',unavailable:zh?'无法自动复制':'Copy unavailable',reset_ms:1600,previous:input.index>0?input.index-1:null,next:input.index>=0&&input.index+1<input.count?input.index+1:null};
 }
 case 'activity':
  if(!validActivityInteraction(input))throw Error('invalid activity');
  return activityInteraction(input);
 case 'activity_view':{
  if(!record(input)||!Array.isArray(input.buckets)||input.buckets.length===0||input.buckets.length>48||!input.buckets.every(bucket=>record(bucket)&&text(bucket.at,128)&&text(bucket.until,128)&&number(Date.parse(bucket.at))&&number(Date.parse(bucket.until))&&Number.isSafeInteger(bucket.count)&&bucket.count>=0)||!number(input.width)||input.width<0)throw Error('invalid activity view');
  const buckets=input.buckets,first=Date.parse(buckets[0].at),end=Date.parse(buckets.at(-1).until);
  if(end<first)throw Error('invalid activity range');
  return {maximum:Math.max(...buckets.map(bucket=>bucket.count)),axis:{mode:end-first<86400000?'clock':'date',times:(input.width<500?[0,1]:[0,.25,.5,.75,1]).map(position=>new Date(first+(end-first)*position).toISOString())}};
 }
 case 'tree':
  if(!record(input)||!Array.isArray(input.nodes)||input.nodes.length>1000||!text(input.query,4096)||!Array.isArray(input.closed)||input.closed.length>1000||!input.closed.every(id=>text(id,300))||!input.nodes.every(node=>record(node)&&text(node.id,300)&&(node.parent===undefined||text(node.parent,300))&&text(node.name,8192)&&text(node.type,300)))throw Error('invalid tree');
  if(input.language!==undefined&&input.language!=='en'&&input.language!=='zh-CN')throw Error('invalid language');
  return tree(input.nodes,input.query,input.closed,input.language);
 case 'column_controls':{
  if(!record(input)||!['render','toggle'].includes(input.action)||!Array.isArray(input.visible)||input.visible.length!==10||!input.visible.every(value=>typeof value==='boolean'))throw Error('invalid controls');
  const visible=input.visible.slice();
  if(input.action==='toggle'){
   if(!Number.isSafeInteger(input.index)||input.index<0||input.index>=visible.length||typeof input.checked!=='boolean')throw Error('invalid toggle');
   visible[input.index]=input.checked;
  }
  return {visible,indexes:[0,1,2,3,5,6,7,8,9,10],storage_key:'steward.trace.columns.v5',persist:input.action==='toggle'};
 }
 case 'columns':
  if(!record(input)||Object.keys(input).some(key=>!/^steward\.trace\.columns\.v[2-5]$/.test(key)||!text(input[key],16384)))throw Error('invalid columns');
  if(r.unavailable!==undefined&&(!Array.isArray(r.unavailable)||r.unavailable.length>4||!r.unavailable.every(key=>/^steward\.trace\.columns\.v[2-5]$/.test(key))))throw Error('invalid storage status');
  return columns(input,r.unavailable);
 case 'timeline_brush':{
  if(!record(input)||!window(input.domain)||!window(input.current)||input.current.end<=input.current.start||input.current.start<input.domain.start||input.current.end>input.domain.end||!number(input.first)||!number(input.last))throw Error('invalid timeline brush');
  const brush=timelineBrush(input.domain,input.current,input.first,input.last);
  return {x:brush.x,width:brush.width,label:brush.label};
 }
 case 'timeline':{
  const next=project({type:'timeline_window',input});
  return {window:next,projection:project({type:'timeline_projection',input:{...input,current:next}})};
 }
 case 'timeline_projection':
  if(!record(input)||!window(input.domain)||!window(input.current)||input.current.end<=input.current.start||input.current.start<input.domain.start||input.current.end>input.domain.end||!number(input.width)||input.width<0||typeof input.has_selected!=='boolean'||!['en','zh-CN'].includes(input.language)||!Array.isArray(input.rows)||input.rows.length>1000||!input.rows.every(row=>record(row)&&number(row.start)&&(row.end===null||number(row.end))&&typeof row.running==='boolean'&&(row.first_output===null||number(row.first_output))))throw Error('invalid timeline projection');
  return timelineProjection(input);
 case 'timeline_window':{
  if(!record(input)||!window(input.domain)||input.domain.end<=input.domain.start||!window(input.current)||!record(input.action))throw Error('invalid timeline');
  const domain=input.domain,current=input.current,action=input.action,span=current.end-current.start;
  switch(action.kind){
  case 'brush':{
   if(!number(action.first)||!number(action.last))throw Error('invalid brush');
   const brush=timelineBrush(domain,current,action.first,action.last);
   return boundedWindow(domain,brush.start,brush.end);
  }
  case 'restore':return boundedWindow(domain,current.start,current.end);
  case 'button':
   if(!['fit','in','out','selected'].includes(action.action))throw Error('invalid button');
   if(action.action==='fit')return boundedWindow(domain,domain.start,domain.end);
   if(action.action==='selected'){
    if(action.selected===null)return current;
    if(!record(action.selected)||!number(action.selected.start)||action.selected.end!==null&&!number(action.selected.end))throw Error('invalid selection');
    const start=action.selected.start,end=action.selected.end??start,padding=Math.max((end-start)*.15,(domain.end-domain.start)/100);
    return boundedWindow(domain,start-padding,end+padding);
   }
   return zoomWindow(domain,current,action.action==='in'?.5:2);
  case 'key':
   if(!['ArrowLeft','ArrowRight','Home','+','-'].includes(action.key))throw Error('invalid key');
   if(action.key==='Home')return boundedWindow(domain,domain.start,domain.end);
   if(action.key==='ArrowLeft'||action.key==='ArrowRight'){const offset=span*.2*(action.key==='ArrowLeft'?-1:1);return boundedWindow(domain,current.start+offset,current.end+offset)}
   return zoomWindow(domain,current,action.key==='+'?.5:2);
  case 'wheel':
   if(typeof action.pan!=='boolean'||!number(action.delta_y)||!number(action.delta_x)||!number(action.anchor))throw Error('invalid wheel');
   if(action.pan){const offset=span*Math.sign(action.delta_y||action.delta_x)*.1;return boundedWindow(domain,current.start+offset,current.end+offset)}
   return zoomWindow(domain,current,action.delta_y<0?.8:1.25,Math.max(0,Math.min(1,action.anchor)));
  }
  throw Error('unknown timeline action');
 }
 case 'bounded_window':
  if(!record(input)||!window(input.domain)||!number(input.start)||!number(input.end))throw Error('invalid window');
  return boundedWindow(input.domain,input.start,input.end);
 case 'zoom_window':
  if(!record(input)||!window(input.domain)||!window(input.current)||!number(input.factor)||!number(input.anchor))throw Error('invalid zoom');
  return zoomWindow(input.domain,input.current,input.factor,input.anchor);
 case 'bucket_selection':
  if(!record(input)||!Number.isSafeInteger(input.count)||input.count<0||!Number.isSafeInteger(input.first)||!Number.isSafeInteger(input.last))throw Error('invalid buckets');
  return bucketSelection(input.count,input.first,input.last);
 case 'elapsed_label':
  if(!number(input))throw Error('invalid elapsed');
  return elapsedLabel(input);
 case 'time_micros':
  if(!text(input,128)||!Number.isFinite(Date.parse(input)))throw Error('invalid timestamp');
  return timeMicros(input);
 case 'activity_geometry':
  if(!Array.isArray(input)||input.length===0||input.length>1000||!input.every(bucket=>record(bucket)&&text(bucket.at,128)&&text(bucket.until,128)&&Number.isFinite(Date.parse(bucket.at))&&Number.isFinite(Date.parse(bucket.until))))throw Error('invalid geometry');
  return activityGeometry(input);
 case 'activity_index':
  if(!record(input)||!Array.isArray(input.geometry)||input.geometry.length>1000||!input.geometry.every(bucket=>record(bucket)&&number(bucket.left)&&number(bucket.right))||!number(input.fraction))throw Error('invalid index');
  return activityIndex(input.geometry,input.fraction);
 }
 throw Error('unknown operation');
}
self.onmessage=event=>{
 const r=event.data;
 try{
  if(!record(r)||!Number.isSafeInteger(r.seq)||r.seq<1)throw Error('invalid envelope');
  self.postMessage({seq:r.seq,type:r.type,result:project(r)});
 }catch{self.postMessage({seq:Number.isSafeInteger(r?.seq)&&r.seq>0?r.seq:0,type:'failed'})}
};
})();

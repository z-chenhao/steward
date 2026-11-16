// Fixed transport only. Candidate chart code executes exclusively in an opaque
// Worker; it receives original chart facts and bounded presentation commands.
(program,component,display,readonly=false)=>{
 let worker,url,stopped=false,sequence=0;
 const pending=new Map(),channel='steward-chart-view';
 const post=value=>parent.postMessage({channel,...value},'*');
 const dispose=()=>{stopped=true;worker?.terminate();if(url)URL.revokeObjectURL(url);for(const p of pending.values())clearTimeout(p.timer);pending.clear()};
 const fail=()=>{if(!stopped){dispose();post({type:'failed'})}};
 const kinds={barChart:'bar',pieChart:'pie','steward.lineChart':'line','steward.scatterChart':'scatter'},kind=kinds[component.name];
 if(!kind){fail();return}
 const facts={...component.props,kind},count=facts.data?.length??facts.labels?.length??0;
 const indices=rows=>Array.isArray(rows)&&rows.length<=count&&new Set(rows).size===rows.length&&rows.every(i=>Number.isInteger(i)&&i>=0&&i<count);
 const valid=p=>p&&indices(p.rows)&&typeof p.horizontal==='boolean'&&typeof p.numericLine==='boolean'&&Number.isFinite(p.height)&&p.height>=260&&p.height<=640&&typeof p.zoom==='boolean'&&Number.isInteger(p.start)&&p.start>=0&&p.start<=1000000&&Number.isInteger(p.end)&&p.end>=0&&p.end<=1000000&&Number.isInteger(p.filteredLength)&&p.filteredLength>=0&&p.filteredLength<=count&&Array.isArray(p.selected)&&p.selected.length===p.rows.length&&p.selected.every(v=>typeof v==='boolean')&&Array.isArray(p.opacity)&&p.opacity.length===p.rows.length&&p.opacity.every(v=>Number.isFinite(v)&&v>=0&&v<=1);
 const send=(command,id)=>{
  if(stopped)return;if(pending.size>=32){fail();return}
  const seq=++sequence,timer=setTimeout(fail,command.type==='init'?10000:2000);pending.set(seq,{type:command.type,item:command.item,id,timer});worker.postMessage({...command,seq});
 };
 try{url=URL.createObjectURL(new Blob([program],{type:'text/javascript'}));worker=new Worker(url)}catch{fail();return}
 worker.onerror=worker.onmessageerror=fail;
 worker.onmessage=event=>{
  if(stopped)return;
  const value=event.data,entry=pending.get(value?.seq);
  const expected={init:'ready',restore:'restored',group:'paint',zoom:'paint',range:'view-state',reset:'reset',capture:'view-state',select:'select',export:'export-rows'}[entry?.type];
  if(!entry||value.type!==expected){fail();return}
  if(value.type==='select'){
   if(!Number.isInteger(value.item)||value.item<0||value.item>=count||value.item!==entry.item){fail();return}
  }else if(value.type==='export-rows'){if(!indices(value.rows)){fail();return}}else if(!valid(value.projection)){fail();return}
  pending.delete(value.seq);clearTimeout(entry.timer);
  if(value.type==='ready'){URL.revokeObjectURL(url);url=undefined}
  post({type:value.type,command:entry.type,...(value.type==='select'?{item:value.item}:value.type==='export-rows'?{rows:value.rows}:{projection:value.projection}),...(value.type==='ready'?{display}:{}),...(entry.id?{id:entry.id}:{})});
  if(readonly&&value.type==='ready')send({type:'restore',state:{start:0,end:Math.max(0,count-1)},query:'',selected:''});
 };
 addEventListener('message',event=>{
  const value=event.data;if(stopped||event.source!==parent||value?.channel!==channel)return;
  if(value.type==='dispose'){dispose();return}
  if(readonly)return;
  if(!['restore','group','zoom','range','reset','capture','select','export'].includes(value.type))return;
  if(value.type==='select'&&(!Number.isInteger(value.item)||value.item<0||value.item>=count))return;
  if(value.type==='group'&&(typeof value.query!=='string'||value.query.length>2000||typeof value.selected!=='string'||value.selected.length>300))return;
  if(value.type==='export'&&(typeof value.id!=='string'||!value.id.length))return;
  if(value.id!==undefined&&(typeof value.id!=='string'||value.id.length>64))return;
  send({type:value.type,...(['restore','range'].includes(value.type)?{state:value.state}:{}),...(['restore','group'].includes(value.type)?{query:value.query,selected:value.selected}:{}),...(value.type==='select'?{item:value.item}:{})},value.id);
 });
 addEventListener('pagehide',dispose);
 send({type:'init',facts});
}

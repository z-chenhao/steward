// Private owning chart policy. Data and commands only; no DOM, refs or effects.
// Preserves ChartExplorer's existing filtering, local zoom and CSV semantics.
'use strict';
(()=>{
  let facts,data,state,query='',selected='',zoom=false,restored=false;
  const numericLine=()=>facts.kind==='line'&&facts.xValues!==undefined;
  const filtered=()=>data.map((point,index)=>({point,index})).filter(({point})=>!query||point.label.toLocaleLowerCase().includes(query.toLocaleLowerCase()));
  const shown=()=>{const rows=filtered();return zoom?rows.slice(state.start,state.end+1):rows};
  const projection=()=>{
    const rows=shown(),horizontal=facts.kind==='bar'&&(rows.length>6||rows.some(({point})=>point.label.length>16));
    return {rows:rows.map(row=>row.index),horizontal,numericLine:numericLine(),height:horizontal?Math.max(260,Math.min(640,rows.length*34+40)):260,zoom,start:state.start,end:state.end,filteredLength:filtered().length,selected:rows.map(({point})=>point.label===selected),opacity:rows.map(({point})=>!selected||point.label===selected?1:.3)};
  };
  const reply=(type,seq,extra={})=>postMessage({type,seq,projection:projection(),...extra});
  const group=value=>{query=typeof value.query==='string'?value.query:'';selected=typeof value.selected==='string'?value.selected:''};
  onmessage=event=>{
    const value=event.data,seq=value.seq;
    if(value.type==='init'&&!facts){
      facts=value.facts;
      data=facts.data??(facts.labels??[]).map((label,index)=>Object.fromEntries([['label',label],['axisX',facts.xValues?.[index]],...(facts.series??[]).map((item,column)=>['series'+column,item.values[index]])]));
      state={start:0,end:Math.max(0,data.length-1)};
      reply('ready',seq);return;
    }
    if(!facts)return;
    if(value.type==='restore'&&!restored){state={...state,...value.state};group(value);restored=true;reply('restored',seq);return}
    if(!restored)return;
    if(value.type==='group'){group(value);reply('paint',seq)}
    // Zoom is deliberately local: reload restores range but starts unzoomed.
    if(value.type==='zoom'){if(facts.kind!=='pie')zoom=!zoom;reply('paint',seq)}
    if(value.type==='range'){state={...state,...value.state};reply('view-state',seq)}
    if(value.type==='reset'){zoom=false;state={start:0,end:Math.max(0,data.length-1)};query='';selected='';reply('reset',seq)}
    if(value.type==='capture')reply('view-state',seq);
    if(value.type==='export')postMessage({type:'export-rows',seq,rows:shown().map(row=>row.index)});
    if(value.type==='select'&&Number.isInteger(value.item)&&shown().some(row=>row.index===value.item))postMessage({type:'select',seq,item:value.item});
  };
})();

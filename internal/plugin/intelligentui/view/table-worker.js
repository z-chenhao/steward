// Owning Table business policy. Executed only in an opaque Worker, never DOM.
'use strict';
(()=>{
  let facts,rows,numeric,locale,language='en',sort=-1,direction=1,query='',selected='',restored=false;
  const visible=()=>{const filtered=rows.filter(row=>(!query||row.cells.some(cell=>cell.toLocaleLowerCase(locale).includes(query.toLocaleLowerCase(locale))))&&(!selected||row.cells.includes(selected)));return sort<0?filtered:[...filtered].sort((a,b)=>direction*a.cells[sort].localeCompare(b.cells[sort],locale,{numeric:true,sensitivity:'base'}))};
  const projection=()=>({sort,direction,numeric,rows:visible().map(row=>row.index),selected:rows.map(row=>row.cells.includes(selected)),pressed:rows.map(row=>row.cells[0]===selected),selectionActive:!!selected,regionLabel:facts.title+(language==='zh-CN'?' 表格':' table'),emptyMessage:language==='zh-CN'?'没有可显示的数据行。':'No rows to display.'});
  const reply=(type,seq)=>postMessage({type,seq,projection:projection()});
  const group=value=>{query=typeof value.query==='string'?value.query:'';selected=typeof value.selected==='string'?value.selected:''};
  onmessage=event=>{
    const value=event.data,seq=value.seq;
    if(value.type==='init'&&!facts){
      facts=value.facts;locale=value.locale;language=value.language;
      rows=facts.rows.map((cells,index)=>({cells,index}));
      numeric=facts.columns.map((_,column)=>rows.some(row=>row.cells[column].trim()!=='')&&rows.every(row=>row.cells[column].trim()===''||Number.isFinite(Number(row.cells[column]))));
      reply('ready',seq);return;
    }
    if(!facts)return;
    if(value.type==='environment'){locale=value.locale;language=value.language;reply('paint',seq);return}
    if(value.type==='restore'&&!restored){sort=Number.isInteger(value.state?.sort)&&value.state.sort>=-1&&value.state.sort<facts.columns.length?value.state.sort:-1;direction=value.state?.direction===-1?-1:1;restored=true;group(value);reply('restored',seq);return}
    if(!restored)return;
    if(value.type==='sort'){direction=sort===value.column?-direction:1;sort=value.column;reply('view-state',seq)}
    if(value.type==='select')postMessage({type:'select',seq,row:value.row});
    if(value.type==='group'){group(value);reply('paint',seq)}
    if(value.type==='reset'){sort=-1;direction=1;query='';selected='';reply('reset',seq)}
    if(value.type==='capture')reply('view-state',seq);
    if(value.type==='export')postMessage({type:'export-rows',seq,rows:visible().map(row=>row.index)});
  };
})();

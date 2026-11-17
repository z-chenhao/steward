// Private owning Cards policy. No DOM, parent handles, image bytes or refs.
'use strict';
(()=>{
  let titles,positions,index=0,selected='',restored=false;
  const requested=new Set();
  const projection=()=>({index,previousDisabled:!restored||index===0,nextDisabled:!restored||index===titles.length-1,counter:`${index+1} / ${titles.length}`,selected:titles.map(title=>title===selected)});
  const reply=(type,seq,extra={})=>postMessage({type,seq,projection:projection(),...extra});
  onmessage=event=>{
    const value=event.data,seq=value.seq;
    if(value.type==='init'&&!titles){titles=value.titles;positions=value.positions;reply('ready',seq,{prefetchMargin:240});return}
    if(!titles)return;
    if(Array.isArray(value.positions))positions=value.positions;
    if(value.type==='restore'&&!restored){
      index=Number.isSafeInteger(value.state?.index)?Math.max(0,Math.min(titles.length-1,value.state.index)):0;
      selected=typeof value.selected==='string'?value.selected:'';restored=true;
      reply('restored',seq,{scroll:{left:positions[index],behavior:'instant'}});return;
    }
    if(!restored)return;
    if(value.type==='move'){
      const next=Math.max(0,Math.min(titles.length-1,index+value.delta)),changed=index!==next;
      index=next;reply(changed?'view-state':'paint',seq,{scroll:{left:positions[index],behavior:'smooth'}});return;
    }
    if(value.type==='resize'){positions=value.positions;reply('paint',seq,{scroll:{left:positions[index],behavior:'instant'}});return}
    if(value.type==='scroll'){
      const distance=item=>Math.abs(positions[item]-value.left);
      const nearest=titles.reduce((best,_,i)=>distance(i)<distance(best)?i:best,index),changed=index!==nearest;
      index=nearest;reply(changed?'view-state':'paint',seq);return;
    }
    if(value.type==='group'){selected=value.selected;reply('paint',seq);return}
    if(value.type==='select'){reply('select',seq,{item:value.item});return}
    if(value.type==='capture'){reply('view-state',seq);return}
    if(value.type==='visible'){
      const items=value.items.filter(item=>!requested.has(item));for(const item of items)requested.add(item);
      reply('images',seq,{items});
    }
  };
})();

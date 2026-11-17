// Fixed Cards DOM mechanism. Only the opaque Worker executes package code.
(program,restoreText=()=>true)=>{
  let worker,url,stopped=false,sequence=0,initialized=false,restoring=false,restored=false,pendingGroup;
  const pending=new Map();
  const post=value=>parent.postMessage({channel:'steward-cards-view',...value},'*');
  const dispose=()=>{stopped=true;worker?.terminate();if(url)URL.revokeObjectURL(url);for(const entry of pending.values())clearTimeout(entry.timer);pending.clear()};
  const fail=()=>{if(!stopped){dispose();post({type:'failed'})}};
  const start=()=>{
    if(!restoreText()){fail();return}
    const root=document.querySelector('.native-carousel'),list=root?.querySelector('.native-carousel-list');
    const cards=list?[...list.children]:[],controls=root?.querySelector('.native-carousel-controls');
    if(!cards.length||cards.length>16||!controls){fail();return}
    const buttons=[...controls.querySelectorAll('button')],counter=controls.querySelector('span');
    const titles=cards.map(card=>card.querySelector('.native-card-title'));
    if(buttons.length!==2||!counter||titles.some(title=>!title)){fail();return}
    const [previous,next]=buttons;previous.disabled=next.disabled=true;
    const images=new Map();cards.forEach((card,item)=>{const image=card.querySelector('img[data-card-image]');if(image)images.set(image,item)});
    let geometryLimit=0;
    const positions=()=>{const values=cards.map(card=>Math.max(0,Math.min(card.offsetLeft-list.offsetLeft,list.scrollWidth-list.clientWidth)));geometryLimit=Math.max(geometryLimit,...values);return values};
    const validIndex=item=>Number.isInteger(item)&&item>=0&&item<cards.length;
    const validProjection=p=>p&&validIndex(p.index)&&typeof p.previousDisabled==='boolean'&&typeof p.nextDisabled==='boolean'&&typeof p.counter==='string'&&p.counter.length<=128&&Array.isArray(p.selected)&&p.selected.length===cards.length&&p.selected.every(v=>typeof v==='boolean');
    const send=(command,meta={})=>{
      if(stopped)return;
      if(pending.size>=32){fail();return}
      const seq=++sequence,timer=setTimeout(fail,command.type==='init'?10000:2000);
      pending.set(seq,{type:command.type,meta,timer});worker.postMessage({...command,seq});
    };
    let observer;
    const finishRestore=()=>{
      if(pendingGroup){const group=pendingGroup;pendingGroup=undefined;send(group,{finishRestore:true});return}
      restored=true;post({type:'restored'});for(const image of images.keys())observer.observe(image);
    };
    try{url=URL.createObjectURL(new Blob([program],{type:'text/javascript'}));worker=new Worker(url)}catch{fail();return}
    worker.onerror=worker.onmessageerror=fail;
    worker.onmessage=event=>{
      if(stopped)return;
      const value=event.data,entry=pending.get(value?.seq);
      if(!entry){fail();return}
      const expected={init:['ready'],restore:['restored'],move:['view-state','paint'],resize:['paint'],scroll:['view-state','paint'],group:['paint'],select:['select'],capture:['view-state'],visible:['images']}[entry.type];
      if(!expected?.includes(value.type)||!validProjection(value.projection)){fail();return}
      if(value.scroll&&(!Number.isFinite(value.scroll.left)||value.scroll.left<0||value.scroll.left>geometryLimit||!['instant','smooth'].includes(value.scroll.behavior))){fail();return}
      if(value.type==='ready'&&(!Number.isFinite(value.prefetchMargin)||value.prefetchMargin<0||value.prefetchMargin>4096)){fail();return}
      if(value.type==='select'&&(!validIndex(value.item)||value.item!==entry.meta.item)){fail();return}
      if(value.type==='images'&&(!Array.isArray(value.items)||value.items.length>cards.length||new Set(value.items).size!==value.items.length||!value.items.every(item=>validIndex(item)&&[...images.values()].includes(item)))){fail();return}
      clearTimeout(entry.timer);pending.delete(value.seq);
      const p=value.projection;
      previous.disabled=!restored||p.previousDisabled;next.disabled=!restored||p.nextDisabled;counter.textContent=p.counter;
      cards.forEach((card,item)=>{card.dataset.selected=String(p.selected[item]);titles[item].setAttribute('aria-pressed',String(p.selected[item]))});
      if(value.scroll)list.scrollTo({...value.scroll,left:Math.min(value.scroll.left,Math.max(0,list.scrollWidth-list.clientWidth))});
      if(value.type==='ready'){
        initialized=true;URL.revokeObjectURL(url);url=undefined;
        observer=new IntersectionObserver(entries=>{const items=entries.filter(entry=>entry.isIntersecting).map(entry=>images.get(entry.target));if(restored&&items.length)send({type:'visible',items})},{root:list,rootMargin:`0px ${value.prefetchMargin}px`});
        post({type:'ready'});
      }
      if(value.type==='restored'||entry.meta.finishRestore){
        finishRestore();if(restored){previous.disabled=p.previousDisabled;next.disabled=p.nextDisabled}
      }
      if(value.type==='view-state')post({type:'view-state',state:{index:p.index},...(entry.meta.id?{id:entry.meta.id}:{})});
      if(value.type==='select')post({type:'select',item:value.item});
      if(value.type==='images')for(const item of value.items)post({type:'image-request',item});
    };
    addEventListener('message',event=>{
      const value=event.data;if(stopped||event.source!==parent||value?.channel!=='steward-cards-view')return;
      if(value.type==='dispose'){observer?.disconnect();dispose();return}
      if(value.type==='restore'&&initialized&&!restoring){restoring=true;send({type:'restore',state:value.state,selected:value.selected,positions:positions()});return}
      if(value.type==='group'&&typeof value.selected==='string'&&value.selected.length<=300){
        if(restoring&&!restored)pendingGroup={type:'group',selected:value.selected};else if(restored)send({type:'group',selected:value.selected});return;
      }
      if(!restored)return;
      if(value.type==='capture'&&typeof value.id==='string'&&value.id.length<=64)send({type:'capture'},{id:value.id});
      if(value.type==='image'&&validIndex(value.item)&&value.image instanceof Blob&&['image/png','image/jpeg','image/webp','image/gif'].includes(value.image.type)){
        const image=cards[value.item].querySelector('img[data-card-image]');
        if(image){const source=URL.createObjectURL(value.image),release=()=>URL.revokeObjectURL(source);image.addEventListener('load',release,{once:true});image.addEventListener('error',release,{once:true});image.loading='eager';image.src=source}
      }
      if(value.type==='image-error'&&validIndex(value.item)){const image=cards[value.item].querySelector('img[data-card-image]');if(image)image.dataset.imageUnavailable='true'}
    });
    previous.addEventListener('click',()=>{if(restored)send({type:'move',delta:-1,positions:positions()})});
    next.addEventListener('click',()=>{if(restored)send({type:'move',delta:1,positions:positions()})});
    titles.forEach((title,item)=>title.addEventListener('click',()=>{if(restored)send({type:'select',item},{item})}));
    let scrollQueued=false,resizeQueued=false;
    list.addEventListener('scroll',()=>{if(!restored||scrollQueued)return;scrollQueued=true;requestAnimationFrame(()=>{scrollQueued=false;if(!stopped)send({type:'scroll',left:list.scrollLeft,positions:positions()})})});
    new ResizeObserver(()=>{if(!restored||resizeQueued)return;resizeQueued=true;requestAnimationFrame(()=>{resizeQueued=false;if(!stopped)send({type:'resize',positions:positions()})})}).observe(list);
    send({type:'init',titles:titles.map(title=>title.textContent),positions:positions()});
  };
  addEventListener('pagehide',dispose);
  if(document.readyState==='loading')addEventListener('DOMContentLoaded',start,{once:true});else start();
}

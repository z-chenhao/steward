// Fixed Host mechanism. Candidate code is data until passed to an opaque Blob
// Worker. It never executes in this DOM realm or receives a parent/ref handle.
(program,readonly=false)=>{
  let worker,workerURL,stopped=false,initialized=false,restored=false,restoring=false,sequence=0;
  const pending=new Map();
  const channel=readonly?'steward-plan-view':'steward-table-view';
  const post=value=>{if(!readonly||value.type==='height'||value.type==='failed')parent.postMessage({channel,...value},'*')};
  const dispose=()=>{stopped=true;worker?.terminate();if(workerURL)URL.revokeObjectURL(workerURL);for(const p of pending.values())clearTimeout(p.timer);pending.clear()};
  const fail=()=>{if(!stopped){dispose();post({type:'failed'})}};
  const start=()=>{
    const root=document.querySelector('.answer-table');if(!root){fail();return}
    let facts;try{facts=JSON.parse(new TextDecoder().decode(Uint8Array.from(atob(root.getAttribute('data-table-facts')||''),c=>c.charCodeAt(0))))}catch{fail();return}
    root.removeAttribute('data-table-facts');
    const body=root.querySelector('tbody'),headers=[...root.querySelectorAll('th')],original=[...body.rows];
    if(typeof facts?.title!=='string'||typeof facts.source!=='string'||!Array.isArray(facts.columns)||!facts.columns.length||facts.columns.length>12||facts.columns.length!==headers.length||!facts.columns.every(c=>typeof c==='string')||!Array.isArray(facts.rows)||facts.rows.length>200||facts.rows.length!==original.length||!facts.rows.every(row=>Array.isArray(row)&&row.length===headers.length&&row.every(c=>typeof c==='string'))){fail();return}
    if(!readonly)root.querySelectorAll('button').forEach(button=>button.disabled=true);
    const iconTemplate=root.querySelector('template[data-table-sort-icon]'),icon=iconTemplate?.content.firstElementChild;
    if(!icon||icon.localName!=='svg'){fail();return}iconTemplate.remove();
    root.setAttribute('aria-label',facts.title);root.querySelector('caption').textContent=facts.title;root.querySelector('.answer-table-source').textContent=facts.source;
    headers.forEach((header,column)=>header.querySelector('button').textContent=facts.columns[column]);
    original.forEach((row,index)=>[...row.cells].forEach((cell,column)=>(column===0?cell.querySelector('button'):cell).textContent=facts.rows[index][column]));
    const indices=value=>Array.isArray(value)&&value.length<=original.length&&value.every(i=>Number.isInteger(i)&&i>=0&&i<original.length)&&new Set(value).size===value.length;
    const bools=(value,length)=>Array.isArray(value)&&value.length===length&&value.every(v=>typeof v==='boolean');
    const valid=p=>p&&Number.isInteger(p.sort)&&p.sort>=-1&&p.sort<headers.length&&(p.direction===1||p.direction===-1)&&indices(p.rows)&&bools(p.numeric,headers.length)&&bools(p.selected,original.length)&&bools(p.pressed,original.length)&&typeof p.selectionActive==='boolean'&&typeof p.regionLabel==='string'&&p.regionLabel.length<=1024&&typeof p.emptyMessage==='string'&&p.emptyMessage.length<=2000;
    const empty=document.createElement('p');
    const paint=p=>{
      headers.forEach((header,column)=>{header.dataset.numeric=String(p.numeric[column]);header.setAttribute('aria-sort',p.sort===column?(p.direction===1?'ascending':'descending'):'none');header.querySelector('svg')?.remove();if(p.sort===column)header.querySelector('button').append(icon.cloneNode(true))});
      original.forEach((row,index)=>{if(p.selectionActive)row.setAttribute('aria-selected',String(p.selected[index]));else row.removeAttribute('aria-selected');[...row.cells].forEach((cell,column)=>cell.dataset.numeric=String(p.numeric[column]));row.querySelector('button').setAttribute('aria-pressed',String(p.pressed[index]))});
      body.replaceChildren(...p.rows.map(i=>original[i]));empty.remove();empty.textContent=p.emptyMessage;if(!p.rows.length)root.querySelector('.answer-table-scroll').append(empty);root.querySelector('[role=region]').setAttribute('aria-label',p.regionLabel);
    };
    let tableLocale,pendingGroup;
    const send=(command,meta={})=>{
      if(stopped||pending.size>=32){fail();return}
      const seq=++sequence,timer=setTimeout(fail,command.type==='init'?10000:2000);pending.set(seq,{command:command.type,meta,timer});worker.postMessage({...command,seq});
    };
    const finishRestore=()=>{
      if(pendingGroup){const group=pendingGroup;pendingGroup=undefined;send(group,{finishRestore:true});return}
      restored=true;root.querySelectorAll('button').forEach(button=>button.disabled=false);post({type:'restored'});
    };
    try{workerURL=URL.createObjectURL(new Blob([program],{type:'text/javascript'}));worker=new Worker(workerURL)}catch{fail();return}
    worker.onerror=worker.onmessageerror=fail;
    worker.onmessage=event=>{
      if(stopped)return;
      const value=event.data,p=pending.get(value?.seq);
      if(!p){fail();return}
      const expected={init:'ready',restore:'restored',sort:'view-state',select:'select',group:'paint',environment:'paint',reset:'reset',capture:'view-state',export:'export-rows'}[p.command];
      if(value.type!==expected){fail();return}
      if(value.type==='select'){
        if(!Number.isInteger(value.row)||value.row<0||value.row>=original.length){fail();return}
      }else if(value.type==='export-rows'){
        if(!indices(value.rows)){fail();return}
      }else if(!valid(value.projection)){fail();return}
      clearTimeout(p.timer);pending.delete(value.seq);
      if(value.type==='select'){post({type:'select',row:value.row});return}
      if(value.type==='export-rows'){post({type:'export-rows',id:p.meta.id,rows:value.rows});return}
      paint(value.projection);
      if(value.type==='ready'){initialized=true;URL.revokeObjectURL(workerURL);workerURL=undefined;post({type:'ready'})}
      if(value.type==='restored'||p.meta.finishRestore)finishRestore();
      if(value.type==='view-state'||value.type==='reset')post({type:'view-state',state:{sort:value.projection.sort,direction:value.projection.direction},...(p.meta.id?{id:p.meta.id}:{})});
      if(value.type==='reset')post({type:'reset-group'});
    };
    const environment=value=>{
      const html=document.documentElement;
      if(value.theme==='light'||value.theme==='dark'){html.style.colorScheme=value.theme;html.dataset.widgetTheme=value.theme}
      if(Number.isFinite(value.viewportWidth)&&value.viewportWidth>0&&value.viewportWidth<=100000)html.toggleAttribute('data-host-compact',value.viewportWidth<=Number(html.dataset.compactMaxWidth));
      for(const key of ['--card','--border','--foreground','--muted-foreground','--hover','--font-ui','--font-sans','--control-border','--focus']){const token=value.tokens?.[key];if(typeof token==='string'&&token.length<=256)html.style.setProperty(key,token)}
      let changed=false;
      if(typeof value.locale==='string'&&value.locale.length<=64&&value.locale!==tableLocale){try{Intl.getCanonicalLocales(value.locale);tableLocale=value.locale;changed=true}catch{}}
      if(typeof value.language==='string'&&(value.language==='en'||value.language==='zh-CN')&&html.lang!==value.language){html.lang=value.language;changed=true}
      if(initialized&&changed)send({type:'environment',locale:tableLocale,language:html.lang});
      report();
    };
    addEventListener('message',event=>{
      const value=event.data;if(stopped||event.source!==parent||value?.channel!==channel)return;
      if(value.type==='dispose'){dispose();return}
      if(value.type==='theme'){environment(value);return}
      if(readonly)return;
      if(value.type==='restore'&&initialized&&!restoring){restoring=true;send({type:'restore',state:value.state,query:value.query,selected:value.selected});return}
      if(value.type==='group'&&restoring&&!restored){pendingGroup={type:'group',query:value.query,selected:value.selected};return}
      if(!restored)return;
      if(value.type==='group')send({type:'group',query:value.query,selected:value.selected});
      if(value.type==='reset')send({type:'reset'});
      if((value.type==='capture'||value.type==='export')&&typeof value.id==='string'&&value.id.length<=64)send({type:value.type},{id:value.id});
    });
    headers.forEach((header,column)=>header.querySelector('button').addEventListener('click',()=>{if(restored&&!readonly)send({type:'sort',column})}));
    original.forEach((row,index)=>row.querySelector('button').addEventListener('click',()=>{if(restored&&!readonly)send({type:'select',row:index})}));
    addEventListener('keydown',event=>{if(!readonly&&event.isTrusted&&event.key==='Escape'){event.preventDefault();post({type:'escape'})}},true);
    let last=0;
    const report=()=>{if(stopped)return;const height=Math.ceil(document.body.getBoundingClientRect().height);if(height!==last){last=height;post({type:'height',height})}};
    new ResizeObserver(report).observe(document.body);document.fonts.ready.then(report);
    send({type:'init',facts,locale:tableLocale,language:document.documentElement.lang||'en'});
  };
  addEventListener('pagehide',dispose);
  if(document.readyState==='loading')addEventListener('DOMContentLoaded',start,{once:true});else start();
}

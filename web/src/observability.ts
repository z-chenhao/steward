import {copyText} from './components/ui/clipboard';
// Progressive navigation for the server-rendered trace workbench. The server
// remains the only owner of query/selection state; native links work without JS.
import {enhanceTraceRequests} from './trace-request';
import {formatLocalTime} from './local-time';
import {revealCurrentTabs} from './shell';
import {createSideSheet} from './components/ui/sheet';
import {t} from './i18n';
import {enhanceSplitView} from './components/ui/split-view';
import {enhanceReadOnlyJson,setReadOnlyJsonMode} from './components/ui/read-only-json';
export function enhanceObservability() {
 if(!document.querySelector('#observability-page'))return;
 const listPage=!!document.querySelector('.obs-trace-list');
 let sheet:ReturnType<typeof createSideSheet>|null=null;
 let pending:AbortController|null=null;
 let disposeWorkbench=()=>{};
 let jsonMode:'formatted'|'json'='formatted';
 const collapsedByTrace=new Map<string,Set<string>>();
 const collapsed=(pane:HTMLElement)=>{const id=pane.closest<HTMLElement>('[data-trace-id]')?.dataset.traceId??'';if(!collapsedByTrace.has(id))collapsedByTrace.set(id,new Set());return collapsedByTrace.get(id)!};
 const renderTree=(pane:HTMLElement)=>{
  const nodes=Array.from(pane.querySelectorAll<HTMLElement>('.observation-node')),byId=new Map(nodes.map(node=>[node.dataset.observationId,node]));
  const query=pane.querySelector<HTMLInputElement>('.obs-tree-search input')?.value.toLocaleLowerCase().trim()??'',closed=collapsed(pane),visible=new Set<HTMLElement>();
  for(const node of nodes)if(!query||node.querySelector('.obs-node-name')?.textContent?.toLocaleLowerCase().includes(query)){
   let current:HTMLElement|undefined=node,blocked=false;
   for(let depth=0;current&&depth<=nodes.length;depth++){current=byId.get(current.dataset.parentObservation);if(current&&closed.has(current.dataset.observationId??''))blocked=true}
   if(query){current=node;for(let depth=0;current&&depth<=nodes.length;depth++){visible.add(current);current=byId.get(current.dataset.parentObservation)}}else if(!blocked)visible.add(node);
  }
  for(const node of nodes){node.hidden=!visible.has(node);const button=node.querySelector<HTMLButtonElement>('[data-observation-toggle]');if(button){const expanded=query?nodes.some(child=>child.dataset.parentObservation===node.dataset.observationId&&visible.has(child)):!closed.has(node.dataset.observationId??'');button.disabled=!!query;button.setAttribute('aria-expanded',String(expanded));button.setAttribute('aria-label',t(expanded?'Collapse node':'Expand node')+': '+node.querySelector('.obs-node-name')?.textContent)}}
 };
 const setMode=(scope:ParentNode)=>{setReadOnlyJsonMode(scope,jsonMode);for(const button of scope.querySelectorAll<HTMLButtonElement>('[data-json-mode]')){button.disabled=false;button.setAttribute('aria-pressed',String(button.dataset.jsonMode===jsonMode))}};
 const initializeWorkbench=(scope:ParentNode)=>{
  const root=scope.querySelector<HTMLElement>('[data-split-view]');if(!root)return;
  disposeWorkbench=enhanceSplitView(root,t('Resize trace tree'));
  for(const node of root.querySelectorAll<HTMLElement>('.observation-node')){node.id='trace-node-'+node.dataset.observationId;const button=node.querySelector('[data-observation-toggle]');if(button){const children=Array.from(root.querySelectorAll<HTMLElement>('.observation-node')).filter(child=>child.dataset.parentObservation===node.dataset.observationId);button.setAttribute('aria-controls',children.map(child=>'trace-node-'+child.dataset.observationId).join(' '))}}
  const pane=root.querySelector<HTMLElement>('.obs-tree-pane');if(pane)renderTree(pane);
  enhanceTraceRequests(root);enhanceReadOnlyJson(root);setMode(root);
  for(const button of root.querySelectorAll<HTMLButtonElement>('[data-copy-json]'))button.disabled=!root.querySelector('[data-json-source="'+button.dataset.copyJson+'"]');
 };
 initializeWorkbench(document);
 const dismiss=()=>{pending?.abort();disposeWorkbench();sheet?.dispose();sheet=null};
 if(listPage){
  const rail=document.querySelector<HTMLDetailsElement>('.obs-filter-rail');
  const compact=matchMedia('(max-width:760px)');let wideOpen=rail?.open??true;
  const fitFilters=()=>{if(!rail)return;if(compact.matches){wideOpen=rail.open;rail.open=false}else rail.open=wideOpen};
  compact.addEventListener('change',fitFilters);fitFilters();
  const table=document.querySelector<HTMLTableElement>('.obs-trace-list table')!;
  const columns=Array.from(table.querySelectorAll<HTMLElement>('col'));
  const widths=columns.map(col=>parseFloat(getComputedStyle(col).width)||0);
  const columnIndexes=[0,1,2,4,5,6,7,8,9,10];
  const toggles=Array.from(document.querySelectorAll<HTMLInputElement>('[data-trace-column]'));
  let visible=toggles.map((_,index)=>index<9);
  try{
   let saved=JSON.parse(localStorage.getItem('steward.trace.columns.v4')??'null');
   if(saved===null){
    let previous=JSON.parse(localStorage.getItem('steward.trace.columns.v3')??'null');
    if(previous===null){const older=JSON.parse(localStorage.getItem('steward.trace.columns.v2')??'null');if(Array.isArray(older)&&older.length===10)previous=[...older.slice(0,5),true,...older.slice(5)]}
    if(Array.isArray(previous)&&previous.length===11&&previous.every(value=>typeof value==='boolean'))saved=[previous[0],previous[1],previous[9],...previous.slice(3,9),previous[10]];
   }
   if(Array.isArray(saved)&&saved.length===toggles.length&&saved.every(value=>typeof value==='boolean'))visible=saved;
  }catch{}
  const render=()=>{
   toggles.forEach((toggle,index)=>{
    toggle.checked=visible[index];toggle.disabled=false;
    const col=columnIndexes[index];columns[col].hidden=!visible[index];
    table.querySelectorAll('tr').forEach(row=>{if(row.children.length===columns.length)(row.children[col] as HTMLElement).hidden=!visible[index]});
   });
   table.style.minWidth=columns.reduce((width,col,index)=>width+(col.hidden?0:widths[index]),0)+'px';
  };
  toggles.forEach((toggle,index)=>toggle.addEventListener('change',()=>{visible[index]=toggle.checked;render();try{localStorage.setItem('steward.trace.columns.v4',JSON.stringify(visible))}catch{}}));
  render();
 }

 for(const chart of document.querySelectorAll<HTMLElement>('[data-trace-activity]')){
  const buckets=JSON.parse(chart.dataset.traceActivity??'null') as {at:string;count:number}[]|null;
  if(!buckets?.length)continue;
  const maximum=Math.max(1,...buckets.map(bucket=>bucket.count));
  const ns='http://www.w3.org/2000/svg',svg=document.createElementNS(ns,'svg');svg.setAttribute('viewBox','0 0 960 80');svg.setAttribute('preserveAspectRatio','none');svg.setAttribute('aria-hidden','true');
  buckets.forEach((bucket,index)=>{const bar=document.createElementNS(ns,'rect');bar.setAttribute('x',String(index*20));bar.setAttribute('y',String(80-bucket.count/maximum*76));bar.setAttribute('width','16');bar.setAttribute('height',String(bucket.count/maximum*76));const title=document.createElementNS(ns,'title');title.textContent=(formatLocalTime(bucket.at,'datetime',document.documentElement.lang)?.full??'')+' · '+bucket.count;bar.append(title);svg.append(bar)});
  chart.append(svg);
  const axis=chart.nextElementSibling;
  const span=new Date(buckets.at(-1)!.at).valueOf()-new Date(buckets[0].at).valueOf();
  for(const index of [0,12,24,36,47]){const label=document.createElement('span');const date=new Date(buckets[index].at);label.textContent=formatLocalTime(date.toISOString(),span<86400000?'clock':'date',document.documentElement.lang)?.text??'';axis?.append(label)}
 }
 document.addEventListener('input',event=>{const field=event.target;if(field instanceof HTMLInputElement&&field.closest('.obs-tree-search')){const pane=field.closest<HTMLElement>('.obs-tree-pane');if(pane)renderTree(pane)}});
 document.addEventListener('click',event=>{
  const target=event.target as Element|null,treeToggle=target?.closest<HTMLButtonElement>('[data-observation-toggle]');
  if(treeToggle){const pane=treeToggle.closest<HTMLElement>('.obs-tree-pane'),node=treeToggle.closest<HTMLElement>('.observation-node');if(pane&&node){const closed=collapsed(pane),id=node.dataset.observationId!;if(closed.has(id))closed.delete(id);else closed.add(id);renderTree(pane)}return}
  const mode=target?.closest<HTMLButtonElement>('[data-json-mode]');if(mode){jsonMode=mode.dataset.jsonMode==='json'?'json':'formatted';const root=mode.closest('.obs-workbench');if(root)setMode(root);return}
  const copy=target?.closest<HTMLButtonElement>('[data-copy-json]');if(copy&&!copy.disabled){const pane=copy.closest('.obs-detail-pane'),source=pane?.querySelector<HTMLElement>('[data-json-source="'+copy.dataset.copyJson+'"]');if(source)void copyText(source.textContent??'').then(copied=>{if(!copied)throw Error('copy unavailable');copy.title=t('Copied');copy.dataset.copyLabel=t('Copied');copy.setAttribute('aria-label',t('Copied'));setTimeout(()=>{copy.setAttribute('aria-label',t(copy.dataset.copyJson==='input'?'Copy input':'Copy output'));copy.title='';delete copy.dataset.copyLabel},1600)}).catch(()=>{copy.title=t('Copy unavailable')})}
 });

 document.addEventListener('click',event=>{
  const button=(event.target as Element|null)?.closest<HTMLButtonElement>('[data-trace-timeline]');if(!button)return;
  const active=button.closest<HTMLElement>('.obs-tree-pane')?.toggleAttribute('data-timeline')??false;
  button.setAttribute('aria-pressed',String(active));
 });
 async function select(url:URL,push:boolean){
  pending?.abort();const controller=new AbortController();pending=controller;
  const current=document.querySelector<HTMLElement>('#observability-workbench');
  const treeTop=current?.querySelector('.obs-tree-pane')?.scrollTop??0;
  const detailTop=current?.querySelector('.obs-detail-pane')?.scrollTop??0;
  const treeFilter=current?.querySelector<HTMLInputElement>('.obs-tree-search input')?.value??'';
  const timeline=!!current?.querySelector('.obs-tree-pane[data-timeline]');
  const selected=current?.querySelector('.tree-heading[aria-current]')?.closest<HTMLElement>('[data-observation-id]')?.dataset.observationId;
  current?.classList.add('obs-loading');
  try{
   const response=await fetch(url,{credentials:'same-origin',cache:'no-store',signal:controller.signal,headers:{Accept:'text/html'}});
   if(!response.ok)throw new Error('trace read unavailable');
   const parsed=new DOMParser().parseFromString(await response.text(),'text/html');
   const next=parsed.querySelector('#observability-workbench');
   if(!next||(!current&&!listPage)||controller.signal.aborted)throw new Error('invalid trace view');
   // Capture the latest focus: the user may have tabbed during the request.
   const focus=current?.contains(document.activeElement)?document.activeElement:null;
   const focusedHref=focus instanceof HTMLAnchorElement?focus.href:null;
   const focusRegion=focus?.closest('.obs-tree-pane')?'.obs-tree-pane':focus?.closest('.obs-detail-pane')?'.obs-detail-pane':'.obs-trace-header';
   const focusedClasses=focus instanceof HTMLAnchorElement?Array.from(focus.classList):[];
   disposeWorkbench();
   if(current)current.replaceWith(next);
   else {
    sheet=createSideSheet(t('Trace details'),()=>{
     const depth=history.state?.traceSheetDepth;
     if(typeof depth==='number'&&depth>0)history.go(-depth);else dismiss();
    },{modal:false,storageKey:'steward.trace.peek-width.v1'});
    sheet.body.append(next);sheet.open();
   }
   if(sheet){
    sheet.full.href=url.href;
    const workbench=next.querySelector<HTMLElement>('[data-trace-title]');sheet.title.textContent=t('Trace')+' · '+(workbench?.dataset.traceTitle??'');
    const links=Array.from(document.querySelectorAll<HTMLAnchorElement>('.obs-trace-list a.trace-open'));
    const index=links.findIndex(link=>new URL(link.href).pathname===url.pathname);
    sheet.previous.hidden=false;sheet.next.hidden=false;sheet.previous.disabled=index<=0;sheet.next.disabled=index<0||index>=links.length-1;
    sheet.previous.onclick=()=>{if(index>0)void select(new URL(links[index-1].href),true)};
    sheet.next.onclick=()=>{if(index>=0&&index+1<links.length)void select(new URL(links[index+1].href),true)};
   }
   initializeWorkbench(next);
   const sameTrace=current?.querySelector<HTMLElement>('[data-trace-id]')?.dataset.traceId===next.querySelector<HTMLElement>('[data-trace-id]')?.dataset.traceId;
   const tree=next.querySelector('.obs-tree-pane');
   if(sameTrace){
    const filter=next.querySelector<HTMLInputElement>('.obs-tree-search input');if(filter){filter.value=treeFilter;filter.dispatchEvent(new Event('input',{bubbles:true}))}
    tree?.toggleAttribute('data-timeline',timeline);next.querySelector('[data-trace-timeline]')?.setAttribute('aria-pressed',String(timeline));
   }
   if(tree)tree.scrollTop=sameTrace?treeTop:0;
   const nextSelected=next.querySelector('.tree-heading[aria-current]')?.closest<HTMLElement>('[data-observation-id]')?.dataset.observationId;
   const detail=next.querySelector('.obs-detail-pane');if(detail&&selected&&selected===nextSelected)detail.scrollTop=detailTop;
   if(focusedHref){
    const replacement=Array.from(next.querySelectorAll<HTMLAnchorElement>(`${focusRegion} a`)).find(anchor=>anchor.href===focusedHref&&focusedClasses.every(name=>anchor.classList.contains(name)));
    replacement?.focus({preventScroll:true});
   }
   revealCurrentTabs(next);
   if(push)history.pushState(listPage?{traceSheetDepth:(history.state?.traceSheetDepth??0)+1}:null,'',url);
  }catch{if(!controller.signal.aborted)location.assign(url);}
  finally{if(pending===controller)pending=null;}
 }
 document.addEventListener('click',event=>{
  if(!(event instanceof MouseEvent)||event.button!==0||event.ctrlKey||event.metaKey||event.shiftKey||event.altKey)return;
  const target=event.target as Element|null;
  let anchor=target?.closest<HTMLAnchorElement>(listPage?'a.obs-select,a.trace-open,a.control-identifier-link,.context-reader a':'a.obs-select,a.control-identifier-link,.context-reader a');
  if(!anchor&&listPage&&!target?.closest('a,button,input,select,textarea,label,summary,[role=button],[contenteditable]')){
   if(window.getSelection()?.toString())return;
   anchor=target?.closest('tr[data-execution-id]')?.querySelector<HTMLAnchorElement>('a.trace-open');
   anchor?.focus({preventScroll:true});
  }
  if(!anchor||anchor.target||anchor.download)return;
  const url=new URL(anchor.href);
  if(url.origin!==location.origin||!url.pathname.startsWith('/developer/observability/'))return;
  event.preventDefault();void select(url,true);
 });
 window.addEventListener('popstate',()=>{if(listPage&&!history.state?.traceSheetDepth)dismiss();else if(location.pathname.startsWith('/developer/observability/'))void select(new URL(location.href),false);});
 window.addEventListener('pageshow',event=>{if(event.persisted&&listPage&&history.state?.traceSheetDepth)void select(new URL(location.href),false)});
 window.addEventListener('pagehide',()=>{pending?.abort();disposeWorkbench();sheet?.dispose();sheet=null});
}

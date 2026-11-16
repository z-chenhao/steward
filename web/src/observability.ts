import {disposeTraceProgram} from './trace-program';
import {enhanceTracePageControls,readTraceNavigation} from './trace-page-controls';
import {renderTraceTree,disposeTraceTree} from './trace-tree';
import {enhanceTraceColumns} from './trace-columns';
import {enhanceTraceWorkbenchControls} from './trace-workbench-controls';
// Progressive navigation for the server-rendered trace workbench. The server
// remains the only owner of query/selection state; native links work without JS.
import {enhanceTraceRequests} from './trace-request';
import {enhanceTraceActivity} from './trace-activity';
import {enhanceTraceTimeline} from './trace-timeline';
import type {TimeWindow} from './trace-time';
import {revealCurrentTabs} from './shell';
import {createSideSheet} from './components/ui/sheet';
import {t} from './i18n';
import {enhanceSplitView} from './components/ui/split-view';
import {enhanceReadOnlyJson} from './components/ui/read-only-json';
export function enhanceObservability() {
 const page=document.querySelector<HTMLElement>('#observability-page');if(!page)return;
 const pageRevision=page.dataset.traceReaderRevision??'';
 const listPage=!!document.querySelector('.obs-trace-list');
 let disposeColumns=enhanceTraceColumns(document);
 let sheet:ReturnType<typeof createSideSheet>|null=null;
 let pending:AbortController|null=null;
 let disposeWorkbench=()=>{};
 let jsonMode:'formatted'|'json'='formatted';
 const timelineWindows=new Map<string,TimeWindow>();
 let disposeActivity=enhanceTraceActivity(document);
 const collapsedByTrace=new Map<string,Set<string>>();
 const collapsed=(pane:HTMLElement)=>{const id=pane.closest<HTMLElement>('[data-trace-id]')?.dataset.traceId??'';if(!collapsedByTrace.has(id))collapsedByTrace.set(id,new Set());return collapsedByTrace.get(id)!};
 const renderTree=(pane:HTMLElement)=>{void renderTraceTree(pane,collapsed(pane))};
 const initializeWorkbench=(scope:ParentNode,url=new URL(location.href))=>{
  const root=scope.querySelector<HTMLElement>('[data-split-view]');if(!root)return;
  const disposeSplit=enhanceSplitView(root,t('Resize trace tree'));
  const trace=root.dataset.traceId??'';const disposeTimeline=enhanceTraceTimeline(root,window=>{if(window)timelineWindows.set(trace,window);else timelineWindows.delete(trace)},timelineWindows.get(trace));
  let disposeControls=()=>{};let disposeRequests=()=>{};let disposeNavigation=()=>{};
  disposeWorkbench=()=>{disposeNavigation();disposeRequests();disposeControls();disposeTraceTree(root);disposeTimeline();disposeSplit()};
  for(const node of root.querySelectorAll<HTMLElement>('.observation-node')){node.id='trace-node-'+node.dataset.observationId;const button=node.querySelector('[data-observation-toggle]');if(button){const children=Array.from(root.querySelectorAll<HTMLElement>('.observation-node')).filter(child=>child.dataset.parentObservation===node.dataset.observationId);button.setAttribute('aria-controls',children.map(child=>'trace-node-'+child.dataset.observationId).join(' '))}}
  const pane=root.querySelector<HTMLElement>('.obs-tree-pane');if(pane)renderTree(pane);
  disposeRequests=enhanceTraceRequests(root);enhanceReadOnlyJson(root);
  disposeNavigation=enhanceTracePageControls(root,root.closest<HTMLElement>('#observability-workbench')??root,listPage,target=>{void select(target,true)});
  const links=Array.from(document.querySelectorAll<HTMLAnchorElement>('.obs-trace-list a.trace-open'));
  disposeControls=enhanceTraceWorkbenchControls(root,jsonMode,mode=>{jsonMode=mode},sheet?{links,index:links.findIndex(link=>new URL(link.href).pathname===url.pathname),previous:sheet.previous,next:sheet.next,navigate:target=>{void select(target,true)}}:undefined);
 };
 initializeWorkbench(document);
 const dismiss=()=>{pending?.abort();disposeWorkbench();sheet?.dispose();sheet=null};
 let disposePageControls=listPage?enhanceTracePageControls(page,document,true,target=>{void select(target,true)}):()=>{};

 document.addEventListener('input',event=>{const field=event.target;if(field instanceof HTMLInputElement&&field.closest('.obs-tree-search')){const pane=field.closest<HTMLElement>('.obs-tree-pane');if(pane)renderTree(pane)}});
 document.addEventListener('click',event=>{
  const target=event.target as Element|null,treeToggle=target?.closest<HTMLButtonElement>('[data-observation-toggle]');
  if(treeToggle){const pane=treeToggle.closest<HTMLElement>('.obs-tree-pane'),node=treeToggle.closest<HTMLElement>('.observation-node');if(pane&&node){const closed=collapsed(pane),id=node.dataset.observationId!;if(closed.has(id))closed.delete(id);else closed.add(id);renderTree(pane)}return}
 });

 async function select(url:URL,push:boolean){
  pending?.abort();
  // One document uses one frozen asset set, as in Memory Inspector.
  if((url.searchParams.get('reader_revision')??'')!==pageRevision){location.assign(url);return}
  const controller=new AbortController();pending=controller;
  const current=document.querySelector<HTMLElement>('#observability-workbench');
  const treeTop=current?.querySelector('.obs-tree-pane')?.scrollTop??0;
  const detailTop=current?.querySelector('.obs-detail-pane')?.scrollTop??0;
  const treeFilter=current?.querySelector<HTMLInputElement>('.obs-tree-search input')?.value??'';
  const selected=current?.querySelector('.tree-heading[aria-current]')?.closest<HTMLElement>('[data-observation-id]')?.dataset.observationId;
  current?.classList.add('obs-loading');
  try{
   const response=await fetch(url,{credentials:'same-origin',cache:'no-store',signal:controller.signal,headers:{Accept:'text/html'}});
   if(!response.ok)throw new Error('trace read unavailable');
   const parsed=new DOMParser().parseFromString(await response.text(),'text/html');
   if((parsed.querySelector<HTMLElement>('#observability-page')?.dataset.traceReaderRevision??'')!==pageRevision)throw new Error('different trace interpreter');
   const next=parsed.querySelector('#observability-workbench');
   if(!next||(!current&&!listPage)||controller.signal.aborted)throw new Error('invalid trace view');
   const sameTrace=current?.querySelector<HTMLElement>('[data-trace-id]')?.dataset.traceId===next.querySelector<HTMLElement>('[data-trace-id]')?.dataset.traceId;
   const nextSelected=next.querySelector('.tree-heading[aria-current]')?.closest<HTMLElement>('[data-observation-id]')?.dataset.observationId;
   const navigation=await readTraceNavigation(page!,{list:listPage,current:!!current,same_trace:sameTrace,same_selection:!!selected&&selected===nextSelected});
   if(controller.signal.aborted||pending!==controller)return;
   if(navigation.presentation==='page'){location.assign(url);return}
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

   }
   initializeWorkbench(next,url);
   const tree=next.querySelector('.obs-tree-pane');
   if(navigation.keep_filter){
    const filter=next.querySelector<HTMLInputElement>('.obs-tree-search input');if(filter){filter.value=treeFilter;filter.dispatchEvent(new Event('input',{bubbles:true}))}
   }
   if(tree)tree.scrollTop=navigation.keep_tree_scroll?treeTop:0;
   const detail=next.querySelector('.obs-detail-pane');if(detail&&navigation.keep_detail_scroll)detail.scrollTop=detailTop;
   if(navigation.restore_focus&&focusedHref){
    const replacement=Array.from(next.querySelectorAll<HTMLAnchorElement>(`${focusRegion} a`)).find(anchor=>anchor.href===focusedHref&&focusedClasses.every(name=>anchor.classList.contains(name)));
    replacement?.focus({preventScroll:true});
   }
   if(navigation.reveal_tabs)revealCurrentTabs(next);
   if(push)history.pushState(listPage?{traceSheetDepth:(history.state?.traceSheetDepth??0)+1}:null,'',url);
  }catch{if(!controller.signal.aborted)location.assign(url);}
  finally{if(pending===controller)pending=null;}
 }

 window.addEventListener('popstate',()=>{if(listPage&&!history.state?.traceSheetDepth)dismiss();else if(location.pathname.startsWith('/developer/observability/'))void select(new URL(location.href),false);});
 window.addEventListener('pageshow',event=>{if(!event.persisted)return;disposePageControls();disposeColumns();disposeColumns=enhanceTraceColumns(document);disposePageControls=listPage?enhanceTracePageControls(page,document,true,target=>{void select(target,true)}):()=>{};disposeActivity();disposeActivity=enhanceTraceActivity(document);if(listPage&&history.state?.traceSheetDepth)void select(new URL(location.href),false);else if(!listPage)initializeWorkbench(document)});
 window.addEventListener('pagehide',()=>{disposePageControls();disposeColumns();disposeActivity();pending?.abort();disposeWorkbench();sheet?.dispose();sheet=null;disposeTraceProgram(page)});
}

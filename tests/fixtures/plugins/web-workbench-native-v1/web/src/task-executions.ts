import {createSideSheet} from './components/ui/sheet';
import {enhanceProjections,releaseProjections} from './projections';
import {watchExecutionDetail} from './live-updates';
import {t} from './i18n';

// Execution inspection uses the same non-modal shell as Developer Trace.
// Configuration, approvals and feedback keep their native controlled pages.
export function enhanceTaskExecutions(){
 const list=document.querySelector<HTMLElement>('[data-execution-list]');
 if(!list)return;
 let sheet:ReturnType<typeof createSideSheet>|null=null;
 let pending:AbortController|null=null;
 let stopUpdates=()=>{};
 const dismiss=()=>{pending?.abort();pending=null;stopUpdates();if(sheet){releaseProjections(sheet.body);sheet.dispose();sheet=null}};
 async function select(url:URL,push:boolean){
  pending?.abort();const controller=new AbortController();pending=controller;
  try{
   const response=await fetch(url,{credentials:'same-origin',cache:'no-store',signal:controller.signal,headers:{Accept:'text/html'}});
   if(!response.ok)throw new Error('execution read unavailable');
   const parsed=new DOMParser().parseFromString(await response.text(),'text/html');
   const next=parsed.querySelector<HTMLElement>('#execution-detail');
   if(controller.signal.aborted)return;
   if(!next)throw new Error('invalid execution view');
   stopUpdates();
   if(!sheet){
    sheet=createSideSheet(t('Execution details'),()=>{
     const depth=history.state?.executionSheetDepth;
     if(typeof depth==='number'&&depth>0)history.go(-depth);else dismiss();
    },{modal:false,storageKey:'steward.execution.peek-width.v1',previousLabel:'Previous execution',nextLabel:'Next execution'});
   }
   releaseProjections(sheet.body);sheet.body.replaceChildren(next);sheet.body.scrollTop=0;
   sheet.title.textContent=t('Execution details');sheet.full.href=url.href;
   const links=Array.from(list!.querySelectorAll<HTMLAnchorElement>('[data-execution-open]'));
   const index=links.findIndex(link=>new URL(link.href).pathname===url.pathname);
   sheet.previous.hidden=false;sheet.next.hidden=false;
   sheet.previous.disabled=index<=0;sheet.next.disabled=index<0||index>=links.length-1;
   sheet.previous.onclick=()=>{if(index>0)void select(new URL(links[index-1].href),true)};
   sheet.next.onclick=()=>{if(index>=0&&index+1<links.length)void select(new URL(links[index+1].href),true)};
   stopUpdates=watchExecutionDetail(next,url.pathname);enhanceProjections(next);sheet.open();
   if(push&&url.href!==location.href)history.pushState({executionSheetDepth:(history.state?.executionSheetDepth??0)+1},'',url);
  }catch{if(!controller.signal.aborted)location.assign(url)}
  finally{if(pending===controller)pending=null}
 }
 document.addEventListener('click',event=>{
  if(!(event instanceof MouseEvent)||event.button!==0||event.ctrlKey||event.metaKey||event.shiftKey||event.altKey)return;
  const target=event.target as Element|null;
  let anchor=target?.closest<HTMLAnchorElement>('[data-execution-open],.control-sheet .execution-history a,.control-sheet .execution-heading a,[data-execution-refresh]');
  if(!anchor&&!target?.closest('a,button,input,select,textarea,label,summary,[role=button],[contenteditable]')){
   if(window.getSelection()?.toString())return;
   anchor=target?.closest('tr[data-execution-row]')?.querySelector<HTMLAnchorElement>('[data-execution-open]');
   anchor?.focus({preventScroll:true});
  }
  if(!anchor||anchor.target||anchor.download)return;
  const url=new URL(anchor.href);
  if(url.origin!==location.origin||!url.pathname.startsWith('/tasks/executions/'))return;
  event.preventDefault();void select(url,true);
 });
 window.addEventListener('popstate',()=>{if(!history.state?.executionSheetDepth)dismiss();else if(location.pathname.startsWith('/tasks/executions/'))void select(new URL(location.href),false)});
 window.addEventListener('pageshow',event=>{if(event.persisted&&history.state?.executionSheetDepth)void select(new URL(location.href),false)});
 window.addEventListener('pagehide',dismiss);
}

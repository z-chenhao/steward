import {traceProgram} from './trace-program';
import {legacyTracePageControls,legacyTraceNavigation} from './legacy/trace-page-controls-v1';
import {acceptTracePagePlan,acceptTraceNavigationPlan,type TracePageState,type TracePagePlan} from './trace-page-controls-contract';
// Bind only the known Trace links. The program receives no URLs or text.
const retained=new WeakMap<HTMLElement,TracePageState>();
const selectors={select:'a.obs-select',trace:'a.trace-open',identifier:'a.control-identifier-link',context:'.context-reader a'};
export function enhanceTracePageControls(root:HTMLElement,scope:HTMLElement|Document,list:boolean,select:(url:URL)=>void):()=>void {
 const entry=root.dataset.traceProgramRevision?traceProgram(root):undefined,rail=list?scope.querySelector<HTMLDetailsElement>('.obs-filter-rail'):null;
 const lifetime=new AbortController(),signal=lifetime.signal;
 let active=true,state:TracePageState=retained.get(root)??{compact:null,wide_open:rail?.open??true},plan:TracePagePlan|undefined,busy=false,queued=false;
 const current=()=>active&&root.isConnected&&(!entry||entry.active);
 const stop=()=>{active=false;queued=false;lifetime.abort()};
 const fail=()=>{stop();if(entry){entry.active=false;void entry.browser.then(browser=>browser.close()).catch(()=>{})}};
 const project=async()=>{
  if(!current())return;if(busy){queued=true;return}busy=true;
  try{do{queued=false;const open=rail?.open??false,input={list,width:window.innerWidth,open,state};
   const value=entry?await(await entry.browser).call({type:'page_controls',input}):legacyTracePageControls(input);
   if(!current())return;
   const next=acceptTracePagePlan(value);
   // Do not overwrite a newer native details interaction with an old snapshot.
   plan=next;state=next.state;retained.set(root,state);if(rail&&rail.open===open)rail.open=next.open;
  }while(queued&&current())}catch{if(active)fail()}finally{busy=false}
 };
 const ready=project();
 scope.addEventListener('click',event=>{
  if(!current()||!(event instanceof MouseEvent)||event.button!==0||event.ctrlKey||event.metaKey||event.shiftKey||event.altKey)return;
  const target=event.target as Element|null;
  if(list&&root.id==='observability-page'&&target?.closest('#observability-workbench'))return;
  if(!plan){
   const direct=target?.closest<HTMLAnchorElement>(Object.values(selectors).join(','));
   let anchor=direct;
   if(!anchor&&!target?.closest('a,button,input,select,textarea,label,summary,[role=button],[contenteditable]')){
    if(window.getSelection()?.toString())return;
    anchor=target?.closest('tr[data-execution-id]')?.querySelector<HTMLAnchorElement>('a.trace-open');
   }
   if(!anchor||anchor.target||anchor.download)return;
   const url=new URL(anchor.href);if(url.origin!==location.origin||!url.pathname.startsWith('/developer/observability/'))return;
   event.preventDefault();
   void ready.then(()=>{
    if(!current()||!plan)return;
    if(direct){
     if(plan.kinds.some(kind=>target?.closest(selectors[kind])===anchor))select(url);
     else location.assign(url.href);
    }else if(plan.row){
     if(plan.focus_row)anchor?.focus({preventScroll:true});
     select(url);
    }
   });
   return;
  }
  const selector=plan.kinds.map(kind=>selectors[kind]).join(',');
  let anchor=selector?target?.closest<HTMLAnchorElement>(selector):null;
  if(!anchor&&plan.row&&!target?.closest('a,button,input,select,textarea,label,summary,[role=button],[contenteditable]')){
   if(window.getSelection()?.toString())return;
   anchor=target?.closest('tr[data-execution-id]')?.querySelector<HTMLAnchorElement>('a.trace-open');
   if(plan.focus_row)anchor?.focus({preventScroll:true});
  }
  if(!anchor||anchor.target||anchor.download)return;
  const url=new URL(anchor.href);if(url.origin!==location.origin||!url.pathname.startsWith('/developer/observability/'))return;
  event.preventDefault();select(url);
 },{signal});
 if(rail)window.addEventListener('resize',()=>{void project()},{signal});
 return stop;
}

export async function readTraceNavigation(root:HTMLElement,input:import('./trace-page-controls-contract').TraceNavigationInput){
 if(!root.dataset.traceProgramRevision)return legacyTraceNavigation(input);
 const entry=traceProgram(root);
 try{
  if(!entry.active||!root.isConnected)throw Error('trace navigation unavailable');
  const value=await(await entry.browser).call({type:'navigation',input});
  if(!entry.active||!root.isConnected)throw Error('trace navigation unavailable');
  return acceptTraceNavigationPlan(value,input);
 }catch(error){entry.active=false;void entry.browser.then(browser=>browser.close()).catch(()=>{});throw error}
}

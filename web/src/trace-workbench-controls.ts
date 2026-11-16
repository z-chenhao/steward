import {copyText} from './components/ui/clipboard';
import {setReadOnlyJsonMode} from './components/ui/read-only-json';
import {traceProgram} from './trace-program';
import {legacyTraceControls} from './legacy/trace-workbench-controls-v1';
import {acceptTraceControlsPlan,type TraceJsonMode,type TraceControlsInput,type TraceControlsPlan,type TraceModeState} from './trace-workbench-controls-contract';
type Neighbors={links:HTMLAnchorElement[];index:number;previous:HTMLButtonElement;next:HTMLButtonElement;navigate:(url:URL)=>void};
// Cache the pure, frozen recipe before enabling controls. Clipboard writes stay
// directly in the user's gesture; no body crosses the isolated program seam.
export function enhanceTraceWorkbenchControls(root:HTMLElement,mode:TraceJsonMode,changed:(mode:TraceJsonMode)=>void,neighbors?:Neighbors):()=>void {
 const revision=root.dataset.traceProgramRevision,entry=revision?traceProgram(root):undefined;
 const modes=Array.from(root.querySelectorAll<HTMLButtonElement>('[data-json-mode]')),copies=Array.from(root.querySelectorAll<HTMLButtonElement>('[data-copy-json]'));
 const source=(key:'input'|'output')=>root.querySelector<HTMLElement>('[data-json-source="'+key+'"]');
 const input:TraceControlsInput={mode,input:!!source('input'),output:!!source('output'),language:document.documentElement.lang==='zh-CN'?'zh-CN':'en',index:neighbors?.index??-1,count:neighbors?.links.length??0};
 let active=true;const lifetime=new AbortController(),signal=lifetime.signal,timers=new Map<HTMLButtonElement,ReturnType<typeof setTimeout>>(),resetters=new Map<HTMLButtonElement,()=>void>();
 const current=()=>active&&root.isConnected&&(!entry||entry.active);
 const disable=()=>{for(const button of [...modes,...copies])button.disabled=true;if(neighbors){neighbors.previous.disabled=true;neighbors.next.disabled=true}};
 const stop=()=>{if(!active)return;active=false;lifetime.abort();for(const timer of timers.values())clearTimeout(timer);timers.clear();for(const reset of resetters.values())reset();resetters.clear();disable();if(neighbors){neighbors.previous.onclick=null;neighbors.next.onclick=null}};
 const renderMode=(state:TraceModeState)=>{setReadOnlyJsonMode(root,state.mode);for(const button of modes){button.disabled=false;button.setAttribute('aria-pressed',String(state[button.dataset.jsonMode as TraceJsonMode]))}};
 const apply=(value:unknown)=>{
  if(!current())return;const plan:TraceControlsPlan=acceptTraceControlsPlan(value,input.count);
  // Enabling copy can never authorize a source the Host did not actually read.
  if(plan.copies.some(copy=>!copy.disabled&&!source(copy.key)))throw Error('missing copy source');
  if(modes.some(button=>!['formatted','json'].includes(button.dataset.jsonMode??''))||copies.some(button=>!plan.copies.some(copy=>copy.key===button.dataset.copyJson)))throw Error('unknown controls');
  renderMode(plan.states[plan.initial]);changed(plan.states[plan.initial].mode);
  for(const button of modes)button.addEventListener('click',()=>{if(!current())return;const key=button.dataset.jsonMode as TraceJsonMode,state=plan.states[key];if(!state)return;renderMode(state);changed(state.mode)},{signal});
  for(const button of copies){
   const rule=plan.copies.find(copy=>copy.key===button.dataset.copyJson);if(!rule)throw Error('unknown copy control');button.disabled=rule.disabled;
   button.addEventListener('click',()=>{
    if(!current()||button.disabled)return;const body=source(rule.key);if(!body)return;
    void copyText(body.textContent??'').then(copied=>{
     if(!current())return;if(!copied)throw Error('copy unavailable');
     const previous=timers.get(button);if(previous)clearTimeout(previous);
     button.title=plan.copied;button.dataset.copyLabel=plan.copied;button.setAttribute('aria-label',plan.copied);
     const reset=()=>{button.setAttribute('aria-label',rule.reset_label);button.title='';delete button.dataset.copyLabel};resetters.set(button,reset);
     timers.set(button,setTimeout(()=>{timers.delete(button);resetters.delete(button);if(current())reset()},plan.reset_ms));
    }).catch(()=>{if(current())button.title=plan.unavailable});
   },{signal});
  }
  if(neighbors){for(const [button,index] of [[neighbors.previous,plan.previous],[neighbors.next,plan.next]] as const){button.hidden=false;button.disabled=index===null;button.onclick=index===null?null:()=>{if(current())neighbors.navigate(new URL(neighbors.links[index].href))}}}
 };
 disable();
 if(!entry){apply(legacyTraceControls(input));return stop}
 void entry.browser.then(browser=>browser.call({type:'workbench_controls',input})).then(apply).catch(()=>{if(active){stop();entry.active=false;void entry.browser.then(browser=>browser.close()).catch(()=>{})}});
 return stop;
}

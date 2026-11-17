import {traceProgram,disposeTraceProgram} from './trace-program';
import {legacyRenderTraceTree} from './legacy/trace-workbench-v1';
import {t} from './i18n';
const sequences=new WeakMap<HTMLElement,number>();
export const disposeTraceTree=disposeTraceProgram;
export async function renderTraceTree(pane:HTMLElement,closed:Set<string>){
 const root=pane.closest<HTMLElement>('[data-split-view]'),revision=root?.dataset.traceProgramRevision;
 if(!root||!revision){legacyRenderTraceTree(pane,closed,t);return}
 const nodes=Array.from(pane.querySelectorAll<HTMLElement>('.observation-node'));
 const seq=(sequences.get(pane)??0)+1;sequences.set(pane,seq);
 const entry=traceProgram(root);
 try{
  const browser=await entry.browser;
  if(!entry.active||!root.isConnected||sequences.get(pane)!==seq)return;
  const result=await browser.call({type:'tree',input:{nodes:nodes.map(node=>({id:node.dataset.observationId??'',parent:node.dataset.parentObservation,name:node.querySelector('.obs-node-name')?.textContent??'',type:node.dataset.observationType??''})),query:pane.querySelector<HTMLInputElement>('.obs-tree-search input')?.value??'',closed:Array.from(closed),language:document.documentElement.lang==='zh-CN'?'zh-CN':'en'}});
  if(!entry.active||!root.isConnected||sequences.get(pane)!==seq)return;
  if(!Array.isArray(result)||result.length!==nodes.length)throw Error('invalid tree projection');
  // Validate the whole reply before applying any DOM change. Only the original
  // ordered nodes and their existing toggle controls may be affected.
  const rows=result.map((value,index)=>{
   const button=nodes[index].querySelector<HTMLButtonElement>('[data-observation-toggle]');
   if(!value||typeof value!=='object'||Array.isArray(value)||value.id!==nodes[index].dataset.observationId||typeof value.hidden!=='boolean')throw Error('invalid node');
   if(button){if(typeof value.disabled!=='boolean'||!['true','false'].includes(value.expanded)||typeof value.label!=='string'||value.label.length>16384)throw Error('invalid toggle')}
   else if(value.disabled!==null||value.expanded!==null||value.label!==null)throw Error('invented toggle');
   return {value,button};
  });
  rows.forEach(({value,button},index)=>{nodes[index].hidden=value.hidden;if(button){button.disabled=value.disabled;button.setAttribute('aria-expanded',value.expanded);button.setAttribute('aria-label',value.label)}});
 }catch{
  if(entry.active&&sequences.get(pane)===seq){entry.active=false;void entry.browser.then(browser=>browser.close()).catch(()=>{});for(const node of nodes){const button=node.querySelector<HTMLButtonElement>('[data-observation-toggle]');if(button)button.disabled=true}}
 }
}

import type {TraceBrowser} from './trace-browser';
import {traceProgram,disposeTraceProgram} from './trace-program';
import {legacyTraceColumns,legacyTraceColumnControls} from './legacy/trace-workbench-v1';
type Plan={visible:boolean[];indexes:number[];storage_key:string;persist:boolean};
function acceptPlan(value:unknown,count:number,columns:HTMLElement[],mayPersist:boolean):Plan {
 if(!value||typeof value!=='object'||Array.isArray(value))throw Error('invalid columns');
 const p=value as Record<string,unknown>;
 if(!Array.isArray(p.visible)||p.visible.length!==count||!p.visible.every(value=>typeof value==='boolean')||!Array.isArray(p.indexes)||p.indexes.length!==count||new Set(p.indexes).size!==count||!p.indexes.every(index=>Number.isSafeInteger(index)&&index>=0&&index<columns.length&&!columns[index].hasAttribute('data-trace-required'))||p.storage_key!=='steward.trace.columns.v5'||typeof p.persist!=='boolean'||p.persist&&!mayPersist)throw Error('invalid column projection');
 return p as Plan;
}
export function enhanceTraceColumns(scope:ParentNode):()=>void {
 const table=scope.querySelector<HTMLTableElement>('.obs-trace-list table');if(!table)return ()=>{};
 const columns=Array.from(table.querySelectorAll<HTMLElement>('col')),widths=columns.map(col=>parseFloat(getComputedStyle(col).width)||0);
 const toggles=Array.from(scope.querySelectorAll<HTMLInputElement>('[data-trace-column]'));
 const page=scope.querySelector<HTMLElement>('#observability-page'),revision=page?.dataset.traceProgramRevision;
 const storage:Partial<Record<'steward.trace.columns.v2'|'steward.trace.columns.v3'|'steward.trace.columns.v4'|'steward.trace.columns.v5',string>>={},unavailable:(keyof typeof storage)[]=[];
 for(const key of ['steward.trace.columns.v2','steward.trace.columns.v3','steward.trace.columns.v4','steward.trace.columns.v5'] as const){try{const value=localStorage.getItem(key);if(value!==null)storage[key]=value}catch{unavailable.push(key)}}
 let active=true,busy=true,visible:boolean[]=[],browser:TraceBrowser|undefined;
 const lifetime=new AbortController(),signal=lifetime.signal;
 const disable=()=>{for(const toggle of toggles)toggle.disabled=true};
 const stop=()=>{if(!active)return;active=false;lifetime.abort();if(page&&revision)disposeTraceProgram(page);disable()};
 const render=(plan:Plan)=>{
  if(!active||!table.isConnected)return;
  visible=plan.visible;
  toggles.forEach((toggle,index)=>{
   toggle.checked=visible[index];toggle.disabled=false;
   const col=plan.indexes[index];columns[col].hidden=!visible[index];
   table.querySelectorAll('tr').forEach(row=>{if(row.children.length===columns.length)(row.children[col] as HTMLElement).hidden=!visible[index]});
  });
  table.style.minWidth=columns.reduce((width,col,index)=>width+(col.hidden?0:widths[index]),0)+'px';
  if(plan.persist){try{localStorage.setItem(plan.storage_key,JSON.stringify(visible))}catch{}}
  busy=false;
 };
 disable();
 for(const [index,toggle] of toggles.entries())toggle.addEventListener('change',()=>{
  if(!active||busy)return;busy=true;disable();
  const checked=toggle.checked;
  if(!revision){render(acceptPlan(legacyTraceColumnControls(visible,{index,checked}),toggles.length,columns,true));return}
  void browser!.call({type:'column_controls',input:{action:'toggle',visible,index,checked}}).then(result=>{if(active)render(acceptPlan(result,toggles.length,columns,true))}).catch(stop);
 },{signal});
 if(!revision){render(acceptPlan(legacyTraceColumnControls(legacyTraceColumns(storage,unavailable)),toggles.length,columns,false));return stop}
 void traceProgram(page!).browser.then(async connected=>{
  if(!active){connected.close();return}browser=connected;
  const initial=await browser.call({type:'columns',input:storage,unavailable});
  if(!Array.isArray(initial)||initial.length!==toggles.length||!initial.every(value=>typeof value==='boolean'))throw Error('invalid preferences');
  if(!active)return;
  const result=await browser.call({type:'column_controls',input:{action:'render',visible:initial}});
  if(active)render(acceptPlan(result,toggles.length,columns,false));
 }).catch(stop);
 return stop;
}

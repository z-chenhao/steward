import {localTimeElement} from './local-time';
import {t} from './i18n';
import {connectWorkspacePanel} from './workspace-panels';
type Worker={id:string;title:string;role:string;status:string;task_ref?:string;created_at:string;started_at?:string;ended_at?:string};
export function enhanceWorkers(){
 const panel=document.querySelector<HTMLDialogElement>('#worker-panel');
 const current=document.querySelector<HTMLElement>('[data-worker-current]');
 if(!panel||!current)return;
 const list=panel.querySelector<HTMLElement>('[data-worker-list]')!;
 const status=panel.querySelector<HTMLElement>('[data-worker-status]')!;
 let timer=0,request:AbortController|null=null,generation=0,denied=false,previous='';
 const say=(text:string)=>{const value=t(text);if(status.textContent!==value)status.textContent=value};
 function stop(){generation++;clearTimeout(timer);request?.abort();request=null}
 function rows(items:Worker[],more:boolean){
  say(!items.length?'No Worker executions yet.':more?'Showing the latest 50 executions.':'');
  const key=JSON.stringify(items);if(key===previous)return;previous=key;list.replaceChildren();
  for(const item of items){
   const row=document.createElement('a');row.className='worker-row';row.href='/developer/observability/'+encodeURIComponent(item.id);
   const body=document.createElement('div'),title=document.createElement('strong'),meta=document.createElement('p'),state=document.createElement('span');
   title.textContent=item.title||t('Worker');state.className='status-pill';state.dataset.status=item.status;state.textContent=t(item.status);
   meta.textContent=t(item.role);const at=item.started_at||item.created_at;meta.append(' · ',localTimeElement(at));
   body.append(title,meta);row.append(body,state);list.append(row);
  }
  say(!items.length?'No Worker executions yet.':more?'Showing the latest 50 executions.':'');
 }
 async function refresh(){
  if(document.hidden||denied)return;
  const id=++generation,controller=new AbortController();request=controller;
  const timeout=setTimeout(()=>controller.abort(),6000);
  try{
   const paths=panel!.open?['/api/workers/current','/api/workers']:['/api/workers/current'];
   const responses=await Promise.all(paths.map(path=>fetch(path,{credentials:'same-origin',cache:'no-store',signal:controller.signal})));
   if(id!==generation)return;
   if(responses.some(response=>response.status===401||response.status===403)){denied=true;previous='';current!.textContent=t('Sign in');current!.title=t('Sign in to view Worker activity.');list.replaceChildren();say('Sign in to view Worker activity.');return}
   if(responses.some(response=>!response.ok))throw Error('unavailable');
   const values=await Promise.all(responses.map(response=>response.json()));
   for(const value of values)if(!Array.isArray(value.items)||typeof value.more!=='boolean')throw Error('invalid');
   if(id!==generation)return;
   const parse=(items:unknown[]):Worker[]=>items.filter((v:unknown):v is Worker=>!!v&&typeof v==='object'&&['id','title','role','status','created_at'].every(k=>typeof(v as Record<string,unknown>)[k]==='string'));
   const active=parse(values[0].items).find(item=>item.status==='running');current!.textContent=active?`${t('Worker')} · ${active.title||t('running')}`:t('Worker');current!.title=active?`${active.title} · ${t(active.status)}`:t('Worker progress');
   if(values[1])rows(parse(values[1].items),values[1].more);
  }catch{if(id===generation){if(panel!.open)say('Worker activity is temporarily unavailable. Retrying…');else{current!.textContent=t('Worker · unavailable');current!.title=t('Worker activity is temporarily unavailable. Retrying…')}}}
  finally{clearTimeout(timeout);if(id===generation){request=null;if(!document.hidden&&!denied)timer=window.setTimeout(refresh,panel!.open?2000:5000)}}
 }
 connectWorkspacePanel(panel,()=>{stop();denied=false;say('Loading Worker activity…');void refresh()},()=>{stop();void refresh()});
 document.addEventListener('visibilitychange',()=>{stop();if(!document.hidden)void refresh()});window.addEventListener('pagehide',stop);void refresh();
}

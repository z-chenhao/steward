import {createRoot} from 'react-dom/client';
import {flushSync} from 'react-dom';
import {FilterMenu} from './components/ui/action-menu';

export function enhanceTaskFilters(){
 const host=document.querySelector<HTMLElement>('[data-task-filters]');
 const fallback=host?.querySelector<HTMLDetailsElement>('[data-task-filter-fallback]');
 const mount=host?.querySelector<HTMLElement>('[data-task-filter-mount]');
 const status=fallback?.querySelector<HTMLSelectElement>('select[name=view]');
 const needsMe=fallback?.querySelector<HTMLInputElement>('input[name=needs_me]');
 if(!fallback||!mount||!status||!needsMe)return;
 const listQuery=new URLSearchParams(location.search);
 const navigate=(view:string,checked:boolean)=>{
  const query=new URLSearchParams(listQuery);query.delete('cursor');query.delete('feedback_before');query.set('view',view);
  if(checked)query.set('needs_me','1');else query.delete('needs_me');
  location.assign('/tasks?'+query.toString());
 };
 flushSync(()=>createRoot(mount).render(<FilterMenu value={status.value} options={[...status.options].map(option=>({value:option.value,label:option.label}))} checked={needsMe.checked} checkboxLabel="Needs me" onValueChange={value=>navigate(value,needsMe.checked)} onCheckedChange={checked=>navigate(status.value,checked)}/>));
 fallback.hidden=true;
}

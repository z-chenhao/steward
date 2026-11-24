import {createRoot} from 'react-dom/client';
import {flushSync} from 'react-dom';
import {ActionMenu,type MenuAction} from './action-menu';

// Native commands own submission, authorization and confirmations. The shared
// Menu only presents those commands; without enhancement the disclosure works.
let nextMenu=0;
export function enhanceFormMenus(scope:ParentNode=document){
 for(const fallback of scope.querySelectorAll<HTMLDetailsElement>('[data-form-menu]:not([data-form-menu-enhanced])')){
  const summary=fallback.querySelector('summary');if(!summary)continue;
  const actions:MenuAction[]=[],id='control-form-menu-'+ ++nextMenu;
  for(const command of fallback.querySelectorAll<HTMLButtonElement|HTMLAnchorElement>('button,a[href]')){
   if(command instanceof HTMLButtonElement&&command.form){const form=command.form;form.dataset.controlMenuOpener=id;actions.push({label:command.textContent?.trim()??'',disabled:command.disabled,run:()=>form.requestSubmit(command)})}
   else if(command instanceof HTMLAnchorElement)actions.push({label:command.textContent?.trim()??'',run:()=>location.assign(command.href)});
  }
  if(!actions.length)continue;
  const mount=document.createElement('span');fallback.after(mount);
  const root=createRoot(mount);
  try{flushSync(()=>root.render(<ActionMenu actions={actions} label={summary.getAttribute('aria-label')??'Options'}/>));const trigger=mount.querySelector('button');if(trigger)trigger.id=id;fallback.dataset.formMenuEnhanced='true';fallback.hidden=true}
  catch{root.unmount();mount.remove()}
 }
}

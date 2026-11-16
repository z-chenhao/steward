import {t} from './i18n';
import {enhanceModelConnectionControls} from './model-connection-controls';
import {enhanceLegacyModelConnectionV1} from './legacy/model-connection-v1';
import {confirmSettingsAction,enhanceSettingsControls} from './settings-controls';
import {enhanceNotificationPreference} from './notification-preference';
import {enhanceLanguagePreference} from './preferences';

const signInOpenedHash='#auth-window-opened';

async function openSignIn(popup:Window, form:HTMLFormElement){
 let returnPath=location.pathname+location.search;
 let opened=false;
 try{
  const signal=AbortSignal.timeout(8000);
  const body=new URLSearchParams();
  for(const [name,value] of new FormData(form))if(typeof value==='string')body.append(name,value);
  const response=await fetch(form.action,{method:'POST',body,signal});
  if(!response.ok)return;
  const target=new URL(response.url);
  if(target.origin===location.origin&&(target.pathname==='/settings/models'||target.pathname==='/settings/models/new'))returnPath=target.pathname+target.search;
  while(!popup.closed&&!signal.aborted){
   const response=await fetch('/api/auth',{signal,cache:'no-store'});
   if(!response.ok)return;
   const state=await response.json();
   if(state.status!=='pending')return;
   const event=state.events?.find((event:{kind:string,url?:string})=>event.kind==='open_url'&&event.url);
   if(event){
    const url=new URL(event.url);
    if(url.protocol!=='https:'||url.username||url.password)return;
    popup.location.replace(url.href);
    opened=true;
    return;
   }
   await new Promise(resolve=>setTimeout(resolve,150));
  }
 }catch{
  // The local sign-in page remains the fallback for a blocked or failed launch.
 }finally{
  if(!opened)popup.close();
  const target=new URL(opened?returnPath+signInOpenedHash:returnPath,location.href);
  // Returning to Models can now be a hash-only navigation. Reload its
  // server state so the pending form and popup marker initialize together.
  if(target.pathname===location.pathname&&target.search===location.search){history.replaceState(history.state,'',target);location.reload()}
  else location.assign(target);

 }
}

// Settings use native navigation; a submission has one explicit attempt.
export function enhanceSettings(){
 try{enhanceLanguagePreference()}finally{document.querySelector('[data-language-preference]')?.setAttribute('data-language-ready','')}
 enhanceNotificationPreference();
 enhanceSettingsControls();
 const api=document.querySelector<HTMLFormElement>('[data-api-connection]');
 if(api?.hasAttribute('data-model-owner-connection'))enhanceModelConnectionControls(api);
 else if(api?.hasAttribute('data-model-legacy-v1'))enhanceLegacyModelConnectionV1(api);

 if(location.hash===signInOpenedHash){
  const signIn=document.querySelector<HTMLElement>('#signin-status');
  if(signIn?.querySelector('[data-signin-pending]'))signIn.dataset.authWindowOpened='';
  history.replaceState(history.state,'',location.pathname+location.search);
 }
 const forms=document.querySelectorAll<HTMLFormElement>('.settings-page form');
 const reset=new Map<HTMLFormElement,()=>void>();
 for(const form of forms){form.addEventListener('submit',event=>{
  if(event.defaultPrevented||form.dataset.autosaveReady||form.hasAttribute('data-app-action'))return;
  if(form.dataset.submitting){event.preventDefault();return}
  if(confirmSettingsAction(form,(event as SubmitEvent).submitter)){event.preventDefault();return}
  if(form.hasAttribute('data-open-signin')){
   const popup=window.open('about:blank','_blank');
   if(popup){
    popup.opener=null;
    event.preventDefault();
    void openSignIn(popup,form);
   }
  }
  const buttons=Array.from(form.querySelectorAll<HTMLButtonElement>('button[type=submit],button:not([type])'));
  const labels=buttons.map(button=>button.textContent??'');
  const disabled=buttons.map(button=>button.disabled);
  const submitter=(event as SubmitEvent).submitter;
  let action:HTMLInputElement|undefined;
  // Disabled controls are omitted from native form submission. Preserve the
  // clicked action before applying the double-submit guard.
  if(submitter instanceof HTMLButtonElement&&submitter.name){action=document.createElement('input');action.type='hidden';action.name=submitter.name;action.value=submitter.value;form.append(action)}
  form.dataset.submitting='true';form.setAttribute('aria-busy','true');
  const verifying=form.hasAttribute('data-model-verify');
  for(const button of buttons){button.disabled=true;if(!form.hasAttribute('data-preserve-submit-label'))button.textContent=verifying?t('Verifying…'):t('Working…')}
  reset.set(form,()=>{action?.remove();delete form.dataset.submitting;form.removeAttribute('aria-busy');buttons.forEach((button,i)=>{button.disabled=disabled[i];button.textContent=labels[i]})});
 })}
 window.addEventListener('pageshow',event=>{if(event.persisted){for(const restore of reset.values())restore();reset.clear()}});
}

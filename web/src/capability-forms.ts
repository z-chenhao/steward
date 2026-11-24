import {t} from './i18n';

// Fields belong to the Web form; serialized configuration stays on the Host.
// Validate and save one exact candidate at a time, with a revision check.
export function enhanceCapabilityForms(){
 for(const form of document.querySelectorAll<HTMLFormElement>('[data-capability-form]')){
  const id=form.querySelector<HTMLInputElement>('[name=capability]')!.value;
  const pagePath='/settings/capabilities/'+encodeURIComponent(id);
  // Native add/remove actions render a draft from POST. Keep its fields while
  // restoring the canonical page URL before subsequent autosave/readback.
  if(location.pathname==='/capability/configure')history.replaceState(history.state,'',pagePath);
  const enabled=form.querySelector<HTMLInputElement>('input[name="enabled"]');
  const mode=form.querySelector<HTMLSelectElement>('[data-capability-mode]');
  const status=form.querySelector<HTMLElement>('[data-capability-draft]');
  const values=()=>{
   const data=new URLSearchParams();
   for(const [key,value] of new FormData(form))if(typeof value==='string')data.append(key,value);
   return data;
  };
  const fields=()=>{const data=values();data.delete('revision');for(const key of Array.from(data.keys()))if(key.endsWith('.original'))data.delete(key);return data.toString()};
  const sync=()=>{
   const active=mode?mode.value==='custom':enabled?.checked;
   const group=form.querySelector<HTMLElement>('[data-capability-fields]');if(group)group.hidden=!active;
   form.querySelectorAll<HTMLInputElement|HTMLSelectElement|HTMLTextAreaElement|HTMLButtonElement>('[data-capability-fields] input:not([type="hidden"]),[data-capability-fields] select,[data-capability-fields] textarea,[data-capability-fields] button').forEach(control=>{control.disabled=!active||pending&&control instanceof HTMLButtonElement;if(!(control instanceof HTMLButtonElement))control.required=!!active&&control.hasAttribute('data-required')});
  };
  let pending=false,queued=false,baseline='',timer:ReturnType<typeof setTimeout>|undefined;
  const feedback=(message:string,error=false)=>{
   if(!status)return;
   status.replaceChildren(document.createTextNode(t(message)));status.setAttribute('role',error?'alert':'status');
   if(error){const retry=document.createElement('button');retry.type='button';retry.className='secondary setting-action';retry.textContent=t('Retry');retry.addEventListener('click',()=>void save());const refresh=document.createElement('a');refresh.href=location.pathname;refresh.textContent=t('Refresh to check');status.append(' ',retry,' ',refresh)}
  };
  const save=async()=>{
   clearTimeout(timer);sync();
   if(pending){queued=true;return}
   if(!form.checkValidity()){
    const firstInvalid=form.querySelector<HTMLElement>(':invalid');
    for(let parent=firstInvalid?.parentElement;parent;parent=parent.parentElement){
     if(parent instanceof HTMLDetailsElement)parent.open=true;
    }
    firstInvalid?.setAttribute('aria-invalid','true');
    feedback('Complete the required fields.');
    return;
   }
   form.querySelectorAll('[aria-invalid="true"]').forEach(node=>node.removeAttribute('aria-invalid'));
   const data=values(),sent=fields();
   if(sent===baseline){feedback('');return}
   data.set('action','apply');pending=true;queued=false;form.setAttribute('aria-busy','true');feedback('Saving…');
   // Row edits use native POST navigation. Disable them until this write settles.
   const rowButtons=Array.from(form.querySelectorAll<HTMLButtonElement>('button[name="action"]'));
   for(const button of rowButtons)button.disabled=true;
   const controller=new AbortController(),timeout=setTimeout(()=>controller.abort(),30000);
   let saved=false;
   try{
    // Named action buttons shadow HTMLFormElement.action in MCP forms.
    const response=await fetch(form.getAttribute('action')!,{method:'POST',body:data,credentials:'same-origin',signal:controller.signal});
    const parsed=new DOMParser().parseFromString(await response.text(),'text/html');
    if(!response.ok){const message=parsed.querySelector('.feedback.error')?.textContent?.trim();throw Error(message||t('Could not save. Your changes are kept.'))}
    const next=new URL(response.url),canonical=parsed.querySelector<HTMLFormElement>('[data-capability-form]');
    const revision=canonical?.querySelector<HTMLInputElement>('[name="revision"]')?.value;
    if(next.origin!==location.origin||next.pathname!==pagePath||!revision)throw Error(t('Could not confirm the save. Refresh to check.'));
    form.querySelector<HTMLInputElement>('[name="revision"]')!.value=revision;
    // Provider indexes refer to the persisted order, which may change on deletion.
    for(const input of form.querySelectorAll<HTMLInputElement>('input[name$=".original"]')){
     const updated=canonical?.elements.namedItem(input.name);if(updated instanceof HTMLInputElement)input.value=updated.value;
    }
    const current=document.querySelector('.capabilities-page>.page-heading .status-pill'),updated=parsed.querySelector('.capabilities-page>.page-heading .status-pill');
    if(current&&updated)current.replaceWith(updated);
    baseline=sent;
    saved=true;
    if(fields()!==sent)queued=true;
    else feedback('Saved.')
   }catch(error){feedback(error instanceof Error?error.message:'Could not save. Your changes are kept.',true)}
   finally{clearTimeout(timeout);pending=false;form.removeAttribute('aria-busy');for(const button of rowButtons)button.disabled=false;sync();if(saved&&queued)void save()}
  };
  const changed=(event:Event)=>{
   sync();document.querySelectorAll<HTMLElement>('[data-capability-feedback]').forEach(node=>{node.hidden=true});
   if(event.target===mode&&mode?.value==='custom'&&!form.checkValidity()){feedback('Complete the required fields.');return}
   clearTimeout(timer);timer=setTimeout(()=>void save(),event.type==='input'?700:0);
  };
  sync();baseline=fields();
  form.addEventListener('change',changed);
  form.addEventListener('input',event=>{if(!(event.target instanceof HTMLSelectElement))changed(event)});
  form.addEventListener('submit',event=>{const submitter=(event as SubmitEvent).submitter;if(submitter instanceof HTMLButtonElement&&submitter.name==='action'&&submitter.value!=='apply')return;event.preventDefault();void save()});
  form.dataset.autosaveReady='true';
  const fallback=form.querySelector<HTMLElement>('.capability-submit');
  if(fallback)fallback.hidden=true;
 }
}

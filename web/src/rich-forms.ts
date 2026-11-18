import {t} from './i18n';

export function enhanceRichForms(){
 document.addEventListener('input',event=>{
  const field=event.target;
  if(!(field instanceof HTMLTextAreaElement)||!field.closest('form[action="/ui/form"]'))return;
  field.setCustomValidity('');if(field.form)field.form.dataset.dirty='true';
 });
 document.addEventListener('submit',event=>{
  const form=event.target;
  if(!(form instanceof HTMLFormElement)||form.getAttribute('action')!=='/ui/form')return;
  event.preventDefault();
  if(form.dataset.submitting||form.dataset.submitted==='true')return;
  const fields=Array.from(form.querySelectorAll<HTMLTextAreaElement>('textarea[name^="field."]'));
  for(const field of fields){if(new TextEncoder().encode(field.value).length>4096)field.setCustomValidity(t('This field is too long. Shorten it before sending.'))}
  if(!form.reportValidity())return;
  const data=new URLSearchParams();for(const [key,value] of new FormData(form)){if(typeof value==='string')data.append(key,value)}
  const group=form.querySelector('fieldset'),status=form.querySelector<HTMLElement>('[role=status]');
  form.dataset.submitting='true';if(group)group.disabled=true;
  if(status)status.textContent=t('Sending…');
  void(async()=>{
   const controller=new AbortController(),timer=setTimeout(()=>controller.abort(),10000);
   let failure='Your response was not confirmed. Your answers are kept. Check the conversation before changing or retrying them.';
   try{
    const response=await fetch('/ui/form',{method:'POST',body:data,credentials:'same-origin',signal:controller.signal,headers:{Accept:'application/json'}});
    if(!response.ok){
     if(response.status===400)failure='This answer is too long or invalid. Edit it before sending again.';
     else if(response.status===409)failure='Your response was not saved. Review the current request before trying again.';
     else if(response.status===401||response.status===403)failure='Your access changed. Sign in again before sending. Your draft is kept.';
     throw new Error('response rejected');
    }
    if((await response.json()).accepted!==true)throw new Error('response unconfirmed');
    form.dataset.submitted='true';delete form.dataset.dirty;
    if(status)status.textContent=t('Saved.');
   }catch{
    if(status)status.textContent=t(failure);
   }finally{
    clearTimeout(timer);delete form.dataset.submitting;
    if(group)group.disabled=form.dataset.submitted==='true';
   }
  })();
 });
}

// A live projection can change when another tab submits a form in the same
// message. Preserve unrelated answers and focus across replacement of the island.
export function captureRichFormDrafts(scope:HTMLElement){
 const drafts=Array.from(scope.querySelectorAll<HTMLFormElement>('form[action="/ui/form"][data-dirty="true"]')).filter(form=>form.dataset.submitted!=='true').map(form=>({
  id:form.dataset.formId,
  fields:Array.from(form.querySelectorAll<HTMLTextAreaElement>('textarea[name^="field."]')).map(field=>({name:field.name,value:field.value,label:field.labels?.[0]?.textContent??field.name,focused:field===document.activeElement,start:field.selectionStart,end:field.selectionEnd})),
 }));
 return()=>{for(const draft of drafts){
  const form=Array.from(scope.querySelectorAll<HTMLFormElement>('form[action="/ui/form"]')).find(form=>form.dataset.formId===draft.id);
  if(form&&form.dataset.submitted!=='true'){
   for(const saved of draft.fields){const field=Array.from(form.querySelectorAll<HTMLTextAreaElement>('textarea')).find(field=>field.name===saved.name);if(field){field.value=saved.value;if(saved.focused){field.focus({preventScroll:true});field.setSelectionRange(saved.start,saved.end)}}}
   form.dataset.dirty='true';continue;
  }
  if(form&&draft.fields.every(saved=>Array.from(form.querySelectorAll<HTMLTextAreaElement>('textarea')).some(field=>field.name===saved.name&&field.value===saved.value)))continue;
  const details=document.createElement('details'),heading=document.createElement('summary');details.open=true;heading.textContent=t('Your unsent answers');details.append(heading);
  for(const saved of draft.fields){const label=document.createElement('label'),field=document.createElement('textarea');label.textContent=saved.label;field.value=saved.value;field.readOnly=true;label.append(field);details.append(label)}
  (form??document.getElementById('unsent-form-drafts'))?.append(details);
 }};
}

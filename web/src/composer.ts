import {randomUUID} from './lib/random-id';
import {flushUIStates} from './intelligent-ui/state';
import {showOutgoing,outgoingState,reconcileOutgoing,discardOutgoing} from './outgoing';
import {t} from './i18n';
import {buttonVariants,textareaStyle} from './components/ui/styles';
import {cn} from './lib/utils';

export function enhanceComposer(messageAccepted?: (target:URL)=>void){
 const root=document.querySelector<HTMLElement>('#composer-root');
 if(!root?.dataset.sourceId)return;
 const form=root.querySelector<HTMLFormElement>('form');
 const text=root.querySelector<HTMLTextAreaElement>('textarea');
 const submit=root.querySelector<HTMLButtonElement>('button[type="submit"]');
 const file=root.querySelector<HTMLInputElement>('input[type=file]');
 const remove=root.querySelector<HTMLButtonElement>('[data-remove-file]');
 const status=root.querySelector<HTMLElement>('.attachment-status');
 const source=form?.querySelector<HTMLInputElement>('input[name=source_id]');
 if(text)text.className=cn(textareaStyle,text.className);
 if(submit)submit.className=cn(buttonVariants({size:'icon'}),submit.className);
 // Native nodes survive enhancement, including a picker opened while JS loads.
 const showFile=()=>{if(status)status.textContent=file?.files?.[0]?.name??'';if(remove)remove.hidden=!file?.files?.length};
 file?.addEventListener('change',showFile);remove?.addEventListener('click',()=>{if(file)file.value='';showFile()});showFile();
 root.querySelector('[data-clear-task]')?.addEventListener('click',()=>{root.querySelector('.task-context-chip')?.remove();const url=new URL(location.href);url.searchParams.delete('task');history.replaceState(null,'',url)});
 let pending=false;
 if(!form||!source||!text)return;
 text.addEventListener('keydown',event=>{
  if(event.key!=='Enter'||event.shiftKey||event.isComposing||event.keyCode===229)return;
  event.preventDefault();
  if(event.repeat||pending||submit?.disabled)return;
  form.requestSubmit(submit??undefined);
 });
 form.addEventListener('submit',event=>{
  event.preventDefault();if(pending||submit?.disabled||form.dataset.modelSelecting)return;
  const data=new FormData(form),submittedSource=source.value;
  const submittedText=text.value,submittedFile=file?.files?.[0];
  const feedback=root.querySelector<HTMLElement>('#composer-feedback');
  document.querySelector('.welcome')?.remove();
  const entry=showOutgoing(submittedSource,submittedText,submittedFile);
  // Snapshot before clearing; uncertain retries reuse this exact source and file.
  source.value=root.dataset.sourceId=randomUUID().replaceAll('-','');
  text.value='';if(file)file.value='';showFile();if(feedback)feedback.textContent='';
  text.focus({preventScroll:true});requestAnimationFrame(()=>window.scrollTo({top:document.documentElement.scrollHeight,behavior:'instant'}));
  const send=async()=>{
   if(pending||entry.confirmed)return;
   pending=true;form.dataset.submitting='true';if(submit)submit.disabled=true;outgoingState(entry,'sending');
   const controller=new AbortController(),timeout=setTimeout(()=>controller.abort(),15000);
   try{
    await flushUIStates().catch(()=>{});
    const response=await fetch('/message',{method:'POST',body:data,credentials:'same-origin',signal:controller.signal,headers:{Accept:'application/json'}});
    if([400,413,422].includes(response.status)){
     outgoingState(entry,'rejected',()=>{
      if(pending||text.value||file?.files?.length){if(feedback)feedback.textContent=t('Finish or clear your current draft before editing this message.');return}
      source.value=root.dataset.sourceId=submittedSource;
      text.value=submittedText;
      if(file&&submittedFile){const transfer=new DataTransfer();transfer.items.add(submittedFile);file.files=transfer.files}
      showFile();discardOutgoing(entry);if(feedback)feedback.textContent='';text.focus();
     });
     return;
    }
    if(!response.ok)throw Error('send not confirmed');
    const result=await response.json();if(result.accepted!==true||typeof result.location!=='string')throw Error('invalid acknowledgement');
    const target=new URL(result.location,location.origin);if(target.origin!==location.origin||target.pathname!=='/')throw Error('invalid destination');
    reconcileOutgoing();outgoingState(entry,'accepted');
    if(!entry.confirmed)document.body.dataset.waitingReply='true';
    if(!messageAccepted){location.assign(target);return}
    if(!form.querySelector('[name=task_ref]'))target.searchParams.delete('task');
    messageAccepted(target);
   }catch{
    if(!entry.confirmed)outgoingState(entry,'uncertain',()=>void send());
   }finally{
    clearTimeout(timeout);pending=false;delete form.dataset.submitting;
    if(submit)submit.disabled=!!form.dataset.modelSelecting||document.querySelector<HTMLElement>('[data-can-send]')?.dataset.canSend==='false';
   }
  };
  void send();
 });
}

import {t} from '../../i18n';

// LAN HTTP and some embedded browsers have no async Clipboard API. Keep the
// fallback inside the user gesture, and restore the current focus/selection.
export async function copyText(value:string):Promise<boolean>{
 try{if(navigator.clipboard?.writeText){await navigator.clipboard.writeText(value);return true}}catch{}
 const active=document.activeElement,selection=document.getSelection();
 const ranges=selection?Array.from({length:selection.rangeCount},(_,i)=>selection.getRangeAt(i).cloneRange()):[];
 const start=active instanceof HTMLInputElement||active instanceof HTMLTextAreaElement?active.selectionStart:null;
 const end=active instanceof HTMLInputElement||active instanceof HTMLTextAreaElement?active.selectionEnd:null;
 const buffer=document.createElement('textarea');buffer.className='control-copy-buffer';buffer.value=value;buffer.readOnly=true;buffer.setAttribute('aria-hidden','true');document.body.append(buffer);
 try{buffer.focus({preventScroll:true});buffer.select();return !!document.execCommand('copy')}catch{return false}
 finally{
  buffer.remove();
  if(active instanceof HTMLElement&&active.isConnected){active.focus({preventScroll:true});if(start!==null&&end!==null&&(active instanceof HTMLInputElement||active instanceof HTMLTextAreaElement))active.setSelectionRange(start,end)}
  if(selection){selection.removeAllRanges();for(const range of ranges)if(range.startContainer.isConnected&&range.endContainer.isConnected)selection.addRange(range)}
 }
}

export function enhanceCopyControls(){
 const initialize=(scope:ParentNode)=>{
  const buttons=[...(scope instanceof HTMLButtonElement&&scope.matches('[data-copy-value]')?[scope]:[]),...scope.querySelectorAll<HTMLButtonElement>('button[data-copy-value]')];
  for(const button of buttons){button.disabled=false;button.closest('.control-identifier')?.setAttribute('data-copy-ready','')}
 };
 document.addEventListener('click',event=>{
  const button=(event.target as Element|null)?.closest<HTMLButtonElement>('button[data-copy-value]');if(!button||button.disabled)return;
  const value=button.dataset.copyValue;if(value===undefined)return;
  const label=button.getAttribute('aria-label')??t('Copy full ID');
  void copyText(value).then(copied=>{
   if(!button.isConnected)return;
   const owner=button.closest('.control-identifier')!;
   let status=owner.querySelector<HTMLElement>('[data-copy-status]');
   if(!status){status=document.createElement('span');status.className='sr-only';status.dataset.copyStatus='';status.setAttribute('role','status');owner.append(status)}
   status.textContent=t(copied?'Copied':'Copy unavailable');button.title=status.textContent;button.dataset.copyLabel=status.textContent;button.dataset.copyState=copied?'copied':'unavailable';
   if(!copied){
    let input=owner.querySelector<HTMLInputElement>('[data-copy-fallback]');
    if(!input){input=document.createElement('input');input.readOnly=true;input.className='control-copy-fallback';input.dataset.copyFallback='';input.setAttribute('aria-label',t('Select full value to copy'));owner.append(input)}
    input.value=value;input.focus({preventScroll:true});input.select();
   }
   setTimeout(()=>{if(button.isConnected){button.title=value;button.setAttribute('aria-label',label);delete button.dataset.copyLabel;delete button.dataset.copyState}},1800);
  });
 });
 initialize(document);
 new MutationObserver(records=>{for(const record of records)for(const node of record.addedNodes)if(node instanceof HTMLElement)initialize(node)}).observe(document.body,{childList:true,subtree:true});
}

import {createElement} from 'react';
import {createRoot} from 'react-dom/client';
import {flushSync} from 'react-dom';
import {t} from '../../i18n';
import {SelectView,type SelectOption} from './select-view';

type SelectControl={trigger:HTMLButtonElement;close:(focus?:boolean)=>void;sync:()=>void;setLocked:(locked:boolean,busy?:boolean)=>void;dispose:()=>void};
const controls=new WeakMap<HTMLSelectElement,SelectControl>();
let current:SelectControl|undefined,nextID=0;

// Native values preserve server forms and no-script fallback; all enhanced
// selectors use the same Base UI component, including language and React forms.
export function enhanceSelect(select:HTMLSelectElement):SelectControl|undefined{
 if(select.hasAttribute('data-control-react'))return;
 if(controls.has(select))return controls.get(select);
 const abort=new AbortController(),language=!!select.closest('[data-language-preference]');
 const id=select.id?select.id+'-picker':'control-select-'+ ++nextID;
 const labels=[...select.labels??[]],previousFor=labels.map(label=>label.htmlFor);
 const originalLabel=select.getAttribute('aria-label');
 const label=originalLabel??labels.map(label=>{const copy=label.cloneNode(true) as HTMLElement;copy.querySelectorAll('select,input,textarea,button,.control-select,.control-help,.preference-help').forEach(node=>node.remove());return copy.textContent?.trim()}).join(' ');
 const host=document.createElement('span');host.className='control-select-mount';
 const validation=document.createElement('p');validation.className='field-help danger-text';validation.setAttribute('role','alert');validation.id=id+'-error';validation.hidden=true;
 let wrapper=select.parentElement!;
 if(!wrapper.classList.contains('control-select')){wrapper=document.createElement('span');wrapper.className='control-select';select.before(wrapper);wrapper.append(select)}
 select.after(host,validation);
 const root=createRoot(host);let opened=false,locked=false,busy=false,invalid=false,disposed=false;
 const clearValidation=()=>{invalid=false;validation.textContent='';validation.hidden=true;control.trigger?.removeAttribute('aria-describedby')};
 const render=()=>{
  if(disposed)return;
  const options:SelectOption[]=[...select.options].map(option=>({value:option.value,label:option.label,disabled:option.disabled,lang:option.lang}));
  const disabled=locked||select.matches(':disabled');
  root.render(createElement(SelectView,{id,label,value:select.value,options,disabled,busy,invalid,open:opened&&!disabled,language,container:select.closest('dialog')??undefined,setOpen:open=>{
   if(open){current?.close();document.dispatchEvent(new Event('control-select-open'));current=control}
   else if(current===control)current=undefined;
   opened=open;render();
  },choose:value=>{
   if(disabled||select.value===value)return;
   clearValidation();
   select.value=value;
   select.dispatchEvent(new Event('input',{bubbles:true}));select.dispatchEvent(new Event('change',{bubbles:true}));
   render();
  }}));
 };
 const control:SelectControl={trigger:null!,sync:render,close:focus=>{opened=false;if(current===control)current=undefined;render();if(focus)control.trigger.focus()},setLocked:(value,saving=false)=>{locked=value;busy=saving;if(locked)opened=false;flushSync(render)},dispose:()=>{
  disposed=true;abort.abort();observer.disconnect();controls.delete(select);if(current===control)current=undefined;
  select.hidden=false;if(originalLabel!==null)select.setAttribute('aria-label',originalLabel);delete select.dataset.controlEnhanced;labels.forEach((label,index)=>label.htmlFor=previousFor[index]);
  validation.remove();
  queueMicrotask(()=>{root.unmount();host.remove()});
 }};
 controls.set(select,control);
 flushSync(render);control.trigger=host.querySelector('button')!;
 if(!control.trigger){root.unmount();host.remove();validation.remove();controls.delete(select);return}
 select.hidden=true;select.removeAttribute('aria-label');select.dataset.controlEnhanced='';labels.forEach(label=>label.htmlFor=id);
 select.addEventListener('change',()=>{clearValidation();render()},{signal:abort.signal});select.addEventListener('focus',()=>control.trigger.focus(),{signal:abort.signal});
 select.addEventListener('invalid',event=>{event.preventDefault();invalid=true;validation.textContent=select.validationMessage||t('Please fill out this field.');validation.hidden=false;control.trigger.setAttribute('aria-describedby',validation.id);render();control.trigger.focus()},{signal:abort.signal});
 select.addEventListener('control-value-sync',render,{signal:abort.signal});
 select.form?.addEventListener('reset',()=>queueMicrotask(()=>{clearValidation();control.close();render()}),{signal:abort.signal});
 document.addEventListener('preference-help-open',()=>control.close(),{signal:abort.signal});
 window.addEventListener('pagehide',()=>control.close(),{signal:abort.signal});
 const observer=new MutationObserver(render);observer.observe(select,{attributes:true,attributeFilter:['disabled','selected','label'],childList:true,subtree:true,characterData:true});
 const fieldset=select.closest('fieldset');if(fieldset)observer.observe(fieldset,{attributes:true,attributeFilter:['disabled']});
 return control;
}
export function enhanceSelectControls(scope:ParentNode=document){
 scope.querySelectorAll<HTMLSelectElement>('select:not([multiple]):not([size]):not([data-language-preference] select)').forEach(select=>enhanceSelect(select));
}

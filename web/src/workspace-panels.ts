import {t} from './i18n';

// Two read-only Chat detail panes share one reserved area. They never replace
// the conversation or its composer, and only one pane can be open at a time.
const panels=new Map<HTMLDialogElement,()=>void>();
const compact=matchMedia('(max-width:1279px)');
let readingAnchor:{node:HTMLElement;top:number}|null=null;
// Keep the same message anchor across open/switch/close. Reflow can otherwise
// change which message crosses the header and produce a cumulative jump.
window.addEventListener('wheel',()=>{readingAnchor=null},{passive:true});
window.addEventListener('touchstart',()=>{readingAnchor=null},{passive:true});
document.addEventListener('keydown',event=>{if(['PageUp','PageDown','Home','End','ArrowUp','ArrowDown'].includes(event.key))readingAnchor=null});
export function connectWorkspacePanel(panel:HTMLDialogElement,onOpen:()=>void,onClose:()=>void){
 let opener:HTMLElement|null=null;
 const html=document.documentElement;
 const triggers=Array.from(document.querySelectorAll<HTMLElement>(`[data-panel-open="${panel.id}"]`));
 const expand=panel.querySelector<HTMLButtonElement>('[data-panel-expand]');
 function sync(){
  const active=Array.from(panels.keys()).find(item=>item.open);
  html.classList.toggle('workspace-panel-open',!!active);
  html.classList.toggle('workspace-panel-expanded',!!active?.classList.contains('is-expanded'));
  triggers.forEach(button=>button.setAttribute('aria-expanded',String(panel.open)));
 }
 function layout(change:()=>void){
  const atBottom=innerHeight+scrollY>=document.documentElement.scrollHeight-64;
  const anchor=readingAnchor?.node.isConnected?readingAnchor:{node:Array.from(document.querySelectorAll<HTMLElement>('.message')).find(item=>item.getBoundingClientRect().bottom>120),top:0};
  if(!readingAnchor&&anchor.node)anchor.top=anchor.node.getBoundingClientRect().top;
  if(anchor.node)readingAnchor={node:anchor.node,top:anchor.top};
  change();
  if(atBottom){readingAnchor=null;window.scrollTo({top:document.documentElement.scrollHeight})}
  else if(anchor.node)window.scrollBy(0,anchor.node.getBoundingClientRect().top-anchor.top);
  if(!Array.from(panels.keys()).some(item=>item.open))readingAnchor=null;
 }
 const expandLabel=panel.id==='worker-panel'?'Expand worker view':'Expand computer view';
 const reduceLabel=panel.id==='worker-panel'?'Reduce worker view':'Reduce computer view';
 function close(restore=true){
  if(!panel.open)return;
  onClose();panel.close();panel.classList.remove('is-expanded');
  expand?.setAttribute('aria-pressed','false');expand?.setAttribute('aria-label',t(expandLabel));
  sync();if(restore&&opener?.isConnected)opener.focus({preventScroll:true});
 }
 panels.set(panel,()=>close(false));
 triggers.forEach(button=>button.addEventListener('click',()=>{
  if(panel.open){layout(()=>close());return}
  opener=button;
  layout(()=>{panels.forEach((dismiss,other)=>{if(other!==panel)dismiss()});if(compact.matches)panel.showModal();else panel.show();sync()});
  onOpen();panel.querySelector<HTMLButtonElement>('[data-panel-close]')?.focus({preventScroll:true});
 }));
 panel.querySelector('[data-panel-close]')?.addEventListener('click',()=>layout(()=>close()));
 // Native Escape/close must stop polling as well. Ignore queued close events
 // from the synchronous modal/nonmodal resize conversion.
 panel.addEventListener('cancel',event=>{event.preventDefault();layout(()=>close())});
 panel.addEventListener('close',()=>{if(!panel.open)sync()});
 document.addEventListener('keydown',event=>{if(event.key==='Escape'&&!event.defaultPrevented&&panel.open&&!compact.matches){event.preventDefault();layout(()=>close())}});
 compact.addEventListener('change',()=>{if(panel.open){const focus=document.activeElement as HTMLElement|null;panel.close();if(compact.matches||panel.classList.contains('is-expanded'))panel.showModal();else panel.show();focus?.focus({preventScroll:true})}});
 expand?.addEventListener('click',()=>layout(()=>{const large=panel.classList.toggle('is-expanded');expand.setAttribute('aria-pressed',String(large));expand.setAttribute('aria-label',t(large?reduceLabel:expandLabel));panel.close();if(compact.matches||large)panel.showModal();else panel.show();sync();expand.focus({preventScroll:true})}));
}

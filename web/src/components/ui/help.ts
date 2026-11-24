// Preferences and all server-rendered pages share this help disclosure. Native
// details stay available until enhancement succeeds, including late live regions.
export function enhanceContextHelp(){
 const ready=new WeakSet<HTMLElement>();
 const controls=new WeakMap<HTMLElement,{dismiss:()=>void;position:()=>void}>();
 let active:HTMLElement|null=null;
 const dismiss=()=>{if(active)controls.get(active)?.dismiss();active=null};
 const initialize=(scope:ParentNode)=>{
  const nodes=[...(scope instanceof HTMLElement&&scope.matches('[data-context-help],[data-preference-help]')?[scope]:[]),...scope.querySelectorAll<HTMLElement>('[data-context-help],[data-preference-help]')];
  for(const help of nodes){
   if(ready.has(help))continue;
   const trigger=help.querySelector<HTMLButtonElement>('[data-help-trigger]'),tooltip=help.querySelector<HTMLElement>('[data-help-tooltip]'),fallback=help.querySelector<HTMLDetailsElement>('[data-help-fallback]');
   if(!trigger||!tooltip||!fallback||!('showPopover' in tooltip))continue;
   ready.add(help);let hovered=false,pinned=fallback.open,dismissed=false;
   tooltip.popover='manual';
   const hide=()=>{if(tooltip.matches(':popover-open'))tooltip.hidePopover();tooltip.hidden=true;trigger.setAttribute('aria-expanded','false')};
   const position=()=>{
    if(tooltip.hidden)return;
    const scrollTop=tooltip.scrollTop;
    const zoom=parseFloat(getComputedStyle(document.documentElement).zoom)||1;
    const rect=trigger.getBoundingClientRect(),width=Math.min(280*zoom,innerWidth-32);
    tooltip.style.width=width/zoom+'px';tooltip.style.maxHeight=(innerHeight-32)/zoom+'px';
    tooltip.style.left=Math.max(16,Math.min(rect.left,innerWidth-width-16))/zoom+'px';
    const height=tooltip.getBoundingClientRect().height;
    const below=Math.max(16,rect.bottom-1),belowRoom=innerHeight-16-below,aboveRoom=rect.top+1-16;
    const useBelow=height<=belowRoom||belowRoom>=aboveRoom;
    tooltip.style.maxHeight=Math.max(1,useBelow?belowRoom:aboveRoom)/zoom+'px';
    const actualHeight=tooltip.getBoundingClientRect().height;
    tooltip.style.top=(useBelow?below:Math.max(16,rect.top-actualHeight+1))/zoom+'px';
    tooltip.scrollTop=scrollTop;
   };
   const render=()=>{
    const visible=!dismissed&&(hovered||pinned||document.activeElement===trigger);
    if(visible&&active!==help){dismiss();active=help;document.dispatchEvent(new Event('preference-help-open'))}
    trigger.setAttribute('aria-expanded',String(visible));
    if(visible){tooltip.hidden=false;if(!tooltip.matches(':popover-open'))tooltip.showPopover();position()}else{hide();if(active===help)active=null}
   };
   controls.set(help,{position,dismiss:()=>{hovered=false;pinned=false;dismissed=true;hide()}});
   tooltip.addEventListener('toggle',()=>{if(!tooltip.matches(':popover-open')){tooltip.hidden=true;trigger.setAttribute('aria-expanded','false');if(active===help)active=null}});
   help.addEventListener('pointerenter',event=>{if(event.pointerType==='touch')return;hovered=true;dismissed=false;render()});
   help.addEventListener('pointerleave',()=>{hovered=false;render()});
   tooltip.addEventListener('pointerenter',()=>{hovered=true;render()});
   tooltip.addEventListener('pointerleave',()=>{hovered=false;render()});
   trigger.addEventListener('focus',()=>{dismissed=false;render()});
   trigger.addEventListener('blur',event=>{if(event.relatedTarget instanceof Node&&!help.contains(event.relatedTarget))pinned=false;render()});
   trigger.addEventListener('click',event=>{event.preventDefault();pinned=!pinned;dismissed=!pinned;render()});
   trigger.setAttribute('aria-expanded',String(pinned));
   const hadFocus=fallback.contains(document.activeElement);fallback.hidden=true;trigger.hidden=false;if(hadFocus)trigger.focus();else if(pinned)render();
  }
 };
 document.addEventListener('control-select-open',dismiss);
 document.addEventListener('close',dismiss,true);
 document.addEventListener('pointerdown',event=>{if(active&&event.target instanceof Node&&!active.contains(event.target))dismiss()});
 document.addEventListener('keydown',event=>{if(event.key==='Escape'&&active){const open=!!active.querySelector('[data-help-tooltip]:popover-open');dismiss();if(open){event.preventDefault();event.stopPropagation()}}},true);
 window.addEventListener('resize',()=>{if(active)controls.get(active)?.position()});
 window.addEventListener('scroll',event=>{if(active&&!(event.target instanceof Node&&active.contains(event.target)))controls.get(active)?.position()},true);
 window.addEventListener('pageshow',event=>{if(event.persisted)dismiss()});
 initialize(document);
 new MutationObserver(records=>{
  if(active&&!active.isConnected)dismiss();
  for(const record of records)for(const node of record.addedNodes)if(node instanceof HTMLElement)initialize(node);
 }).observe(document.body,{childList:true,subtree:true});
}

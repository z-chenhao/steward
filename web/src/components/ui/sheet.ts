import {t} from '../../i18n';
import {createResizeHandle} from './resize-handle';

// The shared shell supports a non-modal peek beside a working list, or a modal
// sheet. The feature supplies content and navigation; native links still work.
export function createSideSheet(label:string,onClose:()=>void,options:{modal?:boolean;storageKey?:string;previousLabel?:string;nextLabel?:string}={}){
 const dialog=document.createElement('dialog');dialog.className='control-sheet';dialog.setAttribute('aria-label',label);
 const header=document.createElement('div');header.className='control-sheet-header';
 const title=document.createElement('strong');title.textContent=label;
 const icon=(pathData:string)=>{
  const svg=document.createElementNS('http://www.w3.org/2000/svg','svg');svg.setAttribute('viewBox','0 0 24 24');svg.setAttribute('fill','none');svg.setAttribute('stroke','currentColor');svg.setAttribute('stroke-width','1.65');svg.setAttribute('aria-hidden','true');
  const path=document.createElementNS(svg.namespaceURI,'path');path.setAttribute('d',pathData);svg.append(path);return svg;
 };
 const action=(name:string,path:string)=>{const button=document.createElement('button');button.type='button';button.className='control-icon-button';button.setAttribute('aria-label',t(name));button.title=t(name);button.append(icon(path));return button};
 const full=document.createElement('a');full.className='control-icon-button';full.setAttribute('aria-label',t('Open full page'));full.title=t('Open full page');full.append(icon('M14 3h7v7m0-7L10 14M10 4H4v16h16v-6'));
 const previous=action(options.previousLabel??'Previous trace','m6 14 6-6 6 6'),next=action(options.nextLabel??'Next trace','m6 10 6 6 6-6');previous.hidden=true;next.hidden=true;
 const expand=action('Expand details','M8 3H3v5M16 3h5v5M3 16v5h5M21 16v5h-5');
 const close=action('Close details','m6 6 12 12M6 18 18 6');
 const body=document.createElement('div');body.className='control-sheet-body';
 header.append(title,full,previous,next,expand,close);dialog.append(header,body);document.body.append(dialog);
 let fraction=.5;
 try{const stored=Number(options.storageKey&&localStorage.getItem(options.storageKey));if(stored>=.4&&stored<=.9)fraction=stored}catch{}
 const resize=createResizeHandle(t('Resize details'),{bounds:()=>({min:innerWidth*.4,max:innerWidth*.9,value:dialog.getBoundingClientRect().width||innerWidth*fraction}),at:x=>innerWidth-x,change:(value,commit)=>{fraction=value/innerWidth;dialog.style.setProperty('--sheet-width',fraction*100+'vw');if(commit&&options.storageKey)try{localStorage.setItem(options.storageKey,String(fraction))}catch{}}});
 resize.element.classList.add('control-sheet-resize');dialog.append(resize.element);
 if(fraction!==.5)dialog.style.setProperty('--sheet-width',fraction*100+'vw');
 const fitResize=()=>{resize.element.hidden=innerWidth<=760||dialog.hasAttribute('data-expanded');resize.sync()};
 window.addEventListener('resize',fitResize);fitResize();
 let opener:HTMLElement|null=null;
 const restore=()=>{if(opener?.isConnected)opener.focus({preventScroll:true})};
 const closed=()=>{restore();onClose()};
 const escape=(event:KeyboardEvent)=>{if(event.key==='Escape'&&!event.defaultPrevented&&dialog.open&&dialog.contains(document.activeElement)){event.preventDefault();dialog.close()}};
 close.addEventListener('click',()=>dialog.close());dialog.addEventListener('close',closed);document.addEventListener('keydown',escape);
 expand.addEventListener('click',()=>{const expanded=dialog.toggleAttribute('data-expanded');expand.setAttribute('aria-pressed',String(expanded));fitResize()});
 return {dialog,body,full,title,previous,next,open:()=>{if(!dialog.open){opener=document.activeElement instanceof HTMLElement?document.activeElement:null;if(options.modal===false)dialog.show();else dialog.showModal()}},dispose:()=>{window.removeEventListener('resize',fitResize);resize.dispose();document.removeEventListener('keydown',escape);dialog.removeEventListener('close',closed);dialog.close();dialog.remove();restore()}};
}

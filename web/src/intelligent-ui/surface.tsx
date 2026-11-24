import {t} from '../i18n';
import {useEffect,useLayoutEffect,useRef,useState,type ReactNode} from 'react';
import {ActionMenu,type MenuAction} from '../components/ui/action-menu';
import {Icon} from './icons';
import {download,safeName,serializeSVG,svgPNG} from './exports';
export type DisplayMode='inline'|'fullscreen'|'modal'|'pip';
export function AnswerSurface({title,children,actions=[],onMode,mode:controlled,sandbox=false,graphic=false,onExport}:{title:string;children:ReactNode;actions?:MenuAction[];onMode?:(mode:DisplayMode)=>void;mode?:DisplayMode;sandbox?:boolean;graphic?:boolean;onExport?:(type:string)=>void}){
 const node=useRef<HTMLDialogElement>(null),focus=useRef<HTMLElement|null>(null),wasExpanded=useRef(false);const [localMode,setLocalMode]=useState<DisplayMode>('inline'),[error,setError]=useState('');const mode=controlled??localMode;
 const change=(next:DisplayMode)=>{if(next!=='inline'){const active=document.activeElement as HTMLElement|null;focus.current=active&&node.current?.contains(active)?active:node.current?.querySelector<HTMLElement>('.answer-toolbar button')??null}if(onMode)onMode(next);else setLocalMode(next)};
 useEffect(()=>{const closeOther=(event:Event)=>{if((event as CustomEvent).detail.node!==node.current){if(onMode)onMode('inline');else setLocalMode('inline')}};window.addEventListener('answer-mode',closeOther);return()=>window.removeEventListener('answer-mode',closeOther)},[onMode]);
 useLayoutEffect(()=>{const dialog=node.current;if(!dialog)return;if(mode!=='inline'){wasExpanded.current=true;window.dispatchEvent(new CustomEvent('answer-mode',{detail:{node:dialog}}))}if(mode==='modal'||mode==='fullscreen'){dialog.removeAttribute('open');dialog.showModal()}else{if(dialog.matches(':modal'))dialog.close();if(!dialog.open)dialog.show()}if(mode!=='inline')dialog.querySelector<HTMLButtonElement>('.answer-close')?.focus({preventScroll:true});else if(wasExpanded.current){wasExpanded.current=false;const target=focus.current?.isConnected?focus.current:dialog.querySelector<HTMLElement>('.answer-toolbar button');target?.focus({preventScroll:true})}},[mode]);
 async function exportGraphic(type:string){try{if(onExport){onExport(type);return}const svg=node.current?.querySelector<SVGSVGElement>('svg.recharts-surface');if(!svg)throw Error('No graphic to export.');const source=serializeSVG(svg);if(type==='svg')download(source,'image/svg+xml',safeName(title)+'.svg');else await svgPNG(source,safeName(title)+'.png')}catch(e){setError(e instanceof Error?e.message:'Export failed.')}}
 const hasGraphic=graphic;
 const menu:MenuAction[]=[...actions,...(hasGraphic?[{label:'Download SVG',run:()=>exportGraphic('svg')},{label:'Download PNG',run:()=>exportGraphic('png')}]:[]),{label:'Expand',icon:<Icon name="expand"/>,run:()=>change('fullscreen')},{label:'Open dialog',icon:<Icon name="modal"/>,run:()=>change('modal')},{label:'Keep visible',icon:<Icon name="pip"/>,run:()=>change('pip')}];
 return <dialog ref={node} open={mode!=='modal'&&mode!=='fullscreen'} aria-label={title} role={mode==='modal'||mode==='fullscreen'?'dialog':'group'} className={'answer-surface'+(sandbox?' sandbox-surface':'')+(mode==='fullscreen'?' widget-expanded':'')} data-mode={mode} onCancel={event=>{event.preventDefault();change('inline')}} onKeyDown={event=>{if(event.key==='Escape'&&mode!=='inline'){event.preventDefault();change('inline')}}}>
  <div className="answer-toolbar">{mode!=='inline'&&<button type="button" className="answer-icon-button answer-close" aria-label={t('Close expanded view')} title={t('Close')} onClick={()=>change('inline')}><Icon name="close"/></button>}<ActionMenu actions={menu}/></div>
  {children}{error&&<p className="field-help" role="alert">{t(error)}</p>}
 </dialog>
}

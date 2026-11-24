import {Popover} from '@base-ui/react/popover';
import {useCallback} from 'react';
import {createRoot} from 'react-dom/client';
import {flushSync} from 'react-dom';
import {usePopupZoom} from './popup-zoom';

// A progressive adapter for server-owned form panels. Base UI owns positioning,
// dismissal and focus; the original fields retain native form submission.
function FormDisclosure({summary,panel,container}:{summary:HTMLElement;panel:HTMLElement;container:HTMLElement}){
 const positionerStyle=usePopupZoom();
 const attachLabel=useCallback((element:HTMLSpanElement|null)=>{if(element&&!element.childNodes.length)element.replaceChildren(...Array.from(summary.childNodes,node=>node.cloneNode(true)))},[summary]);
 const attachPanel=useCallback((element:HTMLDivElement|null)=>{if(element&&panel.parentElement!==element)element.append(panel)},[panel]);
 return <Popover.Root>
  <Popover.Trigger className="control-disclosure-trigger"><span ref={attachLabel}/></Popover.Trigger>
  <Popover.Portal keepMounted container={container}>
   <Popover.Positioner className="control-disclosure-positioner" style={positionerStyle} positionMethod="fixed" align="end" sideOffset={8} collisionPadding={12}>
    <Popover.Popup className="control-disclosure-popup" aria-label={summary.textContent?.trim()}><div ref={attachPanel}/></Popover.Popup>
   </Popover.Positioner>
  </Popover.Portal>
 </Popover.Root>;
}
export function enhanceFormDisclosures(scope:ParentNode=document){
 for(const details of scope.querySelectorAll<HTMLDetailsElement>('details[data-control-disclosure]')){
  const summary=details.querySelector<HTMLElement>(':scope > summary'),panel=details.querySelector<HTMLElement>(':scope > .control-disclosure-panel');
  if(!summary||!panel)continue;
  const container=details.closest('form')??document.body;
  const mount=document.createElement('span');mount.className='control-disclosure';
  details.before(mount);
  const root=createRoot(mount);
  flushSync(()=>root.render(<FormDisclosure summary={summary} panel={panel} container={container}/>));
  details.remove();
 }
}

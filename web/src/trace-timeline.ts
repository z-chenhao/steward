import {acceptTraceTimelineProjection,acceptTraceTimelineBrush,type TraceTimelineProjection} from './trace-timeline-projection';
import type {TraceTimelineAction} from './trace-browser';
import {traceProgram,disposeTraceProgram} from './trace-program';
import {enhanceLegacyTraceTimeline} from './legacy/trace-timeline-v1';
import {createChartTooltip} from './components/ui/chart-tooltip';
import type {TimeWindow} from './trace-time';
import {t} from './i18n';
export function enhanceTraceTimeline(root:HTMLElement,remember:(window:TimeWindow|null)=>void,saved?:TimeWindow){
 const revision=root.dataset.traceProgramRevision;if(!revision)return enhanceLegacyTraceTimeline(root,remember,saved);
 const pane=root.querySelector<HTMLElement>('.obs-tree-pane')!,axis=pane.querySelector<HTMLElement>('.obs-timeline-axis')!,rows=Array.from(pane.querySelectorAll<HTMLElement>('.observation-node'));
 const domain={start:Number(root.dataset.timeStart),end:Number(root.dataset.timeEnd)};
 if(!Number.isFinite(domain.start)||domain.end<=domain.start)return ()=>{};
 let current=saved??domain,ready=false,active=true;
 const entry=traceProgram(root),program=entry.browser;
 let transitions=Promise.resolve();
 void program.catch(()=>{});
 const tooltip=createChartTooltip(),lifetime=new AbortController(),signal=lifetime.signal,ns='http://www.w3.org/2000/svg';
 type TimelineDrag={id:number;start:number;last:number;moved:boolean};
 let drag:TimelineDrag|null=null,suppressClick=false;
 const line=(svg:SVGSVGElement,at:number,kind:string)=>{const element=document.createElementNS(ns,'line');element.setAttribute('x1',String(at));element.setAttribute('x2',String(at));element.setAttribute('y1','2');element.setAttribute('y2','22');element.setAttribute('class',kind);svg.append(element)};
 const actions=Array.from(pane.querySelectorAll<HTMLButtonElement>('[data-timeline-action]'));
 let lastProjection:TraceTimelineProjection|undefined;
 const render=(projection=lastProjection)=>{
  if(!projection)return;lastProjection=projection;
  axis.replaceChildren();for(const label of projection.ticks){const tick=document.createElement('span');tick.textContent=label;axis.append(tick)}
  axis.setAttribute('aria-label',projection.range_label);
  root.dataset.timeWindowStart=String(current.start);root.dataset.timeWindowEnd=String(current.end);
  rows.forEach((row,index)=>{
   const lane=projection.rows[index],svg=row.querySelector<SVGSVGElement>('.obs-node-time svg')!;
   svg.replaceChildren();for(const tick of projection.grid)line(svg,tick,'obs-timeline-grid');
   if(lane.bar){const bar=document.createElementNS(ns,'rect');bar.setAttribute('x',String(lane.bar.x));bar.setAttribute('y','7');bar.setAttribute('width',String(lane.bar.width));bar.setAttribute('height','10');bar.setAttribute('rx','2');bar.setAttribute('class','obs-bar');svg.append(bar)}
   if(lane.point!==null)line(svg,lane.point,'obs-point');
   if(lane.first_output!==null)line(svg,lane.first_output,'obs-first-output');
  });
  for(const button of actions)button.disabled=projection.buttons[button.dataset.timelineAction as keyof typeof projection.buttons]??true;
  remember(projection.full?null:{...current});
 };
 const transition=(action:TraceTimelineAction)=>{
  transitions=transitions.then(async()=>{
   if(!active||!entry.active)return;const browser=await program;if(!active||!entry.active)return;
   const result=await browser.call({type:'timeline',input:{domain,current,action,width:axis.clientWidth,has_selected:rows.some(row=>row.querySelector('[aria-current]')),language:document.documentElement.lang==='zh-CN'?'zh-CN':'en',rows:rows.map(row=>({start:Number(row.dataset.start),end:row.dataset.end?Number(row.dataset.end):null,running:row.hasAttribute('data-running'),first_output:row.dataset.firstOutput===undefined?null:Number(row.dataset.firstOutput)}))}});
   if(!active||!entry.active||!root.isConnected)return;
   if(!result||typeof result!=='object'||Array.isArray(result))throw Error('invalid timeline window');
   const envelope=result as Record<string,unknown>;
   if(!envelope.window||typeof envelope.window!=='object'||Array.isArray(envelope.window))throw Error('invalid timeline window');
   const value=envelope.window as Record<string,unknown>;
   if(typeof value.start!=='number'||!Number.isFinite(value.start)||typeof value.end!=='number'||!Number.isFinite(value.end)||value.start<domain.start||value.end>domain.end||value.end<=value.start)throw Error('out of bounds timeline');
   const next={start:value.start,end:value.end};
   const projection=acceptTraceTimelineProjection(envelope.projection,rows.length,next.start===domain.start&&next.end===domain.end);
   current=next;ready=true;tooltip.hide();render(projection);
  }).catch(()=>{dispose();for(const button of actions)button.disabled=true});
 };
 const bounds=()=>axis.getBoundingClientRect();
 const fraction=(clientX:number)=>(clientX-bounds().left)/bounds().width;
 type BrushItem={gesture:TimelineDrag;version:number;window:TimeWindow;first:number;last:number;x:number;y:number};
 let brushVersion=0,brushBusy=false,brushQueued:BrushItem|null=null;
 const previewBrush=(gesture:TimelineDrag,x:number,y:number)=>{
  brushQueued={gesture,version:++brushVersion,window:{...current},first:fraction(gesture.start),last:fraction(gesture.last),x,y};
  if(brushBusy)return;brushBusy=true;
  void (async()=>{
   while(active&&entry.active&&brushQueued){
    const item=brushQueued;brushQueued=null;
    const browser=await program;if(!active||!entry.active)return;
    const result=await browser.call({type:'timeline_brush',input:{domain,current:item.window,first:item.first,last:item.last}});
    const projection=acceptTraceTimelineBrush(result);
    if(!active||!entry.active||!root.isConnected||drag!==item.gesture||brushVersion!==item.version||current.start!==item.window.start||current.end!==item.window.end)continue;
    tooltip.show(projection.label,item.x,item.y);
    for(const row of rows){const svg=row.querySelector<SVGSVGElement>('.obs-node-time svg')!;svg.querySelector('.obs-timeline-brush')?.remove();const rect=document.createElementNS(ns,'rect');rect.setAttribute('class','obs-timeline-brush');rect.setAttribute('x',String(projection.x));rect.setAttribute('width',String(projection.width));rect.setAttribute('height','24');svg.append(rect)}
   }
  })().catch(()=>{dispose();for(const button of actions)button.disabled=true}).finally(()=>{brushBusy=false});
 };
 const describe=(row:HTMLElement)=>{
  const name=row.querySelector('.obs-node-name')?.textContent??'',status=row.querySelector('.obs-node-status')?.getAttribute('data-status')??'';
  return [name,t(status),...(lastProjection?.rows[rows.indexOf(row)]?.tooltip??[])].join('\n');
 };
 for(const row of rows){
  const link=row.querySelector<HTMLAnchorElement>('a.tree-heading')!;
  link.addEventListener('pointermove',event=>{if(ready&&!drag&&event.pointerType!=='touch')tooltip.show(describe(row),event.clientX,event.clientY,link)},{signal});
  link.addEventListener('pointerleave',()=>{if(!drag)tooltip.hide()},{signal});
  link.addEventListener('focus',()=>{if(!ready)return;const rect=link.getBoundingClientRect();tooltip.show(describe(row),rect.left,rect.bottom,link)},{signal});link.addEventListener('blur',tooltip.hide,{signal});
 }
 for(const button of actions){button.disabled=true;button.addEventListener('click',()=>{
  if(!ready)return;const action=button.dataset.timelineAction;
  if(action!=='fit'&&action!=='in'&&action!=='out'&&action!=='selected')return;
  const selected=rows.find(row=>row.querySelector('[aria-current]'));
  transition({kind:'button',action,selected:selected?{start:Number(selected.dataset.start),end:selected.dataset.end?Number(selected.dataset.end):null}:null});
 },{signal})}
 axis.tabIndex=0;
 axis.addEventListener('keydown',event=>{if(!ready)return;const key=event.key;if(key==='ArrowLeft'||key==='ArrowRight'||key==='Home'||key==='+'||key==='-'){event.preventDefault();transition({kind:'key',key})}},{signal});
 pane.addEventListener('wheel',event=>{if(!ready||!(event.ctrlKey||event.metaKey||event.shiftKey)||event.clientX<bounds().left)return;event.preventDefault();transition({kind:'wheel',pan:event.shiftKey,delta_y:event.deltaY,delta_x:event.deltaX,anchor:(event.clientX-bounds().left)/bounds().width})},{passive:false,signal});
 pane.addEventListener('pointerdown',event=>{if(!ready||event.button!==0||event.ctrlKey||event.metaKey||!((event.target as Element).closest('.obs-node-time,.obs-timeline-axis')))return;drag={id:event.pointerId,start:event.clientX,last:event.clientX,moved:false}},{signal});
 pane.addEventListener('pointermove',event=>{
  if(drag?.id!==event.pointerId)return;drag.last=event.clientX;
  if(Math.abs(drag.last-drag.start)>(lastProjection?.drag_threshold??Infinity)){drag.moved=true;if(!pane.hasPointerCapture(event.pointerId))pane.setPointerCapture(event.pointerId);previewBrush(drag,event.clientX,event.clientY)}
 },{signal});
 const cancel=()=>{brushVersion++;brushQueued=null;if(drag&&pane.hasPointerCapture(drag.id))pane.releasePointerCapture(drag.id);drag=null;tooltip.hide();render()};
 pane.addEventListener('pointerup',event=>{if(drag?.id!==event.pointerId)return;const saved=drag;drag=null;brushVersion++;brushQueued=null;if(pane.hasPointerCapture(event.pointerId))pane.releasePointerCapture(event.pointerId);if(saved.moved){suppressClick=true;transition({kind:'brush',first:fraction(saved.start),last:fraction(saved.last)})}},{signal});
 pane.addEventListener('click',event=>{if(suppressClick){suppressClick=false;if(!(event.target as Element).closest('.obs-timeline-actions')){event.preventDefault();event.stopPropagation()}}},{capture:true,signal});
 pane.addEventListener('pointercancel',cancel,{signal});pane.addEventListener('lostpointercapture',()=>{if(drag)cancel()},{signal});
 document.addEventListener('keydown',event=>{if(event.key==='Escape'&&drag){event.preventDefault();event.stopPropagation();cancel()}},{capture:true,signal});
 let lastWidth=0;const resize=new ResizeObserver(()=>{if(Math.abs(axis.clientWidth-lastWidth)>1){lastWidth=axis.clientWidth;transition({kind:'restore'})}});resize.observe(axis);
 const dispose=()=>{if(!active)return;active=false;ready=false;brushVersion++;brushQueued=null;resize.disconnect();lifetime.abort();if(drag&&pane.hasPointerCapture(drag.id))pane.releasePointerCapture(drag.id);drag=null;tooltip.dispose();disposeTraceProgram(root)};
 transition({kind:'restore'});return dispose;
}

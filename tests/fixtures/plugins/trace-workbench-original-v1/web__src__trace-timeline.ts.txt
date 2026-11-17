import {createChartTooltip} from './components/ui/chart-tooltip';
import {boundedWindow,elapsedLabel,zoomWindow,type TimeWindow} from './trace-time';
import {t} from './i18n';
export function enhanceTraceTimeline(root:HTMLElement,remember:(window:TimeWindow|null)=>void,saved?:TimeWindow){
 const pane=root.querySelector<HTMLElement>('.obs-tree-pane')!,axis=pane.querySelector<HTMLElement>('.obs-timeline-axis')!,rows=Array.from(pane.querySelectorAll<HTMLElement>('.observation-node'));
 const domain={start:Number(root.dataset.timeStart),end:Number(root.dataset.timeEnd)};
 if(!Number.isFinite(domain.start)||domain.end<=domain.start)return ()=>{};
 let current=saved?boundedWindow(domain,saved.start,saved.end):domain;
 const tooltip=createChartTooltip(),lifetime=new AbortController(),signal=lifetime.signal,ns='http://www.w3.org/2000/svg';
 let drag:{id:number;start:number;last:number;moved:boolean}|null=null,suppressClick=false;
 const x=(time:number)=>(time-current.start)/(current.end-current.start)*1000;
 const line=(svg:SVGSVGElement,at:number,kind:string)=>{const element=document.createElementNS(ns,'line');element.setAttribute('x1',String(at));element.setAttribute('x2',String(at));element.setAttribute('y1','2');element.setAttribute('y2','22');element.setAttribute('class',kind);svg.append(element)};
 const actions=Array.from(pane.querySelectorAll<HTMLButtonElement>('[data-timeline-action]'));
 const render=()=>{
  const steps=axis.clientWidth>=320?4:axis.clientWidth>=180?2:1;
  axis.replaceChildren();for(let i=0;i<=steps;i++){const tick=document.createElement('span');tick.textContent=elapsedLabel(current.start-domain.start+(current.end-current.start)*i/steps);axis.append(tick)}
  axis.setAttribute('aria-label',t('Time since trace start')+': '+elapsedLabel(current.start-domain.start)+' – '+elapsedLabel(current.end-domain.start));
  root.dataset.timeWindowStart=String(current.start);root.dataset.timeWindowEnd=String(current.end);
  for(const row of rows){
   const svg=row.querySelector<SVGSVGElement>('.obs-node-time svg')!;svg.replaceChildren();for(let i=0;i<=4;i++)line(svg,i*250,'obs-timeline-grid');
   const start=Number(row.dataset.start),end=row.dataset.end?Number(row.dataset.end):row.hasAttribute('data-running')?domain.end:null;
   if(end!==null&&end>start){
    if(end<current.start||start>current.end)continue;
    const bar=document.createElementNS(ns,'rect');bar.setAttribute('x',String(Math.max(0,x(start))));bar.setAttribute('y','7');bar.setAttribute('width',String(Math.max(1,Math.min(1000,x(end))-Math.max(0,x(start)))));bar.setAttribute('height','10');bar.setAttribute('rx','2');bar.setAttribute('class','obs-bar');svg.append(bar);
   }else if(start>=current.start&&start<=current.end)line(svg,x(start),'obs-point');
   const first=Number(row.dataset.firstOutput);if(first>=current.start&&first<=current.end)line(svg,x(first),'obs-first-output');
  }
  const full=current.start===domain.start&&current.end===domain.end;
  for(const button of actions)button.disabled=button.dataset.timelineAction==='selected'?!rows.some(row=>row.querySelector('[aria-current]')):button.dataset.timelineAction==='fit'||button.dataset.timelineAction==='out'?full:false;
  remember(full?null:{...current});
 };
 const apply=(window:TimeWindow)=>{current=boundedWindow(domain,window.start,window.end);tooltip.hide();render()};
 const bounds=()=>axis.getBoundingClientRect();
 const at=(clientX:number)=>current.start+Math.max(0,Math.min(1,(clientX-bounds().left)/bounds().width))*(current.end-current.start);
 const describe=(row:HTMLElement)=>{
  const name=row.querySelector('.obs-node-name')?.textContent??'',status=row.querySelector('.obs-node-status')?.getAttribute('data-status')??'',start=Number(row.dataset.start),end=row.dataset.end?Number(row.dataset.end):null;
  const lines=[name,t(status),t('Start')+' +'+elapsedLabel(start-domain.start)];
  lines.push(end!==null?t('Duration')+' '+elapsedLabel(end-start):row.hasAttribute('data-running')?t('Ongoing; end time not recorded'):t('End time not recorded'));
  if(row.dataset.firstOutput)lines.push(t('First model output')+' +'+elapsedLabel(Number(row.dataset.firstOutput)-domain.start));
  return lines.join('\n');
 };
 for(const row of rows){
  const link=row.querySelector<HTMLAnchorElement>('a.tree-heading')!;
  link.addEventListener('pointermove',event=>{if(!drag&&event.pointerType!=='touch')tooltip.show(describe(row),event.clientX,event.clientY,link)},{signal});
  link.addEventListener('pointerleave',()=>{if(!drag)tooltip.hide()},{signal});
  link.addEventListener('focus',()=>{const rect=link.getBoundingClientRect();tooltip.show(describe(row),rect.left,rect.bottom,link)},{signal});link.addEventListener('blur',tooltip.hide,{signal});
 }
 for(const button of actions)button.addEventListener('click',()=>{
  const action=button.dataset.timelineAction;
  if(action==='fit')apply(domain);else if(action==='selected'){
   const selected=rows.find(row=>row.querySelector('[aria-current]')),start=Number(selected?.dataset.start),end=selected?.dataset.end?Number(selected.dataset.end):start;
   if(!selected)return;const padding=Math.max((end-start)*.15,(domain.end-domain.start)/100);apply({start:start-padding,end:end+padding});
  }else apply(zoomWindow(domain,current,action==='in'?.5:2));
 },{signal});
 axis.tabIndex=0;
 axis.addEventListener('keydown',event=>{const span=current.end-current.start;if(event.key==='ArrowLeft'||event.key==='ArrowRight'){event.preventDefault();const offset=span*.2*(event.key==='ArrowLeft'?-1:1);apply({start:current.start+offset,end:current.end+offset})}else if(event.key==='Home'){event.preventDefault();apply(domain)}else if(event.key==='+'||event.key==='-'){event.preventDefault();apply(zoomWindow(domain,current,event.key==='+'?.5:2))}},{signal});
 pane.addEventListener('wheel',event=>{if(!(event.ctrlKey||event.metaKey||event.shiftKey)||event.clientX<bounds().left)return;event.preventDefault();if(event.shiftKey){const offset=(current.end-current.start)*Math.sign(event.deltaY||event.deltaX)*.1;apply({start:current.start+offset,end:current.end+offset})}else apply(zoomWindow(domain,current,event.deltaY<0?.8:1.25,Math.max(0,Math.min(1,(event.clientX-bounds().left)/bounds().width))))},{passive:false,signal});
 pane.addEventListener('pointerdown',event=>{if(event.button!==0||event.ctrlKey||event.metaKey||!((event.target as Element).closest('.obs-node-time,.obs-timeline-axis')))return;drag={id:event.pointerId,start:event.clientX,last:event.clientX,moved:false}},{signal});
 pane.addEventListener('pointermove',event=>{
  if(drag?.id!==event.pointerId)return;drag.last=event.clientX;if(Math.abs(drag.last-drag.start)>5){drag.moved=true;if(!pane.hasPointerCapture(event.pointerId))pane.setPointerCapture(event.pointerId);tooltip.show(elapsedLabel(at(Math.min(drag.start,drag.last))-domain.start)+' – '+elapsedLabel(at(Math.max(drag.start,drag.last))-domain.start),event.clientX,event.clientY);
   for(const row of rows){const svg=row.querySelector<SVGSVGElement>('.obs-node-time svg')!;svg.querySelector('.obs-timeline-brush')?.remove();const rect=document.createElementNS(ns,'rect');rect.setAttribute('class','obs-timeline-brush');rect.setAttribute('x',String(x(at(Math.min(drag.start,drag.last)))));rect.setAttribute('width',String(Math.abs(x(at(drag.last))-x(at(drag.start)))));rect.setAttribute('height','24');svg.append(rect)}
  }
 },{signal});
 const cancel=()=>{if(drag&&pane.hasPointerCapture(drag.id))pane.releasePointerCapture(drag.id);drag=null;tooltip.hide();render()};
 pane.addEventListener('pointerup',event=>{if(drag?.id!==event.pointerId)return;const saved=drag;drag=null;if(pane.hasPointerCapture(event.pointerId))pane.releasePointerCapture(event.pointerId);if(saved.moved){suppressClick=true;apply({start:at(Math.min(saved.start,saved.last)),end:at(Math.max(saved.start,saved.last))})}},{signal});
 pane.addEventListener('click',event=>{if(suppressClick){event.preventDefault();event.stopPropagation();suppressClick=false}},{capture:true,signal});
 pane.addEventListener('pointercancel',cancel,{signal});pane.addEventListener('lostpointercapture',()=>{if(drag)cancel()},{signal});
 document.addEventListener('keydown',event=>{if(event.key==='Escape'&&drag){event.preventDefault();event.stopPropagation();cancel()}},{capture:true,signal});
 let lastWidth=0;const resize=new ResizeObserver(()=>{if(Math.abs(axis.clientWidth-lastWidth)>1){lastWidth=axis.clientWidth;render()}});resize.observe(axis);
 render();return ()=>{resize.disconnect();lifetime.abort();tooltip.dispose()};
}

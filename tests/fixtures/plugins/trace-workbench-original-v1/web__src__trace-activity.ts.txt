import {createChartTooltip} from './components/ui/chart-tooltip';
import {formatLocalTime} from './local-time';
import {activityGeometry,activityIndex,bucketSelection} from './trace-time';
import {t} from './i18n';
type Bucket={at:string;until:string;count:number};
export function enhanceTraceActivity(scope:ParentNode){
 const disposers:(()=>void)[]=[];
 for(const root of scope.querySelectorAll<HTMLElement>('[data-trace-activity]')){
  const buckets=JSON.parse(root.dataset.traceActivity??'null') as Bucket[]|null;
  const plot=root.querySelector<HTMLElement>('.obs-activity-chart');if(!buckets?.length||!plot)continue;
  const geometry=activityGeometry(buckets),bars=Array.from(plot.querySelectorAll<SVGElement>('[data-bucket]')),brush=plot.querySelector<HTMLElement>('.obs-activity-brush')!,explore=root.querySelector<HTMLAnchorElement>('[data-activity-explore]')!;
  const selectionText=root.querySelector<HTMLOutputElement>('[data-activity-selection]')!,hint=root.querySelector<HTMLElement>('[data-activity-hint]')!;
  const tooltip=createChartTooltip(),lifetime=new AbortController(),signal=lifetime.signal;
  let drag:{pointer:number;first:number;last:number;touch:boolean}|null=null,pinned=false,selected:{first:number;last:number}|null=null,keyboardStart:number|null=null;
  const local=(raw:string)=>formatLocalTime(raw,'datetime',document.documentElement.lang)?.full??raw;
  const summary=(first:number,last=first)=>`${local(buckets[first].at)} – ${local(buckets[last].until)}\n${buckets.slice(first,last+1).reduce((sum,b)=>sum+b.count,0)} ${t('traces')}`;
  const url=(first:number,last:number)=>{const value=new URL(location.href);value.searchParams.delete('cursor');value.searchParams.set('since',buckets[first].at);value.searchParams.set('until',buckets[last].until);return value};
  const preview=(first:number,last:number,x:number,y:number,touch=false)=>{
   selected=bucketSelection(buckets.length,first,last);const selection=selected;
   brush.hidden=false;brush.style.left=geometry[selection.first].left*100+'%';brush.style.width=(geometry[selection.last].right-geometry[selection.first].left)*100+'%';
   const count=buckets.slice(selection.first,selection.last+1).reduce((sum,b)=>sum+b.count,0);explore.href=url(selection.first,selection.last).href;explore.hidden=!touch||count===0;
   const detail=summary(selection.first,selection.last);selectionText.textContent=detail;selectionText.hidden=!touch;hint.hidden=touch;tooltip.show(detail,x,y);pinned=touch;
   return count;
  };
  const indexAt=(x:number)=>activityIndex(geometry,Math.max(0,Math.min(1,(x-plot.getBoundingClientRect().left)/plot.getBoundingClientRect().width)));
  const clear=()=>{drag=null;pinned=false;selected=null;keyboardStart=null;brush.hidden=true;explore.hidden=true;selectionText.hidden=true;hint.hidden=false;tooltip.hide()};
  const go=(first:number,last:number)=>{if(buckets.slice(first,last+1).some(b=>b.count>0))location.assign(url(first,last))};
  const max=Math.max(...buckets.map(b=>b.count));root.querySelector('[data-activity-max]')!.textContent=String(max);
  const axis=root.querySelector('.obs-activity-axis')!;
  const drawAxis=()=>{
   axis.replaceChildren();const positions=plot.clientWidth<500?[0,1]:[0,.25,.5,.75,1];
   const first=new Date(buckets[0].at).valueOf(),end=new Date(buckets.at(-1)!.until).valueOf();
   for(const position of positions){const node=document.createElement('span');node.textContent=formatLocalTime(new Date(first+(end-first)*position).toISOString(),end-first<86400000?'clock':'date',document.documentElement.lang)?.text??'';axis.append(node)}
  };
  const resize=new ResizeObserver(drawAxis);resize.observe(plot);drawAxis();clear();
  const firstActive=Math.max(0,buckets.findIndex(b=>b.count>0));
  bars.forEach((bar,index)=>{
   bar.removeAttribute('title');bar.querySelector('title')?.remove();bar.tabIndex=index===firstActive?0:-1;bar.setAttribute('aria-label',summary(index));
   bar.addEventListener('focus',()=>{const rect=bar.getBoundingClientRect();tooltip.show(summary(index),rect.left,rect.top,bar)},{signal});
   bar.addEventListener('blur',()=>{if(!pinned&&!drag)tooltip.hide()},{signal});
   bar.addEventListener('keydown',event=>{
    if(event.key==='Escape'){clear();event.preventDefault();event.stopPropagation();return}
    let next=index;if(event.key==='ArrowLeft')next=Math.max(0,index-1);else if(event.key==='ArrowRight')next=Math.min(bars.length-1,index+1);else if(event.key==='Home')next=0;else if(event.key==='End')next=bars.length-1;else if(event.key==='Enter'){
     event.preventDefault();const range=selected??{first:index,last:index};go(range.first,range.last);return;
    }else return;
    event.preventDefault();if(event.shiftKey&&keyboardStart===null)keyboardStart=index;if(!event.shiftKey)clear();
    bars[index].tabIndex=-1;bars[next].tabIndex=0;bars[next].focus();
    if(keyboardStart!==null){const rect=bars[next].getBoundingClientRect();preview(keyboardStart,next,rect.left,rect.top,true)}
   },{signal});
  });
  plot.addEventListener('pointerdown',event=>{if(event.button!==0||event.metaKey||event.ctrlKey||event.shiftKey||event.altKey)return;const index=indexAt(event.clientX);drag={pointer:event.pointerId,first:index,last:index,touch:event.pointerType==='touch'};plot.setPointerCapture(event.pointerId);if(!drag.touch)event.preventDefault();preview(index,index,event.clientX,event.clientY,drag.touch)},{signal});
  plot.addEventListener('pointermove',event=>{const index=indexAt(event.clientX);if(drag?.pointer===event.pointerId){drag.last=index;preview(drag.first,index,event.clientX,event.clientY,drag.touch)}else if(!pinned&&event.pointerType!=='touch')tooltip.show(summary(index),event.clientX,event.clientY,bars[index])},{signal});
  plot.addEventListener('pointerleave',()=>{if(!drag&&!pinned)tooltip.hide()},{signal});
  plot.addEventListener('pointerup',event=>{if(drag?.pointer!==event.pointerId)return;const saved=drag;drag=null;if(plot.hasPointerCapture(event.pointerId))plot.releasePointerCapture(event.pointerId);const range=bucketSelection(buckets.length,saved.first,saved.last);if(saved.touch)preview(range.first,range.last,event.clientX,event.clientY,true);else go(range.first,range.last)},{signal});
  plot.addEventListener('pointercancel',clear,{signal});plot.addEventListener('lostpointercapture',()=>{if(drag)clear()},{signal});
  document.addEventListener('keydown',event=>{if(event.key==='Escape'&&(drag||pinned)){clear();event.preventDefault();event.stopPropagation()}},{capture:true,signal});
  document.addEventListener('pointerdown',event=>{if(pinned&&event.target instanceof Node&&!root.contains(event.target))clear()},{signal});
  plot.addEventListener('click',event=>{if(!event.ctrlKey&&!event.metaKey&&!event.shiftKey&&!event.altKey)event.preventDefault()},{signal});
  disposers.push(()=>{resize.disconnect();lifetime.abort();tooltip.dispose()});
 }
 return ()=>disposers.forEach(dispose=>dispose());
}

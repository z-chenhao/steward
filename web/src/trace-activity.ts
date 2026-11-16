import {createChartTooltip} from './components/ui/chart-tooltip';
import {formatLocalTime} from './local-time';
import {enhanceTraceActivityView} from './trace-activity-view';
import {traceProgram} from './trace-program';
import {enhanceLegacyTraceActivity} from './legacy/trace-activity-v1';
import {acceptTraceActivityProjection,type TraceActivityBucket,type TraceActivityState,type TraceActivityEvent,type TraceActivityProjection} from './trace-activity-projection';
type Point={x:number;y:number};
export function enhanceTraceActivity(scope:ParentNode){
 const disposers:(()=>void)[]=[];
 for(const root of scope.querySelectorAll<HTMLElement>('[data-trace-activity]')){
  const page=root.closest<HTMLElement>('[data-trace-program-revision]');
  if(!page?.dataset.traceProgramRevision){disposers.push(enhanceLegacyTraceActivity(root));continue}
  const buckets=JSON.parse(root.dataset.traceActivity??'null') as TraceActivityBucket[]|null;
  const plot=root.querySelector<HTMLElement>('.obs-activity-chart');if(!buckets?.length||!plot)continue;
  const bars=Array.from(plot.querySelectorAll<SVGElement>('[data-bucket]')),brush=root.querySelector<HTMLElement>('.obs-activity-brush')!,explore=root.querySelector<HTMLAnchorElement>('[data-activity-explore]')!,text=root.querySelector<HTMLOutputElement>('[data-activity-selection]')!,hint=root.querySelector<HTMLElement>('[data-activity-hint]')!;
  const entry=traceProgram(page),tooltip=createChartTooltip(),lifetime=new AbortController(),signal=lifetime.signal,disposeView=enhanceTraceActivityView(root,plot,buckets);
  const base=new URL(location.href);base.searchParams.set('reader_revision',page.dataset.traceProgramRevision);
  let active=true,epoch=0,state:TraceActivityState|null=null,pointer:number|null=null,pendingGesture=false,applyingFocus=false,running=false;
  type Job={event:TraceActivityEvent;point?:Point;epoch:number};
  const queue:Job[]=[];
  const current=(job:Job)=>active&&entry.active&&root.isConnected&&job.epoch===epoch;
  const release=()=>{const id=pointer;pointer=null;if(id!==null&&plot.hasPointerCapture(id))plot.releasePointerCapture(id)};
  const clearDOM=()=>{brush.hidden=true;explore.hidden=true;text.hidden=true;hint.hidden=false;tooltip.hide()};
  const stop=()=>{if(!active)return;active=false;epoch++;queue.length=0;release();lifetime.abort();disposeView();tooltip.dispose()};
  const fail=()=>{stop();entry.active=false;void entry.browser.then(browser=>browser.close()).catch(()=>{})};
  const url=(first:number,last:number)=>{const value=new URL(base);value.searchParams.delete('cursor');value.searchParams.set('since',buckets[first].at);value.searchParams.set('until',buckets[last].until);return value};
  const local=(raw:string)=>formatLocalTime(raw,'datetime',document.documentElement.lang)?.full??raw;
  const detail=(value:{first:number;last:number;count:number;label:string})=>`${local(buckets[value.first].at)} – ${local(buckets[value.last].until)}\n${value.count} ${value.label}`;
  const apply=(value:TraceActivityProjection,job:Job)=>{
   state=value.state;if(job.event.kind==='up')pendingGesture=false;
   if(value.labels)for(const [index,label] of value.labels.entries()){bars[index].removeAttribute('title');bars[index].querySelector('title')?.remove();bars[index].tabIndex=index===value.focus?0:-1;bars[index].setAttribute('aria-label',detail(label))}
   const selection=value.selection;
   if(selection){brush.hidden=false;brush.style.left=selection.left*100+'%';brush.style.width=selection.width*100+'%';explore.href=url(selection.first,selection.last).href;explore.hidden=!selection.explore;text.textContent=detail(selection);text.hidden=!selection.persistent;hint.hidden=selection.persistent}
   else clearDOM();
   if(value.focus!==null&&job.event.kind!=='init'){
    bars.forEach((bar,index)=>{bar.tabIndex=index===value.focus?0:-1});applyingFocus=true;try{bars[value.focus].focus()}finally{applyingFocus=false}
   }
   if(value.tooltip===null)tooltip.hide();
   else if(value.tooltip!==false){const tip=value.tooltip,anchor=tip.anchor===null?undefined:bars[tip.anchor],rect=anchor?.getBoundingClientRect(),point=job.point??{x:rect?.left??0,y:rect?.top??0};tooltip.show(detail(tip),point.x,point.y,anchor)}
   // The plugin can only select original authorized buckets. It never returns a URL.
   if(value.navigate&&current(job))location.assign(url(value.navigate.first,value.navigate.last));
  };
  const drain=async()=>{
   if(running)return;running=true;
   try{
    const browser=await entry.browser;
    while(queue.length){const job=queue.shift()!;if(!current(job))continue;
     const result=await browser.call({type:'activity',input:{buckets,state,event:job.event,language:document.documentElement.lang==='zh-CN'?'zh-CN':'en'}});
     if(current(job))apply(acceptTraceActivityProjection(result,buckets,job.event),job);
    }
   }catch{if(active)fail()}finally{running=false}
  };
  const submit=(event:TraceActivityEvent,point?:Point)=>{
   if(!active||!entry.active)return;
   const job={event,point,epoch},last=queue.at(-1);
   if(event.kind==='move'&&last?.event.kind==='move'&&last.epoch===epoch)queue[queue.length-1]=job;else queue.push(job);
   if(queue.length>64){fail();return}void drain();
  };
  const cancel=()=>{epoch++;queue.length=0;state=null;pendingGesture=false;release();clearDOM();submit({kind:'clear'})};
  const fraction=(event:PointerEvent)=>{const rect=plot.getBoundingClientRect();return (event.clientX-rect.left)/Math.max(1,rect.width)};
  const point=(event:PointerEvent):Point=>({x:event.clientX,y:event.clientY});
  clearDOM();submit({kind:'init'});
  bars.forEach((bar,index)=>{
   bar.addEventListener('focus',()=>{if(!applyingFocus)submit({kind:'focus',index})},{signal});
   bar.addEventListener('blur',()=>{if(!applyingFocus)submit({kind:'blur'})},{signal});
   bar.addEventListener('keydown',event=>{
    if(!['ArrowLeft','ArrowRight','Home','End','Enter','Escape'].includes(event.key))return;
    event.preventDefault();event.stopPropagation();
    if(event.key==='Escape'){cancel();return}
    submit({kind:'key',index,key:event.key as 'ArrowLeft'|'ArrowRight'|'Home'|'End'|'Enter',shift:event.shiftKey});
   },{signal});
  });
  plot.addEventListener('pointerdown',event=>{if(event.button!==0||event.metaKey||event.ctrlKey||event.shiftKey||event.altKey)return;pendingGesture=true;pointer=event.pointerId;plot.setPointerCapture(pointer);if(event.pointerType!=='touch')event.preventDefault();submit({kind:'down',fraction:fraction(event),touch:event.pointerType==='touch'},point(event))},{signal});
  plot.addEventListener('pointermove',event=>submit({kind:'move',fraction:fraction(event),touch:event.pointerType==='touch',drag:pointer===event.pointerId},point(event)),{signal});
  plot.addEventListener('pointerleave',()=>submit({kind:'leave'}),{signal});
  plot.addEventListener('pointerup',event=>{if(pointer!==event.pointerId)return;release();submit({kind:'up'},point(event))},{signal});
  plot.addEventListener('pointercancel',cancel,{signal});plot.addEventListener('lostpointercapture',()=>{if(pointer!==null)cancel()},{signal});
  plot.addEventListener('click',event=>{if(!event.ctrlKey&&!event.metaKey&&!event.shiftKey&&!event.altKey)event.preventDefault()},{signal});
  document.addEventListener('keydown',event=>{if(event.key==='Escape'&&(pointer!==null||pendingGesture||state?.pinned)){cancel();event.preventDefault();event.stopPropagation()}},{capture:true,signal});
  document.addEventListener('pointerdown',event=>{if(event.target instanceof Node&&!root.contains(event.target))submit({kind:'outside'})},{signal});
  window.addEventListener('pagehide',stop,{signal});
  disposers.push(stop);
 }
 return ()=>disposers.forEach(dispose=>dispose());
}

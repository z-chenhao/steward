import {formatLocalTime} from './local-time';
import {traceProgram} from './trace-program';
import {legacyTraceActivityView} from './legacy/trace-activity-view-v1';
type Bucket={at:string;until:string;count:number};
type View={maximum:number;axis:{mode:'clock'|'date';times:string[]}};
export function acceptTraceActivityView(value:unknown):View {
 if(!value||typeof value!=='object'||Array.isArray(value))throw Error('invalid activity view');
 const view=value as Partial<View>,axis=view.axis;
 if(typeof view.maximum!=='number'||!Number.isSafeInteger(view.maximum)||view.maximum<0||!axis||typeof axis!=='object'||!['clock','date'].includes(axis.mode)||!Array.isArray(axis.times)||axis.times.length<2||axis.times.length>16||!axis.times.every(time=>typeof time==='string'&&time.length<=128&&Number.isFinite(Date.parse(time))))throw Error('invalid activity view');
 return view as View;
}
// Only immutable authorized bucket times/counts cross the private program seam.
// Locale/timezone formatting and DOM mutation remain shared Host primitives.
export function enhanceTraceActivityView(root:HTMLElement,plot:HTMLElement,buckets:Bucket[]):()=>void {
 const page=root.closest<HTMLElement>('[data-trace-program-revision]'),revision=page?.dataset.traceProgramRevision;
 const axis=root.querySelector('.obs-activity-axis'),maximum=root.querySelector('[data-activity-max]');
 if(!axis||!maximum)return ()=>{};
 const entry=revision?traceProgram(page!):undefined;
 let active=true,sequence=0;
 const draw=()=>{
  const seq=++sequence,width=plot.clientWidth;
  const apply=(value:unknown)=>{
   if(!active||seq!==sequence||!root.isConnected||entry&&!entry.active)return;
   const view=acceptTraceActivityView(value);
   // Validate and build the whole projection before changing existing SSR facts.
   const nodes=view.axis.times.map(time=>{const node=document.createElement('span');node.textContent=formatLocalTime(time,view.axis.mode,document.documentElement.lang)?.text??'';return node});
   maximum.textContent=String(view.maximum);axis.replaceChildren(...nodes);
  };
  if(!entry){apply(legacyTraceActivityView(buckets,width));return}
  void entry.browser.then(browser=>browser.call({type:'activity_view',input:{buckets,width}})).then(apply).catch(()=>{if(active){active=false;resize.disconnect();void entry.browser.then(browser=>browser.close()).catch(()=>{})}});
 };
 const resize=new ResizeObserver(draw);resize.observe(plot);draw();
 return ()=>{active=false;sequence++;resize.disconnect()};
}

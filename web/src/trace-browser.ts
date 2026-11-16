import type {TracePageInput,TraceNavigationInput} from './trace-page-controls-contract';
import type {TraceControlsInput} from './trace-workbench-controls-contract';
import type {TraceActivityBucket,TraceActivityState,TraceActivityEvent} from './trace-activity-projection';
// Trace-private transport. Only authorized display metadata, local preference
// strings and time coordinates are accepted; no raw model bodies or effects.
type Window = {start:number;end:number};
export type TraceTimelineAction =
 | {kind:'restore'}
 | {kind:'brush';first:number;last:number}
 | {kind:'button';action:'fit'|'in'|'out'|'selected';selected:{start:number;end:number|null}|null}
 | {kind:'key';key:'ArrowLeft'|'ArrowRight'|'Home'|'+'|'-'}
 | {kind:'wheel';pan:boolean;delta_y:number;delta_x:number;anchor:number};
export type TraceBrowserRequest =
 | {type:'page_controls';input:TracePageInput}
 | {type:'navigation';input:TraceNavigationInput}
 | {type:'request_reader';input:{language:'en'|'zh-CN'}}
 | {type:'workbench_controls';input:TraceControlsInput}
 | {type:'activity';input:{buckets:TraceActivityBucket[];state:TraceActivityState|null;event:TraceActivityEvent;language:'en'|'zh-CN'}}
 | {type:'activity_view';input:{buckets:{at:string;until:string;count:number}[];width:number}}
 | {type:'timeline_brush';input:{domain:Window;current:Window;first:number;last:number}}
 | {type:'timeline';input:{domain:Window;current:Window;action:TraceTimelineAction;width:number;has_selected:boolean;language:'en'|'zh-CN';rows:{start:number;end:number|null;running:boolean;first_output:number|null}[]}}
 | {type:'tree';input:{nodes:{id:string;parent?:string;name:string;type:string}[];query:string;closed:string[];language?:'en'|'zh-CN'}}
 | {type:'column_controls';input:{action:'render';visible:boolean[]}|{action:'toggle';visible:boolean[];index:number;checked:boolean}}
 | {type:'columns';input:Partial<Record<'steward.trace.columns.v2'|'steward.trace.columns.v3'|'steward.trace.columns.v4'|'steward.trace.columns.v5',string>>;unavailable?:('steward.trace.columns.v2'|'steward.trace.columns.v3'|'steward.trace.columns.v4'|'steward.trace.columns.v5')[]}
;
export interface TraceBrowser {
 call(request:TraceBrowserRequest):Promise<unknown>;
 close():void;
}
export function connectTraceBrowser(revision:string):Promise<TraceBrowser> {
 return new Promise((resolve,reject)=>{
  const unavailable=()=>new Error('Trace presentation program unavailable');
  if(!/^sha256:[a-f0-9]{64}$/.test(revision)){reject(unavailable());return}
  const frame=document.createElement('iframe');frame.hidden=true;frame.setAttribute('sandbox','allow-scripts');frame.title='Trace isolated presentation';
  let port:MessagePort|undefined,sequence=0,stopped=false,connected=false;
  const pending=new Map<number,{type:TraceBrowserRequest['type'];resolve:(value:unknown)=>void;reject:(error:Error)=>void;timer:ReturnType<typeof setTimeout>}>();
  const close=()=>{
   if(stopped)return;stopped=true;clearTimeout(startup);window.removeEventListener('message',loaded);window.removeEventListener('pagehide',close);port?.close();frame.remove();
   for(const entry of pending.values()){clearTimeout(entry.timer);entry.reject(unavailable())}pending.clear();
   if(!connected)reject(unavailable());
  };
  const call=(request:TraceBrowserRequest):Promise<unknown>=>new Promise((done,failed)=>{
   if(stopped||!connected||!port||pending.size>=32){failed(unavailable());close();return}
   const seq=++sequence,timer=setTimeout(close,2000);pending.set(seq,{type:request.type,resolve:done,reject:failed,timer});
   try{port.postMessage({...request,seq})}catch{close()}
  });
  const loaded=(event:MessageEvent)=>{
   if(stopped||port||event.source!==frame.contentWindow||event.origin!=='null'||event.data?.channel!=='steward-trace-browser'||event.data.type!=='loaded')return;
   const channel=new MessageChannel();port=channel.port1;port.onmessageerror=close;
   port.onmessage=event=>{
    const value=event.data;
    if(!value||typeof value!=='object'||Array.isArray(value)){close();return}
    if(value.type==='connected'&&!connected&&sequence===0){connected=true;clearTimeout(startup);resolve({call,close});return}
    const entry=pending.get(value.seq);
    if(!connected||!entry||value.type!==entry.type||Object.keys(value).length!==3||!Object.hasOwn(value,'result')){close();return}
    clearTimeout(entry.timer);pending.delete(value.seq);entry.resolve(value.result);
   };
   port.start();frame.contentWindow!.postMessage({channel:'steward-trace-browser',type:'connect'},'*',[channel.port2]);window.removeEventListener('message',loaded);
  };
  const startup=setTimeout(close,15000);window.addEventListener('message',loaded);window.addEventListener('pagehide',close);
  frame.src='/api/developer/traces/program?reader_revision='+encodeURIComponent(revision);document.body.append(frame);
 });
}

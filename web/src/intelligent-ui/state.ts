import {useCallback,useRef,useSyncExternalStore} from 'react';
import {readSavedView,saveSavedView} from './host-api';

type Data=Record<string,unknown>;
export type SavedView={state:Data;model_context:Data;version:number};
type Snapshot=SavedView & {ready:boolean;error:string};
type Entry={snapshot:Snapshot;listeners:Set<()=>void>;loaded:boolean;loading?:Promise<void>;saving?:Promise<void>;pending:Data;context:Data;dirty:boolean;timer?:number};
const entries=new Map<string,Entry>();
function entry(ref:string):Entry{let e=entries.get(ref);if(!e){e={snapshot:{state:{},model_context:{},version:0,ready:!ref,error:''},listeners:new Set(),loaded:!ref,pending:{},context:{},dirty:false};entries.set(ref,e)}return e}
const emit=(e:Entry)=>{for(const listener of e.listeners)listener()};
async function load(ref:string,e:Entry){
 if(e.loaded)return;e.loading??=(async()=>{try{const data=await readSavedView(ref);e.context={...data.model_context,...e.context};e.snapshot={...data,state:{...data.state,...e.pending},model_context:e.context,ready:true,error:''};e.loaded=true;emit(e);if(e.dirty)schedule(ref,e)}catch{e.snapshot={...e.snapshot,ready:true,error:'Saved view unavailable. Reload to retry.'};emit(e)}})();const loading=e.loading;try{await loading}finally{if(e.loading===loading)e.loading=undefined}
}
function schedule(ref:string,e:Entry){if(!ref)return;clearTimeout(e.timer);e.timer=window.setTimeout(()=>void flush(ref,e).catch(()=>{}),150)}
async function flush(ref:string,e:Entry):Promise<void>{
 clearTimeout(e.timer);if(e.saving){await e.saving;if(e.dirty)return flush(ref,e);return}if(!e.dirty||!ref)return;
 const job=(async()=>{await load(ref,e);if(!e.loaded)throw Error(e.snapshot.error);while(e.dirty){const state=e.snapshot.state,context=e.context,version=e.snapshot.version;e.dirty=false;e.pending={};
  try{const saved=await saveSavedView(ref,{version,state,model_context:context});e.snapshot={...e.snapshot,version:saved.version,error:''};emit(e)}catch(error){e.dirty=true;e.snapshot={...e.snapshot,error:error instanceof Error?error.message:'Changes could not be saved.'};emit(e);throw error}
 }})();e.saving=job;try{await job}finally{if(e.saving===job)e.saving=undefined}
}
export function setUIState(ref:string,patch:Data,context:Data={}){
 const e=entry(ref);const next={...e.snapshot.state,...patch},nextContext={...e.snapshot.model_context,...context};if(JSON.stringify(next)===JSON.stringify(e.snapshot.state)&&JSON.stringify(nextContext)===JSON.stringify(e.snapshot.model_context))return;e.pending={...e.pending,...patch};e.context={...e.snapshot.model_context,...context};e.dirty=true;e.snapshot={...e.snapshot,state:{...e.snapshot.state,...patch},model_context:e.context,error:''};emit(e);schedule(ref,e);
}
export async function getUIState(ref:string){const e=entry(ref);await load(ref,e);return e.snapshot}
async function persistUIStates(){const detail:{pending:Promise<void>[]}={pending:[]};window.dispatchEvent(new CustomEvent('ui-state-flush',{detail}));await Promise.all(detail.pending);await Promise.all([...entries].filter(([ref,e])=>ref&&(e.dirty||e.saving)).map(([ref,e])=>flush(ref,e)))}
// The caller may stop waiting for shared saves. Their own transport deadlines
// still allow an accepted version to settle before any subsequent retry.
export async function flushUIStates(signal?:AbortSignal){
 if(!signal)return persistUIStates();
 signal.throwIfAborted();
 let stop:()=>void;
 const cancelled=new Promise<never>((_,reject)=>{stop=()=>reject(signal.reason);signal.addEventListener('abort',stop,{once:true})});
 try{await Promise.race([persistUIStates(),cancelled])}finally{signal.removeEventListener('abort',stop!)}
}
export function useUIState<T extends Data>(ref:string,defaults:T,describe?:(state:T)=>Data){
 const e=entry(ref),describeRef=useRef(describe);describeRef.current=describe;
 const subscribe=useCallback((listener:()=>void)=>{e.listeners.add(listener);void load(ref,e);return()=>{e.listeners.delete(listener)}},[e,ref]);
 const snapshot=useSyncExternalStore(subscribe,()=>e.snapshot);
 const value={...defaults,...snapshot.state} as T;
 const update=useCallback((patch:Partial<T>)=>{const next={...defaults,...e.snapshot.state,...patch} as T;setUIState(ref,next,describeRef.current?.(next)??{})},[e,ref,JSON.stringify(defaults)]);
 return [value,update,snapshot] as const;
}
// Flush while this document still owns its state. No localStorage or OS API.
window.addEventListener('pagehide',()=>{for(const [ref,e] of entries)if(ref&&e.dirty)void flush(ref,e).catch(()=>{})});

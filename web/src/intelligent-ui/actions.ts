import {flushUIStates} from './state';
import {postAction,readAction} from './host-api';
export type ActionReceipt={status:string;message_id?:string;invocation_id?:string;result?:{text:string;artifact_refs?:string[];error?:{code:string;message:string}}};
export async function startAction(ref:string,source:string){await flushUIStates();return postAction(ref,source)}
export async function actionResult(ref:string,source:string):Promise<ActionReceipt>{return readAction(ref,source)}

export function canonical(value:unknown):string{if(Array.isArray(value))return "["+value.map(canonical).join(",")+"]";if(value&&typeof value==="object")return "{"+Object.keys(value).sort().map(key=>JSON.stringify(key)+":"+canonical((value as Record<string,unknown>)[key])).join(",")+"}";return JSON.stringify(value)}
// Generated code can propose a frozen action. A trusted Host button owns user
// intent; no iframe postMessage can fabricate an ordinary user request.
const proposals=new Map<string,Set<(result:ActionReceipt)=>void>>();
const watchers=new Map<string,Set<()=>void>>();
export function proposeAction(ref:string,done:(result:ActionReceipt)=>void){const pending=proposals.get(ref)??new Set();pending.add(done);proposals.set(ref,pending);for(const listener of watchers.get(ref)??[])listener();return()=>{pending.delete(done);if(!pending.size)proposals.delete(ref)}}
export function watchAction(ref:string,listener:()=>void){const set=watchers.get(ref)??new Set();set.add(listener);watchers.set(ref,set);if(proposals.has(ref))listener();return()=>{set.delete(listener);if(!set.size)watchers.delete(ref)}}
export function completeAction(ref:string,result:ActionReceipt){if(['pending','waiting'].includes(result.status))return;const pending=proposals.get(ref);proposals.delete(ref);for(const done of pending??[])done(result)}

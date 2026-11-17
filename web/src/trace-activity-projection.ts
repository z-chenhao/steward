export type TraceActivityBucket={at:string;until:string;count:number};
type Range={first:number;last:number};
export type TraceActivityState={drag:(Range&{touch:boolean})|null;pinned:boolean;selected:Range|null;keyboard_start:number|null;focused:number|null};
export type TraceActivityEvent=
 | {kind:'init'|'clear'|'outside'|'blur'|'leave'|'up'}
 | {kind:'focus';index:number}
 | {kind:'key';index:number;key:'ArrowLeft'|'ArrowRight'|'Home'|'End'|'Enter'|'Escape';shift:boolean}
 | {kind:'down';fraction:number;touch:boolean}
 | {kind:'move';fraction:number;touch:boolean;drag:boolean};
type Detail=Range&{count:number;label:string};
export type TraceActivityProjection={state:TraceActivityState;selection:(Detail&{left:number;width:number;persistent:boolean;explore:boolean})|null;tooltip:(Detail&{anchor:number|null})|null|false;focus:number|null;navigate:Range|null;labels:Detail[]|null};
const record=(value:unknown):value is Record<string,unknown>=>!!value&&typeof value==='object'&&!Array.isArray(value);
export function acceptTraceActivityProjection(value:unknown,buckets:TraceActivityBucket[],event:TraceActivityEvent):TraceActivityProjection {
 const invalid=()=>{throw Error('invalid activity projection')};
 const index=(value:unknown):value is number=>Number.isSafeInteger(value)&&Number(value)>=0&&Number(value)<buckets.length;
 const range=(value:unknown):value is Range&Record<string,unknown>=>record(value)&&index(value.first)&&index(value.last)&&value.first<=value.last;
 const detail=(value:unknown):value is Detail&Record<string,unknown>=>range(value)&&record(value)&&typeof value.label==='string'&&value.label.length<=64&&value.count===buckets.slice(value.first,value.last+1).reduce((sum,bucket)=>sum+bucket.count,0);
 if(!record(value)||!record(value.state))return invalid();
 const state=value.state;
 if(state.focused!==null&&!index(state.focused)||typeof state.pinned!=='boolean'||state.keyboard_start!==null&&!index(state.keyboard_start)||state.selected!==null&&!range(state.selected)||state.drag!==null&&(!record(state.drag)||!index(state.drag.first)||!index(state.drag.last)||typeof state.drag.touch!=='boolean'))return invalid();
 const selection=value.selection;
 if(selection!==null&&(!detail(selection)||!record(selection)||typeof selection.left!=='number'||!Number.isFinite(selection.left)||selection.left<0||selection.left>1||typeof selection.width!=='number'||!Number.isFinite(selection.width)||selection.width<0||selection.width>1||selection.left+selection.width>1.000000001||typeof selection.persistent!=='boolean'||typeof selection.explore!=='boolean'))return invalid();
 const tip=value.tooltip;
 if(tip!==null&&tip!==false&&(!detail(tip)||!record(tip)||tip.anchor!==null&&!index(tip.anchor)))return invalid();
 if(value.focus!==null&&!index(value.focus))return invalid();
 if(value.navigate!==null&&(!range(value.navigate)||!['up','key'].includes(event.kind)||event.kind==='key'&&event.key!=='Enter'||!buckets.slice(value.navigate.first,value.navigate.last+1).some(bucket=>bucket.count>0)))return invalid();
 if(value.labels!==null&&(!Array.isArray(value.labels)||event.kind!=='init'||value.labels.length!==buckets.length||!value.labels.every((label,i)=>detail(label)&&label.first===i&&label.last===i)))return invalid();
 if(event.kind==='init'&&(value.labels===null||value.focus===null))return invalid();
 return value as TraceActivityProjection;
}

// Private owning controls recipe, not a public plugin SDK.
export type TraceJsonMode='formatted'|'json';
export type TraceControlsInput={mode:TraceJsonMode;input:boolean;output:boolean;language:'en'|'zh-CN';index:number;count:number};
export type TraceModeState={mode:TraceJsonMode;formatted:boolean;json:boolean};
export type TraceControlsPlan={initial:TraceJsonMode;states:{formatted:TraceModeState;json:TraceModeState};copies:{key:'input'|'output';disabled:boolean;reset_label:string}[];copied:string;unavailable:string;reset_ms:number;previous:number|null;next:number|null};
export function acceptTraceControlsPlan(value:unknown,count:number):TraceControlsPlan {
 const record=(v:unknown):v is Record<string,unknown>=>!!v&&typeof v==='object'&&!Array.isArray(v);
 const mode=(v:unknown)=>v==='formatted'||v==='json',text=(v:unknown)=>typeof v==='string'&&v.length<=256,index=(v:unknown)=>v===null||Number.isSafeInteger(v)&&Number(v)>=0&&Number(v)<count;
 if(!record(value)||!mode(value.initial)||!record(value.states))throw Error('invalid controls');
 for(const key of ['formatted','json']){const state=value.states[key];if(!record(state)||!mode(state.mode)||typeof state.formatted!=='boolean'||typeof state.json!=='boolean')throw Error('invalid mode')}
 if(!Array.isArray(value.copies)||value.copies.length!==2||!value.copies.every((copy,i)=>record(copy)&&copy.key===(i===0?'input':'output')&&typeof copy.disabled==='boolean'&&text(copy.reset_label))||!text(value.copied)||!text(value.unavailable)||!Number.isSafeInteger(value.reset_ms)||Number(value.reset_ms)<0||Number(value.reset_ms)>10000||!index(value.previous)||!index(value.next))throw Error('invalid controls');
 return value as TraceControlsPlan;
}

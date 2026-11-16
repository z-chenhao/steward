import type {TraceControlsInput,TraceControlsPlan} from '../trace-workbench-controls-contract';
// Original 1.0.1 / never-managed direct readers only; delete with those consumers.
export function legacyTraceControls(input:TraceControlsInput):TraceControlsPlan {
 const zh=input.language==='zh-CN';
 return {initial:input.mode,states:{formatted:{mode:'formatted',formatted:true,json:false},json:{mode:'json',formatted:false,json:true}},copies:[{key:'input',disabled:!input.input,reset_label:zh?'复制输入':'Copy input'},{key:'output',disabled:!input.output,reset_label:zh?'复制输出':'Copy output'}],copied:zh?'已复制':'Copied',unavailable:zh?'无法自动复制':'Copy unavailable',reset_ms:1600,previous:input.index>0?input.index-1:null,next:input.index>=0&&input.index+1<input.count?input.index+1:null};
}

import {z} from 'zod';
const text=z.string().max(512).refine(value=>!/\p{Cc}/u.test(value));
const state=z.object({id:z.number().int(),busy:z.boolean(),disabled:z.boolean(),placeholder:z.boolean(),pending_text:text,error:text}).strict();
const view=z.object({label:text.min(1),placeholder_label:text.min(1),states:z.array(state).length(128).refine(rows=>rows.every((row,index)=>row.id===index))}).strict();
export const pickerPresentation=z.object({en:view,zh_cn:view}).strict();
export type PickerPresentation=z.infer<typeof pickerPresentation>;
export type PickerError=''|'partial'|'unavailable'|'selection';
export type PickerFlags={ready:boolean;loading:boolean;saving:boolean;sending:boolean;hasCurrent:boolean};
// The index is only the finite existing render-state vocabulary, not a command
// or workflow interpreter. Request/activation locks remain in the coordinator.
export function modelPickerView(presentation:PickerPresentation,flags:PickerFlags,error:PickerError,language:string){
 const index=Number(flags.ready)+2*Number(flags.loading)+4*Number(flags.saving)+8*Number(flags.sending)+16*Number(flags.hasCurrent)+32*['','partial','unavailable','selection'].indexOf(error);
 const view=language==='zh-CN'?presentation.zh_cn:presentation.en;
 const row=view.states[index];if(!row||row.id!==index)throw Error('Model picker projection unavailable');
 return {...row,label:view.label,placeholder_label:view.placeholder_label};
}

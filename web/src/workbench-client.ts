import {z} from 'zod';
import {legacyWorkbenchRegions,legacyWorkbenchText,legacyWorkbenchTiming} from './legacy/workbench-v1';

const kind=z.enum(['conversation','attention','tasks','configurations','execution','task','settings']);
const schema=z.object({
 revision:z.string().regex(/^sha256:[a-f0-9]{64}$/),
 policy:z.object({live:z.array(z.object({kind,slots:z.array(z.string()).max(7)})).max(7),page_live:z.array(kind).max(7),poll_ms:z.number().int().min(200).max(60000),max_poll_ms:z.number().int().max(60000),read_timeout_ms:z.number().int().min(100).max(10000),execution_sheet:z.boolean(),row_open:z.boolean(),history:z.array(z.object({kind:z.string(),key:z.string()})).max(8)}),
 copy:z.array(z.object({key:z.string(),en:z.string(),zh_cn:z.string()})).max(512),
});
// No node identifies the explicit unbundled/v0 consumer. A present but invalid
// or unavailable managed recipe disables enhancement; it never selects v0.
function load(){
 const node=document.getElementById('web-workbench-config');
 if(!node)return null;
 try{return schema.parse(JSON.parse(node.textContent??''))}catch{return false as const}
}
const recipe=load();
export function workbenchClient(){return recipe}
export function workbenchTiming(){return recipe===null?legacyWorkbenchTiming:recipe===false?null:recipe.policy}
export function workbenchKind(view:string){
 if(view==='/')return 'conversation';
 if(view==='/tasks/attention')return 'attention';
 if(view==='/tasks/configurations')return 'configurations';
 if(view==='/tasks')return 'tasks';
 if(view.startsWith('/tasks/executions/'))return 'execution';
 if(view.startsWith('/tasks/'))return 'task';
 if(view.startsWith('/settings/'))return 'settings';
 return null;
}
export function workbenchRegions(view:string):string[]|null{
 if(recipe===null)return legacyWorkbenchRegions(view);
 if(recipe===false)return null;
 const kind=workbenchKind(view);
 return recipe.policy.live.find(entry=>entry.kind===kind)?.slots??null;
}
export function workbenchText(text:string):string{
 if(recipe===null)return legacyWorkbenchText(text);
 if(recipe===false)return text;
 const entry=recipe.copy.find(entry=>entry.key===text);
 return entry?(document.documentElement.lang==='zh-CN'?entry.zh_cn:entry.en):text;
}
export function workbenchHeaders(etag=''):Record<string,string>{
 const headers:Record<string,string>={};
 if(etag)headers['If-None-Match']=etag;
 if(recipe)headers['X-Steward-Workbench-Reader']=recipe.revision;
 return headers;
}
export function workbenchResponse(response:Response){
 if(recipe&&(response.ok||response.status===304)&&response.headers.get('X-Steward-Workbench-Reader')!==recipe.revision)throw new Error('workbench presentation changed');
}

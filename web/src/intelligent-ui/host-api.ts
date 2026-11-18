import {formViewSchema,type FormView} from './form-schema';
export type {FormView} from './form-schema';
// Private Host transport for the existing state and action consumers. These
// fixed endpoints are not a renderer SDK or a general HTTP/plugin capability.
import type {SavedView} from './state';
import type {ActionReceipt} from './actions';

export async function readSavedView(ref:string):Promise<SavedView>{
 const result=await fetch('/api/ui/state?ref='+encodeURIComponent(ref),{signal:AbortSignal.timeout(10000)});
 if(!result.ok)throw Error('Saved view unavailable.');
 return result.json();
}

// Only an original Cards item is addressable; no Artifact URL or credential
// crosses the opaque-frame boundary. Blob delivery avoids base64 copies.
export async function readCardsImage(ref:string,item:number,signal:AbortSignal):Promise<Blob>{
 const result=await fetch('/ui/cards-image?'+new URLSearchParams({ref,item:String(item)}),{signal,cache:'no-store'});
 if(!result.ok)throw Error('Image unavailable.');
 const image=await result.blob();
 if(!['image/png','image/jpeg','image/webp','image/gif'].includes(image.type))throw Error('Image unavailable.');
 return image;
}

export type CardsContext={title?:string;visible_item:string};
// These descriptions were produced without browser/image input and frozen
// with the original package. The image-bearing frame cannot author them.
export async function readCardsContexts(ref:string,signal:AbortSignal):Promise<CardsContext[]>{
 const result=await fetch('/ui/cards?'+new URLSearchParams({ref,format:'context'}),{signal,cache:'no-store'});
 if(!result.ok)throw Error('View context unavailable.');
 const contexts:unknown=await result.json();
 if(!Array.isArray(contexts)||!contexts.length||contexts.length>16)throw Error('View context unavailable.');
 return contexts.map(value=>{
  if(value===null||typeof value!=='object'||Array.isArray(value)||Object.keys(value).some(key=>key!=='title'&&key!=='visible_item')||typeof value.visible_item!=='string'||!value.visible_item||[...value.visible_item].length>300||(value.title!==undefined&&(typeof value.title!=='string'||[...value.title].length>300)))throw Error('View context unavailable.');
  return value.title===undefined?{visible_item:value.visible_item}:{title:value.title,visible_item:value.visible_item};
 });
}
export async function saveSavedView(ref:string,view:SavedView):Promise<SavedView>{
 const result=await fetch('/api/ui/state',{method:'POST',headers:{'Content-Type':'application/json',Accept:'application/json'},body:JSON.stringify({ref,version:view.version,state:view.state,model_context:view.model_context}),keepalive:true,signal:AbortSignal.timeout(10000)});
 if(!result.ok)throw Error(result.status===409?'Updated in another window. Reload to continue.':'Changes could not be saved. Retry.');
 return result.json();
}
export async function postAction(ref:string,source:string):Promise<ActionReceipt>{
 const result=await fetch('/api/ui/action',{method:'POST',headers:{'Content-Type':'application/json',Accept:'application/json'},body:JSON.stringify({ref,source_id:source})});
 if(!result.ok)throw Error('Action could not be confirmed. Retry.');
 return result.json();
}
export async function readAction(ref:string,source:string):Promise<ActionReceipt>{
 const result=await fetch('/api/ui/action?'+new URLSearchParams({ref,source}));
 if(!result.ok)throw Error('Action unavailable. Retry.');
 return result.json();
}

export type TableContext={sort:number;direction:1|-1;description:{title:string;sort:string|null;direction:1|-1}};
export type TableExport={title:string;columns:string[];rows:string[][]};
const object=(value:unknown):value is Record<string,unknown>=>value!==null&&typeof value==='object'&&!Array.isArray(value);
export async function readTableContexts(ref:string,columns:number,signal:AbortSignal):Promise<TableContext[]>{
 const result=await fetch('/ui/table?'+new URLSearchParams({ref,format:'context'}),{signal,cache:'no-store'});
 if(!result.ok)throw Error('View context unavailable.');
 const values:unknown=await result.json(),seen=new Set<string>();
 if(!Array.isArray(values)||columns<1||columns>12||values.length!==2*(columns+1))throw Error('View context unavailable.');
 return values.map(value=>{
  if(!object(value)||Object.keys(value).length!==3||!Number.isInteger(value.sort)||(value.sort as number)<-1||(value.sort as number)>=columns||(value.direction!==1&&value.direction!==-1)||!object(value.description))throw Error('View context unavailable.');
  const description=value.description,key=value.sort+':'+value.direction;
  if(seen.has(key)||Object.keys(description).length!==3||typeof description.title!=='string'||[...description.title].length>300||description.direction!==value.direction||(value.sort===-1?description.sort!==null:typeof description.sort!=='string'||[...description.sort].length>200))throw Error('View context unavailable.');
  seen.add(key);return {sort:value.sort as number,direction:value.direction,description:{title:description.title,sort:description.sort as string|null,direction:value.direction}};
 });
}
export async function readTableExport(ref:string,rows:number[],signal:AbortSignal):Promise<TableExport>{
 if(rows.length>200||new Set(rows).size!==rows.length||rows.some(row=>!Number.isInteger(row)||row<0||row>=200))throw Error('Export failed.');
 const response=await fetch('/ui/table?'+new URLSearchParams({ref,format:'export',rows:rows.join(',')}),{signal,cache:'no-store'});
 if(!response.ok)throw Error('Export failed.');
 const value:unknown=await response.json();
 if(!object(value)||Object.keys(value).length!==3||typeof value.title!=='string'||value.title.length>600||!Array.isArray(value.columns)||value.columns.length<1||value.columns.length>12||!value.columns.every(c=>typeof c==='string'&&c.length<=400)||!Array.isArray(value.rows)||value.rows.length!==rows.length)throw Error('Export failed.');
 const columns=value.columns as string[];
 if(!value.rows.every(row=>Array.isArray(row)&&row.length===columns.length&&row.every(cell=>typeof cell==='string'&&cell.length<=4000)))throw Error('Export failed.');
 return {title:value.title,columns,rows:value.rows as string[][]};
}

export type ChartExport={title:string;columns:string[];rows:(string|number)[][]};
export async function readChartExport(ref:string,rows:number[],signal:AbortSignal):Promise<ChartExport>{
 if(rows.length>200||new Set(rows).size!==rows.length||rows.some(row=>!Number.isInteger(row)||row<0||row>=200))throw Error('Export failed.');
 const response=await fetch('/ui/chart?'+new URLSearchParams({ref,format:'export',rows:rows.join(',')}),{signal,cache:'no-store'});
 if(!response.ok)throw Error('Export failed.');
 const value:unknown=await response.json();
 if(!object(value)||Object.keys(value).length!==3||typeof value.title!=='string'||value.title.length>600||!Array.isArray(value.columns)||value.columns.length<1||value.columns.length>8||!value.columns.every(c=>typeof c==='string'&&c.length<=600)||!Array.isArray(value.rows)||value.rows.length!==rows.length)throw Error('Export failed.');
 const columns=value.columns as string[];
 if(!value.rows.every(row=>Array.isArray(row)&&row.length===columns.length&&row.every(cell=>typeof cell==='string'&&cell.length<=400||typeof cell==='number'&&Number.isFinite(cell))))throw Error('Export failed.');
 return {title:value.title,columns,rows:value.rows as (string|number)[][]};
}

export async function readFormView(ref:string,signal:AbortSignal):Promise<FormView>{
 const response=await fetch('/ui/form-view?'+new URLSearchParams({ref}),{signal,cache:'no-store'});
 if(!response.ok)throw Error('Form view unavailable.');
 const value:unknown=await response.json();
 const parsed=formViewSchema.safeParse(value);
 if(!parsed.success)throw Error('Form view unavailable.');
 return parsed.data;
}

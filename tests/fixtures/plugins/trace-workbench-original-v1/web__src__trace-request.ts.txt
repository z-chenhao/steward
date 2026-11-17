import {parseExactJson} from './components/ui/read-only-json';
import {t} from './i18n';

// A readable projection may decode JSON string escapes; the original capture
// remains untouched. Duplicate keys and lossy numbers still require raw view.
export function parseReadableRequest(source:string){
 return parseExactJson(source.replace(/"(?:\\.|[^"\\])*"/g,literal=>JSON.stringify(JSON.parse(literal))));
}
type RecordValue=Record<string,unknown>;
const record=(value:unknown):RecordValue|null=>value!==null&&typeof value==='object'&&!Array.isArray(value)?value as RecordValue:null;
const text=(value:unknown):string=>{
 if(typeof value==='string')return value;
 if(Array.isArray(value))return value.map(text).filter(Boolean).join('\n');
 const part=record(value);
 if(!part)return value==null?'':String(value);
 if(typeof part.text==='string')return part.text;
 if(part.content!==undefined)return text(part.content);
 if(part.parts!==undefined)return text(part.parts);
 return JSON.stringify(value,null,2);
};

export function enhanceTraceRequests(scope:ParentNode){
 for(const host of scope.querySelectorAll<HTMLElement>('[data-wire-reader]')){
  const source=host.querySelector<HTMLElement>('[data-wire-source]');if(!source||host.dataset.wireReady)continue;
  let parsed:unknown;
  try{parsed=parseReadableRequest(source.textContent??'')}catch{continue}
  const captures=Array.isArray(parsed)&&parsed.every(value=>{const item=record(value);return item&&'attempt' in item&&'body' in item})?parsed:[{body:parsed}];
  const view=document.createElement('div');view.dataset.formattedOnly='';view.className='obs-wire-reader';
  const paragraph=(content:string)=>{const node=document.createElement('div');node.className='obs-message-text';node.textContent=content;return node};
  const section=(label:string,content:HTMLElement,open=false)=>{const details=document.createElement('details'),summary=document.createElement('summary');details.className='obs-message';details.open=open;summary.textContent=label;details.append(summary,content);return details};
  const json=(value:unknown)=>{const pre=document.createElement('pre');pre.className='obs-json';pre.dataset.readonlyJson='';pre.textContent=JSON.stringify(value,null,2);return pre};
  captures.forEach((value,index)=>{
   const capture=record(value),body=record(capture?.body),attempt=document.createElement('section');attempt.className='obs-wire-attempt';
   const heading=document.createElement('h3');heading.textContent=t('Request')+' '+(index+1)+(typeof body?.model==='string'?' · '+body.model:'');attempt.append(heading);
   if(!body){attempt.append(paragraph(typeof capture?.omitted==='string'&&capture.omitted?capture.omitted:t('Request body was not captured.')));view.append(attempt);return}
   const instructions=body.instructions??body.system;
   if(instructions!==undefined)attempt.append(section(t('System instructions'),paragraph(text(instructions))));
   const input=body.messages??body.input??body.contents;
   const messages=Array.isArray(input)?input:input!==undefined?[{role:'user',content:input}]:[];
   messages.forEach((message,index)=>{
    const item=record(message),role=typeof item?.role==='string'?item.role:typeof item?.type==='string'?item.type:t('Message');
    const content=text(item?.content??item?.parts??message);
    const label=`[${index}] ${role.toUpperCase()} · ${content.replace(/\s+/g,' ').slice(0,90)}`;
    attempt.append(section(label,paragraph(content),index===messages.length-1));
   });
   if(Array.isArray(body.tools)){
    const tools=document.createElement('div');
    for(const value of body.tools){const tool=record(value),definition=record(tool?.function)??tool;tools.append(section(typeof definition?.name==='string'?definition.name:t('Tool'),json(value)))}
    attempt.append(section(t('Tools')+' · '+body.tools.length,tools));
   }
   const parameters=Object.fromEntries(Object.entries(body).filter(([key])=>!['instructions','system','messages','input','contents','tools'].includes(key)));
   if(Object.keys(parameters).length)attempt.append(section(t('Parameters'),json(parameters)));
   view.append(attempt);
  });
  source.dataset.jsonOnly='';host.prepend(view);host.dataset.wireReady='true';
 }
}

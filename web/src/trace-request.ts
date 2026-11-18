import {enhanceReadOnlyJson,parseExactJson,setReadOnlyJsonMode} from './components/ui/read-only-json';
import {traceProgram} from './trace-program';
import {acceptTraceRequestRecipe,type TraceRequestRecipe} from './trace-request-recipe';
import {legacyTraceRequestRecipe} from './legacy/trace-request-v1';

// A readable projection may decode JSON string escapes; the original capture
// remains untouched. Duplicate keys and lossy numbers still require raw view.
export function parseReadableRequest(source:string){
 return parseExactJson(source.replace(/"(?:\\.|[^"\\])*"/g,literal=>JSON.stringify(JSON.parse(literal))));
}
type RecordValue=Record<string,unknown>;
const record=(value:unknown):RecordValue|null=>value!==null&&typeof value==='object'&&!Array.isArray(value)?value as RecordValue:null;
const text=(value:unknown,recipe:TraceRequestRecipe):string=>{
 if(typeof value==='string')return value;
 if(Array.isArray(value))return value.map(value=>text(value,recipe)).filter(value=>!recipe.textRules.skipEmpty||Boolean(value)).join(recipe.textRules.separator);
 const part=record(value);
 if(!part)return value==null?'':String(value);
 if(typeof part[recipe.text]==='string')return part[recipe.text] as string;
 for(const key of recipe.content)if(part[key]!==undefined)return text(part[key],recipe);
 return JSON.stringify(value,null,2);
};

const coalesce=(value:RecordValue|null,keys:string[])=>{for(const key of keys.slice(0,-1))if(value?.[key]!=null)return value[key];return value?.[keys.at(-1)!]};

function renderTraceRequests(scope:ParentNode,recipe:TraceRequestRecipe){
 for(const host of scope.querySelectorAll<HTMLElement>('[data-wire-reader]')){
  const source=host.querySelector<HTMLElement>('[data-wire-source]');if(!source||host.dataset.wireReady)continue;
  let parsed:unknown;
  try{parsed=parseReadableRequest(source.textContent??'')}catch{continue}
  const captures=Array.isArray(parsed)&&parsed.every(value=>{const item=record(value);return item&&recipe.capture.every(key=>key in item)})?parsed:[{[recipe.body]:parsed}];
  const view=document.createElement('div');view.dataset.formattedOnly='';view.className='obs-wire-reader';
  const paragraph=(content:string)=>{const node=document.createElement('div');node.className='obs-message-text';node.textContent=content;return node};
  const section=(label:string,content:HTMLElement,open:boolean)=>{const details=document.createElement('details'),summary=document.createElement('summary');details.className='obs-message';details.open=open;summary.textContent=label;details.append(summary,content);return details};
  const json=(value:unknown)=>{const pre=document.createElement('pre');pre.className='obs-json';pre.dataset.readonlyJson='';pre.textContent=JSON.stringify(value,null,2);return pre};
  captures.forEach((value,index)=>{
   const capture=record(value),body=record(capture?.[recipe.body]),attempt=document.createElement('section');attempt.className='obs-wire-attempt';
   const heading=document.createElement('h3');heading.textContent=recipe.labels.request+' '+(index+1)+(typeof body?.[recipe.model]==='string'?' · '+body[recipe.model]:'');attempt.append(heading);
   if(!body){attempt.append(paragraph(typeof capture?.[recipe.omitted]==='string'&&capture[recipe.omitted]?capture[recipe.omitted] as string:recipe.labels.missing));view.append(attempt);return}
   const sections={system:()=>{
   const instructions=coalesce(body,recipe.instructions);
   if(instructions!==undefined)attempt.append(section(recipe.labels.system,paragraph(text(instructions,recipe)),recipe.expanded.system));
   },messages:()=>{
   const input=coalesce(body,recipe.messages);
   const messages=Array.isArray(input)?input:input!==undefined?[{[recipe.singleMessage.roleField]:recipe.singleMessage.role,[recipe.singleMessage.contentField]:input}]:[];
   messages.forEach((message,index)=>{
    const item=record(message),role=recipe.roles.map(key=>item?.[key]).find(value=>typeof value==='string') as string??recipe.labels.message;
    const content=text(coalesce(item,recipe.content)??message,recipe);
    const label=recipe.labels.indexPrefix+index+recipe.labels.indexSuffix+(recipe.uppercaseRole?role.toUpperCase():role)+recipe.labels.roleSuffix+content.replace(/\s+/g,' ').slice(0,recipe.excerpt);
    attempt.append(section(label,paragraph(content),recipe.expanded.messages==='all'||recipe.expanded.messages==='last'&&index===messages.length-1));
   });
   },tools:()=>{
   if(Array.isArray(body[recipe.tools])){
    const tools=document.createElement('div');
    for(const value of body[recipe.tools] as unknown[]){const tool=record(value),definition=record(tool?.[recipe.definition])??tool;tools.append(section(typeof definition?.[recipe.name]==='string'?definition[recipe.name] as string:recipe.labels.tool,json(value),recipe.expanded.tool))}
    attempt.append(section(recipe.labels.tools+' · '+(body[recipe.tools] as unknown[]).length,tools,recipe.expanded.tools));
   }
   },parameters:()=>{
   const parameters=Object.fromEntries(Object.entries(body).filter(([key])=>!recipe.exclude.includes(key)));
   if(Object.keys(parameters).length)attempt.append(section(recipe.labels.parameters,json(parameters),recipe.expanded.parameters));
   }};
   for(const name of recipe.sections)sections[name]();
   view.append(attempt);
  });
  source.dataset.jsonOnly='';host.prepend(view);host.dataset.wireReady='true';
 }
}


export function enhanceTraceRequests(root:HTMLElement):()=>void {
 if(!root.dataset.traceProgramRevision){renderTraceRequests(root,legacyTraceRequestRecipe());return ()=>{}}
 const entry=traceProgram(root);let active=true;
 if(!root.querySelector('[data-wire-reader]'))return ()=>{};
 void entry.browser.then(browser=>browser.call({type:'request_reader',input:{language:document.documentElement.lang==='zh-CN'?'zh-CN':'en'}})).then(value=>{
  if(!active||!entry.active||!root.isConnected)return;
  const recipe=acceptTraceRequestRecipe(value);renderTraceRequests(root,recipe);enhanceReadOnlyJson(root);
  const selected=root.querySelector<HTMLElement>('[data-json-mode][aria-pressed="true"]')?.dataset.jsonMode;
  setReadOnlyJsonMode(root,selected==='json'?'json':'formatted');
 }).catch(()=>{if(active){entry.active=false;void entry.browser.then(browser=>browser.close()).catch(()=>{})}});
 return ()=>{active=false};
}

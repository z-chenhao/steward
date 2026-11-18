import {randomUUID} from './lib/random-id';
import {useEffect,useLayoutEffect,useMemo,useRef} from 'react';
import {parseMarkdown} from './markdown-parser';
import {morphChildren} from './lib/morph-children';
import DOMPurify from 'dompurify';
import {t} from './i18n';

export {markdownLink} from './markdown-parser';
export function markdownHTML(text:string,streaming=false):string{
 return DOMPurify.sanitize(parseMarkdown(text,streaming,location.origin),{ALLOWED_TAGS:['p','br','strong','em','del','s','blockquote','pre','code','ul','ol','li','hr','h1','h2','h3','h4','h5','h6','table','thead','tbody','tr','th','td','a'],ALLOWED_ATTR:['href','title','class','start','colspan','rowspan'],ALLOW_DATA_ATTR:false});
}

declare global{interface Window{stewardDiagram?:(source:string,id:string)=>Promise<string>}}
let diagrams:Promise<void>|undefined;
const attempted=new WeakSet<Element>();
function loadDiagrams(){
 if(!diagrams)diagrams=new Promise<void>((resolve,reject)=>{
  const script=document.createElement('script');script.src='/diagrams.js';
  const failed=()=>{diagrams=undefined;script.remove();reject(Error('diagram unavailable'))};
  script.onload=()=>window.stewardDiagram?resolve():failed();script.onerror=failed;document.head.append(script);
 });
 return diagrams;
}
export function enhanceDiagrams(scope:ParentNode){
 for(const code of scope.querySelectorAll<HTMLElement>('pre>code.language-mermaid')){
  if(attempted.has(code))continue;attempted.add(code);
  const source=code.textContent??'',pre=code.parentElement!;
  if(source.length>20000)continue;
  void(async()=>{
   try{
    await loadDiagrams();if(!code.isConnected)return;
    const svg=await window.stewardDiagram!(source,'diagram-'+randomUUID());
    if(!code.isConnected)return;
    const img=document.createElement('img');img.className='markdown-diagram';const diagram=new DOMParser().parseFromString(svg,'image/svg+xml');
    img.alt=[diagram.querySelector('svg > title')?.textContent,diagram.querySelector('svg > desc')?.textContent].filter(Boolean).join(' · ')||t('Diagram');
    const href=URL.createObjectURL(new Blob([svg],{type:'image/svg+xml'}));
    img.onload=()=>{URL.revokeObjectURL(href);if(code.isConnected){
     const details=document.createElement('details'),summary=document.createElement('summary');summary.textContent=t('Diagram source');
     pre.before(img,details);details.append(summary,pre);
    }};
    img.onerror=()=>URL.revokeObjectURL(href);img.src=href;
   }catch{pre.setAttribute('aria-label',t('Diagram source'))}
  })();
 }
}
export function Markdown({text,streaming=false}:{text:string;streaming?:boolean}){
 const ref=useRef<HTMLDivElement>(null),html=useMemo(()=>markdownHTML(text),[text]);
 useLayoutEffect(()=>{if(streaming&&ref.current)renderDraftMarkdown(ref.current,text)},[text,streaming]);
 useEffect(()=>{if(!streaming&&ref.current)enhanceDiagrams(ref.current)},[html,streaming]);
 return <div ref={ref} className="semantic markdown" {...(!streaming?{dangerouslySetInnerHTML:{__html:html}}:{})}/>;
}
export function renderDraftMarkdown(node:HTMLElement,text:string){
 node.classList.add('markdown');
 const next=document.createElement('template');next.innerHTML=markdownHTML(text,true);
 morphChildren(node,next.content);
}

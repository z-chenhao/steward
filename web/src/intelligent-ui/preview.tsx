import {useEffect,useState,useRef,useMemo,Component,type ReactNode} from 'react';
import {createRoot,type Root} from 'react-dom/client';
import {flushSync} from 'react-dom';
import {z} from 'zod';
import {OpenIntelligentAnswer} from '../components/open-intelligent-ui/catalog';
import {t} from '../i18n';
import {Markdown} from '../markdown';

const component=z.object({name:z.enum(['Table','barChart','pieChart','plan_visualization','scheduleTime','generateSandboxedUi','steward.lineChart','steward.scatterChart','steward.form','steward.cards']),props:z.unknown()});
const characters=(limit:number)=>z.string().refine(value=>Array.from(value).length<=limit);
export const uiPreviewFrame=z.object({revision:z.number().int().nonnegative(),key:z.string().regex(/^[a-f0-9]{32}$/),state:z.enum(['draft','discarded','committed']),reply_key:z.string().regex(/^[a-f0-9]{32}$/).optional(),items:z.array(z.object({key:z.string().regex(/^[a-f0-9]{32}$/),title:characters(200),text:characters(8192).optional(),components:z.array(component).max(16).nullable()})).max(16).optional()});
type Item=NonNullable<z.infer<typeof uiPreviewFrame>['items']>[number];

// The opaque frame runs only Steward's nonce-authorized display updater.
// Generated scripts and handlers never enter it; snapshots are sanitized by Host.
const visual=z.object({initialHeight:z.number().min(50).max(4000),cssComplete:z.boolean()});
function PreviewSandbox({props,title,session,item,index,revision}:{props:unknown;title:string;session:string;item:string;index:number;revision:number}){
 const[theme,setTheme]=useState(()=>document.documentElement.classList.contains('dark')?'dark':'light');
 const[loaded,setLoaded]=useState(false),[height,setHeight]=useState(0);
 const frame=useRef<HTMLIFrameElement>(null);
 const query=useMemo(()=>new URLSearchParams({key:session,item,component:String(index)}).toString(),[session,item,index]);
 const key=session+':'+item+':'+index;
 useEffect(()=>{const observer=new MutationObserver(()=>setTheme(document.documentElement.classList.contains('dark')?'dark':'light'));observer.observe(document.documentElement,{attributes:true,attributeFilter:['class']});return()=>observer.disconnect()},[]);
 useEffect(()=>{
  const receive=(event:MessageEvent)=>{const d=event.data;if(event.source!==frame.current?.contentWindow||d?.channel!=='steward-preview'||d.key!==key)return;if(d.type==='ready')setLoaded(true);if(d.type==='height'&&Number.isFinite(d.height))setHeight(Math.min(4000,Math.max(50,d.height)))};
  addEventListener('message',receive);return()=>removeEventListener('message',receive);
 },[key]);
 useEffect(()=>{
  if(!loaded)return;
  const request=new AbortController();
  void fetch('/ui/preview?'+query+'&format=json&theme='+theme,{credentials:'same-origin',cache:'no-store',signal:request.signal}).then(r=>{if(!r.ok)throw Error('preview ended');return r.json()}).then(data=>{if(!request.signal.aborted)frame.current?.contentWindow?.postMessage(data,'*')}).catch(()=>{/* Keep the last safe frame until commit/discard or reconnect. */});
  return()=>request.abort();
 },[query,revision,theme,loaded]);
 const parsed=visual.safeParse(props);if(!parsed.success||!parsed.data.cssComplete)return null;
 return <div className="intelligent-widget"><iframe ref={frame} onLoad={()=>setLoaded(true)} title={title} sandbox="allow-scripts" referrerPolicy="no-referrer" style={{height:Math.max(parsed.data.initialHeight,height),width:'100%',border:0}} src={'/ui/preview?'+query}/></div>;
}
class PreviewBoundary extends Component<{children:ReactNode},{failed:boolean}>{
 state={failed:false};static getDerivedStateFromError(){return {failed:true}}
 render(){return this.state.failed?null:this.props.children}
}
function PreviewAnswer({item,session,revision}:{item:Item;session:string;revision:number}){
 return <article className="message assistant message-split" aria-busy="true" data-ui-preview={item.key}>
  {item.text&&<div className="message-bubble" inert><Markdown text={item.text} streaming/></div>}
  <div className="message-bubble">
  <p className="field-help" role="status">{t('Generating interactive answer…')}</p>
  {!item.components?.length&&item.title&&<strong>{item.title}</strong>}
  <div inert>{item.components?.map((c,index)=><PreviewBoundary key={index}>{c.name==='generateSandboxedUi'?<PreviewSandbox props={c.props} title={item.title} session={session} item={item.key} index={index} revision={revision}/>:<OpenIntelligentAnswer value={{...c,disabled:true}} fallback=""/>}</PreviewBoundary>)}</div>
 </div></article>;
}

export function createUIPreview(node:HTMLElement){
 let root:Root|null=null,key='',replyKey='';
 let handoff:HTMLElement|null=null,observer:MutationObserver|null=null;
 const clear=()=>{observer?.disconnect();observer=null;handoff?.removeAttribute('data-ui-handoff');handoff=null;root?.unmount();root=null;key='';replyKey='';node.hidden=true;node.replaceChildren()};
 const reconcile=(deferred=false)=>{
  if(!replyKey)return;
  if(deferred){clear();return}
  const reply=document.querySelector<HTMLElement>(`[data-reply-key="${replyKey}"]`);
  if(!reply)return;
  if(!reply.querySelector('iframe[data-ready="false"]')){clear();return}
  // Keep the last safe picture while the separately authorized final frame
  // hydrates. The final instance owns all interaction and saved user state.
  if(handoff!==reply){observer?.disconnect();observer=null;handoff?.removeAttribute('data-ui-handoff');handoff=reply;}
  reply.setAttribute('data-ui-handoff','');
  if(!observer){observer=new MutationObserver(()=>reconcile());observer.observe(reply,{subtree:true,childList:true,attributes:true,attributeFilter:['data-ready']});}
 };
 return {
  clear,reconcile,
  receive(data:unknown){
   const parsed=uiPreviewFrame.safeParse(data);if(!parsed.success)return;
   const frame=parsed.data;
   if(frame.state==='discarded'){if(frame.key===key)clear();return}
   if(frame.state==='committed'){if(frame.key===key){replyKey=frame.reply_key??'';reconcile();return 'committed' as const}return}
   if(key!==frame.key)clear();key=frame.key;replyKey='';
   const items=frame.items??[];node.hidden=!items.length;
   if(!items.length){root?.unmount();root=null;return}
   root??=createRoot(node);
   flushSync(()=>root!.render(<>{items.map(item=><PreviewAnswer key={item.key} item={item} session={frame.key} revision={frame.revision}/>)}</>));
  }
 };
}

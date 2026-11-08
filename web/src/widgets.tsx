import {randomUUID} from './lib/random-id';
import {t} from './i18n';
import {useEffect,useRef,useState} from 'react';
import {AnswerButton as Button} from './components/ui/answer-controls';
import {AnswerSurface,type DisplayMode} from './intelligent-ui/surface';
import {useUIState,getUIState,setUIState,flushUIStates} from './intelligent-ui/state';
import {useInteractionGroup} from './intelligent-ui/group';
import {proposeAction,canonical} from './intelligent-ui/actions';
import {csv,download,safeName,safeSVG,svgPNG} from './intelligent-ui/exports';

// The opaque-origin frame proposes bounded data and user actions. Only the Host
// owns persistence, ingress, the ordinary tool pipeline and export downloads.
export function IntelligentWidget({title,reference,fallback,initialHeight=300}:{title:string;reference:string;fallback:string;initialHeight?:number}){
 const frame=useRef<HTMLIFrameElement>(null),draftEditor=useRef<HTMLTextAreaElement>(null);const[initialTheme]=useState(()=>document.documentElement.classList.contains('dark')?'dark':'light');
 const[failed,setFailed]=useState(false),[ready,setReady]=useState(false),[mode,setMode]=useState<DisplayMode>('inline'),[graphic,setGraphic]=useState(false);
 const[draft,setDraft]=useState(''),[busy,setBusy]=useState(false),[error,setError]=useState(''),[link,setLink]=useState('');
 const[, ,savedView]=useUIState(reference,{});
 const source=useRef<string|null>(null),sending=useRef(false),draftValue=useRef(''),exportID=useRef('');const group=useInteractionGroup(),actions=useRef(group.actions);actions.current=group.actions;
 function updateDraft(value:string){draftValue.current=value;setDraft(value)}
 function requestExport(format:string){exportID.current=randomUUID();frame.current?.contentWindow?.postMessage({channel:'open-generative-ui',type:'export',id:exportID.current,format},'*')}
 useEffect(()=>{
  let alive=true;const timers=new Set<number>(),captures=new Map<string,()=>void>(),proposals=new Set<()=>void>();const post=(value:Record<string,unknown>)=>frame.current?.contentWindow?.postMessage({channel:'open-generative-ui',...value},'*');
  const theme=()=>{const css=getComputedStyle(document.documentElement),tokens:Record<string,string>={};for(const[target,source]of Object.entries({'--color-background-primary':'--muted','--color-background-secondary':'--hover','--color-background-tertiary':'--card','--color-text-primary':'--foreground','--color-text-secondary':'--muted-foreground','--color-text-tertiary':'--muted-foreground','--color-border-primary':'--control-border','--color-border-secondary':'--border','--color-border-tertiary':'--border','--font-sans':'--font-ui','--color-text-info':'--chat-accent'}))tokens[target]=css.getPropertyValue(source).trim();post({type:'theme',theme:document.documentElement.classList.contains('dark')?'dark':'light',tokens})};
  const listener=async(event:MessageEvent)=>{
   if(event.source!==frame.current?.contentWindow||event.origin!=='null'||event.data?.channel!=='open-generative-ui')return;const value=event.data;
   if(value.type==='ready'){theme();const saved=await getUIState(reference);if(alive)post({type:'restore',state:saved.state,modelContext:saved.model_context})}
   if(value.type==='restored'){setReady(true);setGraphic(value.graphic===true)}
   if(value.type==='error')setFailed(true);
   if(value.type==='exit')setMode('inline');
   if(value.type==='view-state'&&value.state&&typeof value.state==='object'&&!Array.isArray(value.state)){
    if(JSON.stringify(value.state).length<=16*1024&&JSON.stringify(value.model_context??{}).length<=4096)setUIState(reference,value.state,Object.keys(value.model_context??{}).length?{title,...value.model_context}:{});if(typeof value.id==='string'){captures.get(value.id)?.();captures.delete(value.id)}
   }
   if(value.type==='exported'&&value.id===exportID.current&&typeof value.data==='string'&&value.data.length<2*1024*1024){exportID.current='';try{if(value.format==='png')await svgPNG(value.data,safeName(title)+'.png');else if(value.format==='csv')download(csv(JSON.parse(value.data)),'text/csv;charset=utf-8',safeName(title)+'.csv');else download(value.format==='svg'?safeSVG(value.data):value.data,value.format==='html'?'text/html':'image/svg+xml',safeName(title)+'.'+value.format)}catch{setError('Export failed.')}}
   if(value.type==='exported'&&value.id===exportID.current&&value.error){exportID.current='';setError('Export unavailable.')}
   if(value.type==='resize'&&typeof value.height==='number'&&Number.isFinite(value.height)&&frame.current)frame.current.style.height=Math.max(50,Math.min(4000,Math.ceil(value.height)))+'px';
   if(value.type==='invoke'&&typeof value.id==='string'&&value.id.length<32){let result:Record<string,unknown>={accepted:false};
    if(value.method==='sendPrompt'){requestAnimationFrame(()=>draftEditor.current?.focus({preventScroll:true}));if(!sending.current&&!draftValue.current&&typeof value.args?.text==='string'&&value.args.text.trim()&&value.args.text.length<=4096){updateDraft(value.args.text);source.current=null;setError('');result={accepted:true}}}
    if(value.method==='openLink'&&typeof value.args?.url==='string'&&value.args.url.length<=2048){try{const url=new URL(value.args.url);if(url.protocol==='https:'&&!url.username&&!url.password){setLink(url.href);result={accepted:true}}}catch{}}
    if(value.method==='displayMode'&&['inline','fullscreen','modal','pip'].includes(value.args?.mode)){setMode(value.args.mode);result={accepted:true}}
    if(value.method==='callTool'){
     const action=actions.current.find(a=>(a.tool===value.args?.name||a.id===value.args?.action)&&(a.id===value.args?.action||canonical(a.arguments)===canonical(value.args?.arguments)));
     if(action){const cancel=proposeAction(action.reference,receipt=>{if(alive)post({type:'result',id:value.id,result:{accepted:receipt.status==='succeeded',...receipt.result}})});proposals.add(cancel);return}
    }
    post({type:'result',id:value.id,result});
   }
  };
  const capture=(event:Event)=>{if(frame.current?.dataset.ready!=='true')return;const detail=(event as CustomEvent<{pending:Promise<void>[]} >).detail;detail.pending.push(new Promise<void>(resolve=>{const id=randomUUID();const timer=window.setTimeout(()=>{captures.delete(id);resolve()},500);captures.set(id,()=>{clearTimeout(timer);resolve()});post({type:'capture',id})}))};
  window.addEventListener('ui-state-flush',capture);window.addEventListener('message',listener);const observer=new MutationObserver(theme);observer.observe(document.documentElement,{attributes:true,attributeFilter:['class']});const timeout=window.setTimeout(()=>{if(frame.current?.dataset.ready!=='true')setFailed(true)},10000);
  return()=>{alive=false;window.removeEventListener('message',listener);window.removeEventListener('ui-state-flush',capture);for(const resolve of captures.values())resolve();captures.clear();for(const cancel of proposals)cancel();observer.disconnect();window.clearTimeout(timeout);for(const timer of timers)window.clearTimeout(timer)};
 },[reference,title]);
 async function send(){if(sending.current||!draft.trim())return;sending.current=true;setBusy(true);setError('');source.current??=randomUUID();try{await flushUIStates();const response=await fetch('/api/message',{method:'POST',headers:{'Content-Type':'application/json'},body:JSON.stringify({source_id:'ui-widget:'+source.current,content:[{type:'text',text:draft}]})});if(!response.ok)throw Error('Your message could not be sent. Retry to use the same receipt.');updateDraft('');source.current=null}catch(e){setError(e instanceof Error?e.message:'Message unavailable')}finally{sending.current=false;setBusy(false)}}
 return <section className="intelligent-widget" data-widget-ref={reference}><AnswerSurface title={title} sandbox={!failed} graphic={graphic} mode={mode} onMode={setMode} onExport={requestExport} actions={!failed?[{label:'Download CSV',run:()=>requestExport('csv')},{label:'Download HTML',run:()=>requestExport('html')}]:[]}>
  <header><strong>{title}</strong></header><div className="widget-canvas">{!failed&&<iframe ref={frame} style={{height:initialHeight}} title={title} src={'/ui/widget?ref='+encodeURIComponent(reference)+'&theme='+initialTheme} sandbox="allow-scripts" referrerPolicy="no-referrer" data-ready={String(ready)} onError={()=>setFailed(true)}/>} {!ready&&!failed&&<div className="widget-loading" role="status" aria-label={t('Loading interactive answer')}><span className="typing-dots" aria-hidden="true"><i/><i/><i/></span></div>}</div>
  {failed&&<div className="widget-fallback"><p className="field-help" role="status">{t('This interactive view could not be displayed.')}</p><p className="semantic">{fallback}</p></div>}
  {draft&&<div className="widget-followup"><label>{t('Message to Ghost')}<textarea ref={draftEditor} disabled={busy} value={draft} maxLength={4096} onChange={e=>{updateDraft(e.target.value);source.current=null}}/></label><div className="widget-followup-actions"><Button type="button" disabled={busy} onClick={()=>void send()}>{t(busy?'Sending…':'Send to Ghost')}</Button><Button type="button" variant="secondary" disabled={busy} onClick={()=>{updateDraft('');source.current=null}}>{t('Dismiss')}</Button></div></div>}
  {link&&<p><a href={link} target="_blank" rel="noopener noreferrer">{t('Open linked page')} <span aria-hidden="true">↗</span></a></p>}{(error||savedView.error)&&<p role="alert">{t(error||savedView.error)}</p>}
 </AnswerSurface></section>
}

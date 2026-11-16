import {useEffect,useLayoutEffect,useRef,useState} from 'react';
import {t} from '../i18n';
import {randomUUID} from '../lib/random-id';
import {AnswerSurface,type DisplayMode} from './surface';
import {cardsSchema} from './extensions';
import {useInteractionGroup} from './group';
import {getUIState,setUIState,useUIState} from './state';
import {readCardsImage,readCardsContexts,type CardsContext} from './host-api';

type CardsProps=ReturnType<typeof cardsSchema.parse>;
const record=(value:unknown):value is Record<string,unknown>=>value!==null&&typeof value==='object'&&!Array.isArray(value);

// The projection supplies the original component and props. Candidate messages
// cannot choose a persistence ref, group, ordinary input, action or tool target.
export function FrozenCardsView({reference,props,fallback}:{reference:string;props:unknown;fallback:string}){
 const parsed=cardsSchema.safeParse(props);
 if(!parsed.success)return <p className="semantic">{fallback}</p>;
 return <CardsFrame key={reference} reference={reference} props={parsed.data} fallback={fallback}/>;
}

function CardsFrame({reference,props,fallback}:{reference:string;props:CardsProps;fallback:string}){
 const frame=useRef<HTMLIFrameElement>(null),ready=useRef(false),contexts=useRef<CardsContext[]>([]);
 const [mode,setMode]=useState<DisplayMode>('inline');
 const [failed,setFailed]=useState(false),[height,setHeight]=useState(50);
 const [theme]=useState(()=>document.documentElement.classList.contains('dark')?'dark':'light');
 const group=useInteractionGroup(),current=useRef({group,props});current.current={group,props};
 const[,,saved]=useUIState(reference,{});
 const post=(value:Record<string,unknown>)=>frame.current?.contentWindow?.postMessage({channel:'steward-cards-view',...value},'*');
 const environment=()=>{const style=getComputedStyle(document.documentElement),tokens=Object.fromEntries(['--card','--border','--foreground','--muted-foreground','--hover','--font-ui','--control-border'].map(key=>[key,style.getPropertyValue(key).trim()]));post({type:'theme',theme:document.documentElement.classList.contains('dark')?'dark':'light',tokens,viewportWidth:window.innerWidth})};
 useLayoutEffect(()=>{
  if(failed)return;
  let alive=true,restoring=false;ready.current=false;
  let gesturePort:MessagePort|undefined;
  const captures=new Map<string,()=>void>();
  const images=new Set<number>(),imageRequests=new AbortController();
  const timeout=window.setTimeout(()=>{if(alive&&!ready.current)setFailed(true)},10000);
  const receive=async(event:MessageEvent)=>{
   const value=event.data;
   if(event.source!==frame.current?.contentWindow||event.origin!=='null'||value?.channel!=='steward-cards-view')return;
   if(value.type==='failed'){setFailed(true);return}
   if(value.type==='gesture-port'&&!gesturePort&&event.ports.length===1){
    gesturePort=event.ports[0];
    gesturePort.onmessage=message=>{
     const intent=message.data;
     if(!alive||!ready.current)return;
     if(intent?.type==='escape'){setMode('inline');return}
     if(intent?.type!=='open-link'||!Number.isInteger(intent.item)||!current.current.props.items[intent.item]?.url||!navigator.userActivation.isActive)return;
     window.open('/ui/cards-link?'+new URLSearchParams({ref:reference,item:String(intent.item)}),'_blank','noopener,noreferrer');
    };
   }
   if(value.type==='ready'&&!restoring){
    restoring=true;environment();
    try{
     const[view,frozen,selection]=await Promise.all([getUIState(reference),readCardsContexts(reference,imageRequests.signal),current.current.group.readState()]);
     if(frozen.length!==current.current.props.items.length)throw Error('View context unavailable.');
     if(alive){contexts.current=frozen;post({type:'restore',state:view.state,selected:selection.selected})}
    }catch{if(alive)setFailed(true)}
   }
   if(value.type==='restored')ready.current=true;
   if(value.type==='height'&&Number.isFinite(value.height))setHeight(Math.max(1,Math.min(4000,Math.ceil(value.height))));
   if(value.type==='view-state'&&ready.current&&record(value.state)&&Object.keys(value.state).length===1&&Number.isInteger(value.state.index)){
    const index=value.state.index as number,description=contexts.current[index];
    if(!description)return;
    setUIState(reference,{index},description);
    if(typeof value.id==='string'&&value.id.length<=64){captures.get(value.id)?.();captures.delete(value.id)}
   }
   if(value.type==='select'&&ready.current&&Number.isInteger(value.item)){
    const item=current.current.props.items[value.item];
    if(item)current.current.group.select(item.title);
   }
   if(value.type==='image-request'&&ready.current&&Number.isInteger(value.item)&&current.current.props.items[value.item]?.image_ref&&!images.has(value.item)){
    const item=value.item;images.add(item);
    try{const image=await readCardsImage(reference,item,imageRequests.signal);if(alive)post({type:'image',item,image})}
    catch{if(alive)post({type:'image-error',item})}
   }
  };
  const capture=(event:Event)=>{
   if(!ready.current)return;
   const detail=(event as CustomEvent<{pending:Promise<void>[]} >).detail;
   detail.pending.push(new Promise<void>(resolve=>{
    const id=randomUUID();const timer=window.setTimeout(()=>{captures.delete(id);resolve()},500);
    captures.set(id,()=>{window.clearTimeout(timer);resolve()});post({type:'capture',id});
   }));
  };
  window.addEventListener('message',receive);window.addEventListener('resize',environment);window.addEventListener('ui-state-flush',capture);
  const observer=new MutationObserver(environment);observer.observe(document.documentElement,{attributes:true,attributeFilter:['class']});
  return()=>{post({type:'dispose'});alive=false;ready.current=false;gesturePort?.close();imageRequests.abort();window.clearTimeout(timeout);observer.disconnect();window.removeEventListener('message',receive);window.removeEventListener('resize',environment);window.removeEventListener('ui-state-flush',capture);for(const resolve of captures.values())resolve();captures.clear()};
 },[reference,failed]);
 useEffect(()=>{post({type:'group',selected:group.selected})},[group.selected]);
 if(failed)return <p className="semantic">{fallback}</p>;
 return <section className="openintelligentui-native" data-openintelligentui="steward.cards" data-view-ref={reference}><AnswerSurface title={props.title||props.items[0].title} mode={mode} onMode={setMode}><section className="native-carousel"><iframe ref={frame} onLoad={environment} title="Cards view" sandbox="allow-scripts" referrerPolicy="no-referrer" onError={()=>setFailed(true)} src={'/ui/cards?'+new URLSearchParams({ref:reference,theme})} style={{display:'block',width:'100%',height,border:0}}/></section>{saved.error&&<p className="field-help" role="alert">{t(saved.error)}</p>}</AnswerSurface></section>;
}

import {useEffect,useLayoutEffect,useRef,useState} from 'react';
import {t} from '../i18n';
import {randomUUID} from '../lib/random-id';
import {Input} from '../components/ui/answer-controls';
import {AnswerSurface,type DisplayMode} from './surface';
import {tableSchema} from './table-schema';
import {useInteractionGroup} from './group';
import {getUIState,setUIState,useUIState} from './state';
import {readTableContexts,readTableExport,type TableContext} from './host-api';
import {csv,download,safeName} from './exports';
import {Icon} from './icons';

type TableProps=ReturnType<typeof tableSchema.parse>;
const record=(value:unknown):value is Record<string,unknown>=>value!==null&&typeof value==='object'&&!Array.isArray(value);
export function FrozenTableView({reference,props,fallback}:{reference:string;props:unknown;fallback:string}){
 const parsed=tableSchema.strict().safeParse(props);
 if(!parsed.success||parsed.data.rows.some(row=>row.length!==parsed.data.columns.length))return <p className="semantic">{fallback}</p>;
 return <TableFrame key={reference} reference={reference} props={parsed.data} fallback={fallback}/>;
}

// The original projection closes over all identities and facts. The Worker can
// propose a finite sort state or original row positions, never model prose,
// persistence identities, destinations, ordinary inputs or file bytes.
function TableFrame({reference,props,fallback}:{reference:string;props:TableProps;fallback:string}){
 const frame=useRef<HTMLIFrameElement>(null),ready=useRef(false),contexts=useRef<TableContext[]>([]);
 const [mode,setMode]=useState<DisplayMode>('inline'),[failed,setFailed]=useState(false),[active,setActive]=useState(false);
 const [height,setHeight]=useState(50),[filterOpen,setFilterOpen]=useState(false),[exportError,setExportError]=useState('');
 const [theme]=useState(()=>document.documentElement.classList.contains('dark')?'dark':'light');
 const group=useInteractionGroup(),current=useRef({group,props});current.current={group,props};
 const[,,saved]=useUIState(reference,{});
 const requestExport=useRef<()=>void>(()=>{});
 const post=(value:Record<string,unknown>)=>frame.current?.contentWindow?.postMessage({channel:'steward-table-view',...value},'*');
 const environment=()=>{
  const style=getComputedStyle(document.documentElement),tokens=Object.fromEntries(['--card','--border','--foreground','--muted-foreground','--hover','--font-ui','--font-sans','--control-border','--focus'].map(key=>[key,style.getPropertyValue(key).trim()]));
  post({type:'theme',theme:document.documentElement.classList.contains('dark')?'dark':'light',tokens,viewportWidth:window.innerWidth,locale:Intl.Collator().resolvedOptions().locale,language:document.documentElement.lang==='zh-CN'?'zh-CN':'en'});
 };
 useLayoutEffect(()=>{
  if(failed)return;
  let alive=true,restoring=false;
  const captures=new Map<string,()=>void>(),exports=new Map<string,number>(),requests=new AbortController();
  const timeout=window.setTimeout(()=>{if(alive&&!ready.current)setFailed(true)},10000);
  const acceptState=(state:unknown)=>{
   if(!record(state)||Object.keys(state).length!==2||!Number.isInteger(state.sort)||(state.direction!==1&&state.direction!==-1))return false;
   const frozen=contexts.current.find(c=>c.sort===state.sort&&c.direction===state.direction);
   if(!frozen)return false;
   setUIState(reference,{sort:frozen.sort,direction:frozen.direction},frozen.description);return true;
  };
  const receive=async(event:MessageEvent)=>{
   const value=event.data;
   if(!alive||event.source!==frame.current?.contentWindow||event.origin!=='null'||value?.channel!=='steward-table-view')return;
   if(value.type==='failed'){setFailed(true);return}
   if(value.type==='ready'&&!restoring){
    restoring=true;environment();
    try{
     const[view,frozen]=await Promise.all([getUIState(reference),readTableContexts(reference,current.current.props.columns.length,requests.signal)]);
     if(view.error)throw Error('Saved view unavailable.');
     const group=await current.current.group.readState();
     if(alive){contexts.current=frozen;post({type:'restore',state:view.state,query:group.query,selected:group.selected})}
    }catch{if(alive)setFailed(true)}
   }
   if(value.type==='restored'&&restoring&&!ready.current){ready.current=true;setActive(true)}
   if(value.type==='height'&&Number.isFinite(value.height))setHeight(Math.max(1,Math.min(100000,Math.ceil(value.height))));
   if(!ready.current)return;
   if(value.type==='escape'){setMode('inline');return}
   if(value.type==='view-state'&&acceptState(value.state)&&typeof value.id==='string'){
    captures.get(value.id)?.();captures.delete(value.id);
   }
   if(value.type==='select'&&Number.isInteger(value.row)&&value.row>=0){
    const row=current.current.props.rows[value.row];if(row)current.current.group.select(row[0]);
   }
   if(value.type==='reset-group'){
    const group=current.current.group;group.setQuery('');if(group.selected)group.select(group.selected);
   }
   if(value.type==='export-rows'&&typeof value.id==='string'&&exports.has(value.id)){
    window.clearTimeout(exports.get(value.id));exports.delete(value.id);
    try{
     if(!Array.isArray(value.rows)||value.rows.length>current.current.props.rows.length||new Set(value.rows).size!==value.rows.length||value.rows.some((row:unknown)=>!Number.isInteger(row)||(row as number)<0||(row as number)>=current.current.props.rows.length))throw Error('Export failed.');
     const original=await readTableExport(reference,value.rows,requests.signal);
     if(alive)download(csv([original.columns,...original.rows]),'text/csv;charset=utf-8',safeName(original.title)+'.csv');
    }catch{if(alive)setExportError('Export failed.')}
   }
  };
  requestExport.current=()=>{
   if(!ready.current||exports.size)return;
   setExportError('');const id=randomUUID();
   exports.set(id,window.setTimeout(()=>{exports.delete(id);if(alive)setExportError('Export failed.')},5000));post({type:'export',id});
  };
  const capture=(event:Event)=>{
   if(!ready.current)return;
   const detail=(event as CustomEvent<{pending:Promise<void>[]} >).detail;
   detail.pending.push(new Promise<void>(resolve=>{
    const id=randomUUID(),timer=window.setTimeout(()=>{captures.delete(id);resolve()},500);
    captures.set(id,()=>{window.clearTimeout(timer);resolve()});post({type:'capture',id});
   }));
  };
  window.addEventListener('message',receive);window.addEventListener('resize',environment);window.addEventListener('ui-state-flush',capture);
  const observer=new MutationObserver(environment);observer.observe(document.documentElement,{attributes:true,attributeFilter:['class','lang']});
  return()=>{alive=false;ready.current=false;post({type:'dispose'});requestExport.current=()=>{};requests.abort();window.clearTimeout(timeout);observer.disconnect();window.removeEventListener('message',receive);window.removeEventListener('resize',environment);window.removeEventListener('ui-state-flush',capture);for(const resolve of captures.values())resolve();for(const timer of exports.values())window.clearTimeout(timer);captures.clear();exports.clear()};
 },[reference,failed]);
 useEffect(()=>{post({type:'group',query:group.query,selected:group.selected})},[group.query,group.selected]);
 if(failed)return <p className="semantic">{fallback}</p>;
 return <section className="openintelligentui-native" data-openintelligentui="Table" data-view-ref={reference}><AnswerSurface title={props.title} mode={mode} onMode={setMode} actions={[
  {label:'Filter',icon:<Icon name="filter"/>,disabled:!active,run:()=>setFilterOpen(!filterOpen)},
  {label:'Reset',icon:<Icon name="reset"/>,disabled:!active,run:()=>post({type:'reset'})},
  {label:'Download CSV',icon:<Icon name="download"/>,disabled:!active,run:()=>requestExport.current()},
 ]}>
  {(filterOpen||group.query)&&<div className="answer-filter"><Input type="search" aria-label={t('Filter rows')} placeholder={t('Filter')} value={group.query} disabled={!active} onChange={event=>group.setQuery(event.target.value)}/></div>}
  <iframe data-table-view="true" ref={frame} onLoad={environment} title="Table view" sandbox="allow-scripts" referrerPolicy="no-referrer" onError={()=>setFailed(true)} src={'/ui/table?'+new URLSearchParams({ref:reference,theme})} style={{display:'block',width:'100%',height,border:0}}/>
  {(saved.error||exportError)&&<p className="field-help" role="alert">{t(saved.error||exportError)}</p>}
 </AnswerSurface></section>;
}

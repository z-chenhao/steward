import {readChartExport} from './host-api';
import {csv,download,safeName} from './exports';
import {useEffect,useLayoutEffect,useRef,useState} from 'react';
import {nativeDefinition} from '../components/open-intelligent-ui/catalog';
import {t} from '../i18n';
import {randomUUID} from '../lib/random-id';
import {ChartDrawing,type ChartData,type ChartProps} from './charts';
import {AnswerSurface} from './surface';
import {useInteractionGroup} from './group';
import {getUIState,setUIState,useUIState} from './state';
import {Icon} from './icons';

type Projection={rows:number[];horizontal:boolean;numericLine:boolean;height:number;zoom:boolean;start:number;end:number;filteredLength:number;selected:boolean[];opacity:number[]};
const kinds={'barChart':'bar','pieChart':'pie','steward.lineChart':'line','steward.scatterChart':'scatter'} as const;
const record=(value:unknown):value is Record<string,unknown>=>value!==null&&typeof value==='object'&&!Array.isArray(value);
function valid(value:unknown,count:number):value is Projection{
 if(!record(value)||!Array.isArray(value.rows)||value.rows.length>count||new Set(value.rows).size!==value.rows.length||value.rows.some(i=>!Number.isInteger(i)||i<0||i>=count))return false;
 return typeof value.horizontal==='boolean'&&typeof value.numericLine==='boolean'&&typeof value.height==='number'&&Number.isFinite(value.height)&&value.height>=260&&value.height<=640&&typeof value.zoom==='boolean'&&[value.start,value.end,value.filteredLength].every(n=>typeof n==='number'&&Number.isInteger(n)&&n>=0&&n<=1000000)&&Array.isArray(value.selected)&&value.selected.length===value.rows.length&&value.selected.every(v=>typeof v==='boolean')&&Array.isArray(value.opacity)&&value.opacity.length===value.rows.length&&value.opacity.every(v=>typeof v==='number'&&Number.isFinite(v)&&v>=0&&v<=1);
}
function parseChart(name:string,props:unknown):ChartProps|null{
 const parsed=nativeDefinition.safeParse({name,props});
 if(!parsed.success||!(parsed.data.name in kinds))return null;
 const chart=parsed.data as {name:keyof typeof kinds;props:Omit<ChartProps,'kind'|'reference'>};
 return {...chart.props,kind:kinds[chart.name]};
}
export function FrozenChartView({name,reference,props,fallback}:{name:string;reference:string;props:unknown;fallback:string}){
 const chart=parseChart(name,props);if(!chart)return <p className="semantic">{fallback}</p>;
 return <ChartFrame key={reference} name={name} reference={reference} props={chart} fallback={fallback}/>;
}
export function PreviewChartView({name,props,session,item,index,revision,fallback}:{name:string;props:unknown;session:string;item:string;index:number;revision:number;fallback:string}){
 const chart=parseChart(name,props);if(!chart)return null;
 const source='/ui/chart-preview?'+new URLSearchParams({key:session,item,component:String(index),revision:String(revision)});
 return <ChartFrame key={source} name={name} reference="" props={chart} fallback={fallback} readonly source={source}/>;
}

function ChartFrame({name,reference,props,fallback,readonly=false,source}:{name:string;reference:string;props:ChartProps;fallback:string;readonly?:boolean;source?:string}){
 const frame=useRef<HTMLIFrameElement>(null),ready=useRef(false),requestExport=useRef<()=>void>(()=>{});
 const[exportError,setExportError]=useState(''),[display,setDisplay]=useState({title:props.title,description:props.description,unit:props.unit??''});
 const[projection,setProjection]=useState<Projection|null>(null),[failed,setFailed]=useState(false),[active,setActive]=useState(false);
 const group=useInteractionGroup(),[,,saved]=useUIState(readonly?'':reference,{});
 const series=props.series??[];
 // Original cells are facts. The owner selects bounded positions, not values.
 const data:ChartData[]=props.data??(props.labels??[]).map((label,i)=>Object.fromEntries([['label',label],['axisX',props.xValues?.[i]],...series.map((s,j)=>['series'+j,s.values[i]])]) as ChartData);
 const current=useRef({group,props,data,display});current.current={group,props,data,display};
 const post=(value:Record<string,unknown>)=>frame.current?.contentWindow?.postMessage({channel:'steward-chart-view',...value},'*');
 useLayoutEffect(()=>{
  if(failed)return;
  let alive=true,restoring=false;
  const captures=new Map<string,()=>void>(),exports=new Map<string,number>(),requests=new AbortController();
  const timeout=window.setTimeout(()=>{if(alive&&!ready.current)setFailed(true)},10000);
  const persist=(p:Projection)=>setUIState(reference,{start:p.start,end:p.end},{title:current.current.display.title,range:[p.start,p.end]});
  const receive=async(event:MessageEvent)=>{
   const value=event.data;
   if(!alive||event.source!==frame.current?.contentWindow||event.origin!=='null'||value?.channel!=='steward-chart-view')return;
   if(value.type==='failed'){setFailed(true);return}
   if(value.type==='ready'&&!restoring){
    if(!record(value.display)||typeof value.display.title!=='string'||[...value.display.title].length>300||typeof value.display.description!=='string'||[...value.display.description].length>2000||typeof value.display.unit!=='string'||[...value.display.unit].length>64){setFailed(true);return}
    setDisplay({title:value.display.title,description:value.display.description,unit:value.display.unit});
    restoring=true;
    if(readonly)return;
    try{
     const view=await getUIState(reference);if(view.error)throw Error('Saved view unavailable.');
     const group=await current.current.group.readState();
     if(alive)post({type:'restore',state:{start:0,end:Math.max(0,current.current.data.length-1),...view.state},query:group.query,selected:group.selected});
    }catch{if(alive)setFailed(true)}
   }
   if(readonly&&!['ready','restored','paint','failed'].includes(value.type))return;
   if(value.type==='export-rows'&&ready.current&&typeof value.id==='string'&&exports.has(value.id)){
    clearTimeout(exports.get(value.id));exports.delete(value.id);
    try{
     if(!Array.isArray(value.rows)||value.rows.length>current.current.data.length||new Set(value.rows).size!==value.rows.length||value.rows.some((i:unknown)=>!Number.isInteger(i)||(i as number)<0||(i as number)>=current.current.data.length))throw Error('Export failed.');
     const original=await readChartExport(reference,value.rows,requests.signal);
     if(alive)download(csv([original.columns,...original.rows]),'text/csv;charset=utf-8',safeName(original.title)+'.csv');
    }catch{if(alive)setExportError('Export failed.')}
    return;
   }
   if(!valid(value.projection,current.current.data.length)&&value.type!=='select')return;
   if(value.type==='restored'&&restoring&&!ready.current){ready.current=true;setActive(!readonly)}
   if(!ready.current)return;
   if(value.type==='select'){
    if(Number.isInteger(value.item)&&value.item>=0&&value.item<current.current.data.length)current.current.group.select(current.current.data[value.item].label);
    return;
   }
   setProjection(value.projection);
   if(value.type==='view-state'){
    if(value.command==='range')persist(value.projection);
    if(typeof value.id==='string'){captures.get(value.id)?.();captures.delete(value.id)}
   }
   if(value.type==='reset'){
    persist(value.projection);const group=current.current.group;group.setQuery('');if(group.selected)group.select(group.selected);
   }
  };
  requestExport.current=()=>{
   if(readonly||!ready.current||exports.size)return;
   setExportError('');const id=randomUUID();exports.set(id,window.setTimeout(()=>{exports.delete(id);if(alive)setExportError('Export failed.')},5000));post({type:'export',id});
  };
  const capture=(event:Event)=>{
   if(!ready.current)return;
   const detail=(event as CustomEvent<{pending:Promise<void>[]} >).detail;
   detail.pending.push(new Promise<void>(resolve=>{const id=randomUUID(),timer=window.setTimeout(()=>{captures.delete(id);resolve()},500);captures.set(id,()=>{clearTimeout(timer);resolve()});post({type:'capture',id})}));
  };
  window.addEventListener('message',receive);!readonly&&window.addEventListener('ui-state-flush',capture);
  return()=>{alive=false;ready.current=false;post({type:'dispose'});requests.abort();requestExport.current=()=>{};for(const timer of exports.values())clearTimeout(timer);exports.clear();clearTimeout(timeout);window.removeEventListener('message',receive);window.removeEventListener('ui-state-flush',capture);for(const resolve of captures.values())resolve();captures.clear()};
 },[reference,failed,readonly,source]);
 useEffect(()=>{if(active&&!readonly)post({type:'group',query:group.query,selected:group.selected})},[active,readonly,group.query,group.selected]);
 if(failed)return <p className="semantic">{fallback}</p>;
 const p=projection;
 return <section inert={readonly} className="openintelligentui-native" data-openintelligentui={name} data-view-ref={reference}><AnswerSurface title={display.title} graphic={!readonly} actions={readonly?[]:[
  {label:'Zoom',icon:<Icon name="zoom"/>,disabled:!active||props.kind==='pie',run:()=>post({type:'zoom'})},
  {label:'Reset',icon:<Icon name="reset"/>,disabled:!active,run:()=>post({type:'reset'})},
  {label:'Download CSV',icon:<Icon name="download"/>,disabled:!active,run:()=>requestExport.current()},
 ]}>
  <iframe hidden ref={frame} title="Chart policy" sandbox="allow-scripts" referrerPolicy="no-referrer" src={source??('/ui/chart?'+new URLSearchParams({ref:reference}))} onError={()=>setFailed(true)}/>
  {p?<ChartDrawing kind={props.kind} title={display.title} description={display.description} shown={p.rows.map(i=>data[i])} series={series} unit={display.unit} horizontal={p.horizontal} numericLine={p.numericLine} height={p.height} selection={p.selected} opacity={p.opacity} onSelect={label=>{if(readonly)return;const item=p.rows.find(i=>data[i].label===label);if(item!==undefined)post({type:'select',item})}}>
   {!readonly&&p.zoom&&p.filteredLength>1&&<div className="answer-chart-range"><label>{t('From')}<input type="range" min="0" max={Math.max(0,p.end)} value={p.start} onChange={event=>post({type:'range',state:{start:Number(event.target.value)}})}/></label><label>{t('To')}<input type="range" min={p.start} max={p.filteredLength-1} value={p.end} onChange={event=>post({type:'range',state:{end:Number(event.target.value)}})}/></label></div>}
  </ChartDrawing>:<p className="semantic">{fallback}</p>}
  {(saved.error||exportError)&&<p className="field-help" role="alert">{t(saved.error||exportError)}</p>}
 </AnswerSurface></section>;
}

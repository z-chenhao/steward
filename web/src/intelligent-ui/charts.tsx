import {t} from '../i18n';
import {useState} from 'react';
import {BarChart,Bar,PieChart,Pie,LineChart,Line,ScatterChart,Scatter,XAxis,YAxis,Tooltip,CartesianGrid,Cell,type YAxisTickContentProps} from 'recharts';
import {CHART_COLORS} from '../components/open-intelligent-ui/charts/config';
import {ChartContainer,ChartTooltipContent,ChartLegend} from '../components/ui/chart';
import {AnswerSurface} from './surface';
import {useInteractionGroup} from './group';
import {useUIState} from './state';
import {csv,download,safeName} from './exports';
import {Icon} from './icons';
export type ChartData={label:string;value?:number;x?:number;y?:number;[key:string]:string|number|undefined};
export function ChartExplorer({kind,title,description,data:inputData,labels,series=[],unit='',reference=''}:{kind:'bar'|'pie'|'line'|'scatter';title:string;description:string;data?:ChartData[];labels?:string[];series?:{name:string;values:number[]}[];unit?:string;reference?:string}){
 const data:ChartData[]=inputData??(labels??[]).map((label,i)=>Object.fromEntries([['label',label],...series.map((s,j)=>['series'+j,s.values[i]])]) as ChartData);
 const group=useInteractionGroup();const[zoom,setZoom]=useState(false);const[state,update,saved]=useUIState(reference,{start:0,end:Math.max(0,data.length-1)},s=>({title,range:[s.start,s.end]}));
 const filtered=data.filter(d=>!group.query||d.label.toLocaleLowerCase().includes(group.query.toLocaleLowerCase()));const shown=zoom?filtered.slice(state.start,state.end+1):filtered;
 const reduced=matchMedia('(prefers-reduced-motion: reduce)').matches;const choose=(point:unknown)=>{const p=point as {label?:string;payload?:{label?:string}};const label=p?.label??p?.payload?.label;if(label)group.select(label)};
 const horizontal=kind==='bar'&&(shown.length>6||shown.some(d=>d.label.length>16));
 const number=(value:number)=>Intl.NumberFormat(undefined,{notation:'compact',maximumFractionDigits:1}).format(value);
 const measure=document.createElement('canvas').getContext('2d');
 if(measure)measure.font='12px '+getComputedStyle(document.documentElement).getPropertyValue('--font-ui');
 const category=(value:string)=>{
  const fits=(text:string)=>measure?measure.measureText(text).width<=96:Array.from(text).reduce((width,char)=>width+(char.charCodeAt(0)>255?12:7),0)<=96;
  if(fits(value))return value;
  const characters=Array.from(value);let left=Math.ceil(characters.length/2),right=left;
  while(left+characters.length-right>2){if(left>characters.length-right)left--;else right++;const text=characters.slice(0,left).join('')+'…'+characters.slice(right).join('');if(fits(text))return text}
  return characters[0]+'…'+characters[characters.length-1];
 };
 const tick={fontSize:12,fill:'var(--muted-foreground)'};
 const categoryTick=({x,y,payload}:YAxisTickContentProps)=><text data-axis-category x={Number(x)} y={y} textAnchor="end" dominantBaseline="middle" style={tick}><title>{payload.value}</title>{category(String(payload.value))}</text>;
 const common={data:shown,margin:{top:12,right:20,bottom:8,left:0}};
 const axes=<><CartesianGrid vertical={horizontal} horizontal={!horizontal} stroke="var(--border)"/>
  <XAxis dataKey={horizontal?undefined:kind==='scatter'?'x':'label'} type={horizontal||kind==='scatter'?'number':'category'} axisLine={false} tickLine={false} tickMargin={10} minTickGap={24} tickFormatter={horizontal||kind==='scatter'?number:category} tick={tick}/>
  <YAxis dataKey={horizontal?'label':kind==='scatter'?'y':undefined} type={horizontal?'category':'number'} axisLine={false} tickLine={false} tickMargin={8} width={horizontal?124:56} tickFormatter={horizontal?category:number} tick={horizontal?categoryTick:tick}/>
  <Tooltip cursor={{fill:'var(--hover)',fillOpacity:.6,stroke:'var(--border)'}} content={<ChartTooltipContent name={title} unit={unit}/>}/></>;
 const dot=(key='value',name=title,color=CHART_COLORS[0])=>(point:unknown)=>{const p=point as {cx?:number;cy?:number;payload?:ChartData};if(p.cx===undefined||p.cy===undefined||!p.payload)return <g/>;const label=p.payload.label;return <g className="recharts-line-dot" data-chart-label={label} role="button" tabIndex={0} aria-label={name+' / '+label+': '+p.payload[key]+(unit?' '+unit:'')} onClick={()=>group.select(label)} onKeyDown={event=>{if(event.key==='Enter'||event.key===' '){event.preventDefault();group.select(label)}}}><circle cx={p.cx} cy={p.cy} r={12} fill="transparent"/><circle cx={p.cx} cy={p.cy} r={3} fill={group.selected===label?color:'var(--muted)'} stroke={color} strokeWidth={1.5}/></g>};
 const colors=shown.map((d,index)=><Cell key={index} fill={CHART_COLORS[kind==='bar'||kind==='scatter'?0:index%CHART_COLORS.length]} fillOpacity={!group.selected||group.selected===d.label?1:.3}/>);
 const exportRows=kind==='scatter'?[['Label','X','Y'],...shown.map(d=>[d.label,d.x,d.y])]:series.length?[['Label',...series.map(s=>s.name+(unit?' ('+unit+')':''))],...shown.map(d=>[d.label,...series.map((_,i)=>d['series'+i])])]:[['Label','Value'],...shown.map(d=>[d.label,d.value])];
 return <AnswerSurface title={title} graphic actions={[{label:'Zoom',icon:<Icon name="zoom"/>,disabled:kind==='pie',run:()=>setZoom(!zoom)},{label:'Reset',icon:<Icon name="reset"/>,run:()=>{setZoom(false);update({start:0,end:Math.max(0,data.length-1)});group.setQuery('');if(group.selected)group.select(group.selected)}},{label:'Download CSV',run:()=>download(csv(exportRows),'text/csv;charset=utf-8',safeName(title)+'.csv')}] }>
  <div className="native-chart" role="group" aria-label={title}><div className="native-answer-heading"><h3 className="native-answer-title">{title}</h3>{description&&<p className="native-answer-description">{description}</p>}{unit&&<span className="text-muted">{unit}</span>}</div>
  {!shown.length?<p className="native-answer-empty">{t('No data available')}</p>:<ChartContainer height={horizontal?Math.max(260,Math.min(640,shown.length*34+40)):260}>
   {kind==='bar'?<BarChart {...common} layout={horizontal?'vertical':'horizontal'}>{axes}<Bar dataKey="value" maxBarSize={40} radius={horizontal?[0,4,4,0]:[4,4,0,0]} isAnimationActive={!reduced} onClick={choose}>{colors}</Bar></BarChart>
    :kind==='pie'?<PieChart><Pie data={shown} dataKey="value" nameKey="label" innerRadius={56} outerRadius={88} paddingAngle={3} cornerRadius={4} stroke="none" isAnimationActive={!reduced} onClick={choose}>{colors}</Pie><Tooltip content={<ChartTooltipContent name={title} unit={unit}/>}/></PieChart>
    :kind==='line'?<LineChart {...common}>{axes}{(series.length?series:[{name:title}]).map((s,i)=>{const key=series.length?'series'+i:'value',color=CHART_COLORS[i%CHART_COLORS.length];return <Line key={key} name={s.name} type="linear" dataKey={key} stroke={color} strokeWidth={2} dot={dot(key,s.name,color)} activeDot={{r:5}} isAnimationActive={!reduced}/>})}</LineChart>
    :<ScatterChart margin={common.margin}>{axes}<Scatter name={title} data={shown} fill={CHART_COLORS[0]} isAnimationActive={!reduced} onClick={choose}>{colors}</Scatter></ScatterChart>}
  </ChartContainer>}
  {zoom&&filtered.length>1&&<div className="answer-chart-range"><label>{t('From')}<input type="range" min="0" max={Math.max(0,state.end)} value={state.start} onChange={event=>update({start:Number(event.target.value)})}/></label><label>{t('To')}<input type="range" min={state.start} max={filtered.length-1} value={state.end} onChange={event=>update({end:Number(event.target.value)})}/></label></div>}
  {kind==='line'&&series.length>0&&<ChartLegend items={series.map((s,i)=>({name:s.name,color:CHART_COLORS[i%CHART_COLORS.length]}))}/>}
  {kind==='pie'&&<div className="native-chart-legend">{shown.map((d,i)=><button type="button" key={i} className="native-chart-legend-item" aria-pressed={group.selected===d.label} onClick={()=>group.select(d.label)}><span className="native-chart-legend-color" style={{backgroundColor:CHART_COLORS[i%CHART_COLORS.length]}}/>{d.label}</button>)}</div>}
  </div>{saved.error&&<p className="field-help" role="alert">{t(saved.error)}</p>}
 </AnswerSurface>
}

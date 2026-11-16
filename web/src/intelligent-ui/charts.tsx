import {t} from '../i18n';
import type {ReactNode} from 'react';
import {BarChart,Bar,PieChart,Pie,LineChart,Line,ScatterChart,Scatter,XAxis,YAxis,Tooltip,CartesianGrid,Cell,type YAxisTickContentProps} from 'recharts';
import {CHART_COLORS} from '../components/open-intelligent-ui/charts/config';
import {ChartContainer,ChartTooltipContent,ChartLegend} from '../components/ui/chart';
export type ChartData={label:string;value?:number;x?:number;y?:number;[key:string]:string|number|undefined};
export type ChartProps={kind:'bar'|'pie'|'line'|'scatter';title:string;description:string;data?:ChartData[];labels?:string[];xValues?:number[];series?:{name:string;values:number[]}[];unit?:string;reference?:string};
// Fixed drawing over accepted original data and a bounded chart projection.
// Filtering, range, orientation and export policy are supplied by the owner.
export function ChartDrawing({kind,title,description,shown,series=[],unit='',horizontal,numericLine,height,selection,opacity,onSelect,children}:Pick<ChartProps,'kind'|'title'|'description'|'series'|'unit'>&{shown:ChartData[];horizontal:boolean;numericLine:boolean;height:number;selection:boolean[];opacity:number[];onSelect:(label:string)=>void;children?:ReactNode}){
 const selectedLabels=new Set(shown.filter((_,i)=>selection[i]).map(point=>point.label));
 const reduced=matchMedia('(prefers-reduced-motion: reduce)').matches;const choose=(point:unknown)=>{const p=point as {label?:string;payload?:{label?:string}};const label=p?.label??p?.payload?.label;if(label)onSelect(label)};
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
  <XAxis dataKey={horizontal?undefined:numericLine?'axisX':kind==='scatter'?'x':'label'} type={horizontal||kind==='scatter'||numericLine?'number':'category'} domain={numericLine?['dataMin','dataMax']:undefined} axisLine={false} tickLine={false} tickMargin={10} minTickGap={24} tickFormatter={horizontal||kind==='scatter'||numericLine?number:category} tick={tick}/>
  <YAxis dataKey={horizontal?'label':kind==='scatter'?'y':undefined} type={horizontal?'category':'number'} axisLine={false} tickLine={false} tickMargin={8} width={horizontal?124:56} tickFormatter={horizontal?category:number} tick={horizontal?categoryTick:tick}/>
  <Tooltip cursor={{fill:'var(--hover)',fillOpacity:.6,stroke:'var(--border)'}} content={<ChartTooltipContent name={title} unit={unit}/>}/></>;
 const dot=(key='value',name=title,color=CHART_COLORS[0])=>(point:unknown)=>{const p=point as {cx?:number;cy?:number;payload?:ChartData};if(p.cx===undefined||p.cy===undefined||!p.payload)return <g/>;const label=p.payload.label;return <g className="recharts-line-dot" data-chart-label={label} role="button" tabIndex={0} aria-label={name+' / '+label+': '+p.payload[key]+(unit?' '+unit:'')} onClick={()=>onSelect(label)} onKeyDown={event=>{if(event.key==='Enter'||event.key===' '){event.preventDefault();onSelect(label)}}}><circle cx={p.cx} cy={p.cy} r={12} fill="transparent"/><circle cx={p.cx} cy={p.cy} r={3} fill={selectedLabels.has(label)?color:'var(--muted)'} stroke={color} strokeWidth={1.5}/></g>};
 const colors=shown.map((d,index)=><Cell key={index} fill={CHART_COLORS[kind==='bar'||kind==='scatter'?0:index%CHART_COLORS.length]} fillOpacity={opacity[index]}/>);
 return <div className="native-chart" role="group" aria-label={title}><div className="native-answer-heading"><h3 className="native-answer-title">{title}</h3>{description&&<p className="native-answer-description">{description}</p>}{unit&&<span className="text-muted">{unit}</span>}</div>
  {!shown.length?<p className="native-answer-empty">{t('No data available')}</p>:<ChartContainer height={height}>
   {kind==='bar'?<BarChart {...common} layout={horizontal?'vertical':'horizontal'}>{axes}<Bar dataKey="value" maxBarSize={40} radius={horizontal?[0,4,4,0]:[4,4,0,0]} isAnimationActive={!reduced} onClick={choose}>{colors}</Bar></BarChart>
    :kind==='pie'?<PieChart><Pie data={shown} dataKey="value" nameKey="label" innerRadius={56} outerRadius={88} paddingAngle={3} cornerRadius={4} stroke="none" isAnimationActive={!reduced} onClick={choose}>{colors}</Pie><Tooltip content={<ChartTooltipContent name={title} unit={unit}/>}/></PieChart>
    :kind==='line'?<LineChart {...common}>{axes}{(series.length?series:[{name:title}]).map((s,i)=>{const key=series.length?'series'+i:'value',color=CHART_COLORS[i%CHART_COLORS.length];return <Line key={key} name={s.name} type="linear" dataKey={key} stroke={color} strokeWidth={2} dot={dot(key,s.name,color)} activeDot={{r:5}} isAnimationActive={!reduced}/>})}</LineChart>
    :<ScatterChart margin={common.margin}>{axes}<Scatter name={title} data={shown} fill={CHART_COLORS[0]} isAnimationActive={!reduced} onClick={choose}>{colors}</Scatter></ScatterChart>}
  </ChartContainer>}
  {children}
  {kind==='line'&&series.length>0&&<ChartLegend items={series.map((s,i)=>({name:s.name,color:CHART_COLORS[i%CHART_COLORS.length]}))}/>}
  {kind==='pie'&&<div className="native-chart-legend">{shown.map((d,i)=><button type="button" key={i} className="native-chart-legend-item" aria-pressed={selectedLabels.has(d.label)} onClick={()=>onSelect(d.label)}><span className="native-chart-legend-color" style={{backgroundColor:CHART_COLORS[i%CHART_COLORS.length]}}/>{d.label}</button>)}</div>}
  </div>;
}
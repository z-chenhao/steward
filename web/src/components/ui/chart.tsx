// Adapted from shadcn/ui @ 8a3536678a528cc012b9e62484290e79d0be410f.
// MIT; see web/licenses/shadcn-ui.txt. Only the used chart container/tooltip parts.
import type {ComponentProps} from 'react';
import {ResponsiveContainer} from 'recharts';

export function ChartContainer({children,height=260}:Pick<ComponentProps<typeof ResponsiveContainer>,'children'>&{height?:number}){
 return <div data-slot="chart" className="answer-chart-container" style={{height}}><ResponsiveContainer width="100%" height="100%" minWidth={0}>{children}</ResponsiveContainer></div>;
}
type Payload={name?:string|number;value?:unknown;color?:string;payload?:{label?:string}};
export function ChartTooltipContent({active,payload,label,name,unit=''}:{active?:boolean;payload?:readonly Payload[];label?:string|number;name?:string;unit?:string}){
 if(!active||!payload?.length)return null;
 const heading=payload[0]?.payload?.label??label;
 return <div className="answer-chart-tooltip">{heading!==undefined&&<div className="answer-tooltip-label">{heading}</div>}{payload.map((item,index)=>typeof item.value==='number'||typeof item.value==='string'?<div className="answer-tooltip-row" key={index}><span className="answer-tooltip-indicator" style={{backgroundColor:item.color}}/><span>{payload.length===1?name??item.name:item.name}</span><strong>{typeof item.value==='number'?item.value.toLocaleString():item.value}{unit?' '+unit:''}</strong></div>:null)}</div>;
}

export function ChartLegend({items}:{items:{name:string;color:string}[]}){
 return <div className="native-chart-legend" aria-label="Series">{items.map(item=><span className="native-chart-legend-item" key={item.name}><span className="native-chart-legend-color" style={{backgroundColor:item.color}}/>{item.name}</span>)}</div>;
}

import {z} from 'zod';
import {DataTable,TableProps} from '../data-table';
import {BarChart,BarChartProps} from './charts/bar-chart';
import {PieChart,PieChartProps} from './charts/pie-chart';
import {PlanCard} from './plan-card';
import {MeetingTimePicker} from './meeting-time-picker';
import {lineChartSchema} from '../../intelligent-ui/line-schema';
import {ChartExplorer} from '../../intelligent-ui/charts';
import {formSchema,cardsSchema,actionSchema,InputForm,CardCarousel,ToolActionButton} from '../../intelligent-ui/extensions';

const chart=BarChartProps.extend({title:z.string().max(300),description:z.string().max(2000),data:z.array(z.object({label:z.string().max(200),value:z.number().finite().min(-1e15).max(1e15)})).max(200)});
const plan=z.object({approach:z.string().max(8192),technology:z.string().max(300),key_elements:z.array(z.string().max(1000)).max(32)}).strict();
const meeting=z.object({reasonForScheduling:z.string().min(1).max(300),meetingDuration:z.number().positive().max(1440),title:z.string().max(300).optional(),timeSlots:z.array(z.object({date:z.string().min(1).max(200),time:z.string().min(1).max(200),duration:z.string().max(100).optional()}).strict()).min(1).max(16)}).strict();
export const nativeDefinition=z.discriminatedUnion('name',[
 z.object({name:z.literal('Table'),props:TableProps.strict().refine(p=>p.rows.every(row=>row.length===p.columns.length))}),
 z.object({name:z.literal('barChart'),props:chart.strict()}),
 z.object({name:z.literal('pieChart'),props:PieChartProps.merge(chart).strict().refine(p=>p.data.every(d=>d.value>=0)&&(!p.data.length||p.data.some(d=>d.value>0)))}),
 z.object({name:z.literal('plan_visualization'),props:plan}),
 z.object({name:z.literal('scheduleTime'),props:meeting}),
 z.object({name:z.literal('steward.lineChart'),props:lineChartSchema}),
 z.object({name:z.literal('steward.scatterChart'),props:z.object({title:z.string().max(300),description:z.string().max(2000),data:z.array(z.object({label:z.string().max(200),x:z.number().finite(),y:z.number().finite()}).strict()).max(200)}).strict()}),
 z.object({name:z.literal('steward.form'),props:formSchema}),
 z.object({name:z.literal('steward.cards'),props:cardsSchema}),
 z.object({name:z.literal('steward.action'),props:actionSchema}),
]);
export type NativeProjection={name:string;props:unknown;widget_ref?:string;form_id?:string;message_id?:string;submitted?:boolean;disabled?:boolean;response?:string};

export function OpenIntelligentAnswer({value,fallback}:{value:NativeProjection;fallback:string}){
 const result=nativeDefinition.safeParse(value);
 if(!result.success)return <p className="semantic">{fallback}</p>;
 const c=result.data;
 async function respondFields(values:Record<string,string>){
 if(!value.message_id||!value.form_id||value.submitted||value.disabled)throw Error('Selection unavailable');const body=new URLSearchParams({message_id:value.message_id,form_id:value.form_id});for(const [name,v] of Object.entries(values))body.set('field.'+name,v);const r=await fetch('/ui/form',{method:'POST',body,credentials:'same-origin',headers:{Accept:'application/json'}});if(!r.ok||(await r.json()).accepted!==true)throw Error('Response not confirmed');
 }
 async function respond(response:string){
  if(c.name!=='scheduleTime'||!value.message_id||!value.form_id||value.submitted||value.disabled)throw Error('Selection unavailable');
  const i=c.props.timeSlots.findIndex(slot=>response===`Meeting scheduled for ${slot.date} at ${slot.time} (${c.props.meetingDuration} min).`);
  if(i<0&&response!=='The user declined all proposed meeting times. Please suggest alternative times or ask for their availability.')throw Error('Unknown selection');
  const body=new URLSearchParams({message_id:value.message_id,form_id:value.form_id,'field.choice':i<0?'decline':String(i)});
  const controller=new AbortController(),timer=setTimeout(()=>controller.abort(),10000);
  try{const r=await fetch('/ui/form',{method:'POST',body,signal:controller.signal,credentials:'same-origin',headers:{Accept:'application/json'}});if(!r.ok||(await r.json()).accepted!==true)throw Error('Selection not confirmed')}finally{clearTimeout(timer)}
 }
 const reference=value.widget_ref??'';
 return <section className="openintelligentui-native" data-openintelligentui={c.name} data-form-id={value.form_id} data-response={value.response}>
  {c.name==='Table'?<DataTable props={c.props} reference={reference}/>:c.name==='barChart'?<BarChart {...c.props} reference={reference}/>:c.name==='pieChart'?<PieChart {...c.props} reference={reference}/>:c.name==='steward.lineChart'?<ChartExplorer kind="line" {...c.props} reference={reference}/>:c.name==='steward.scatterChart'?<ChartExplorer kind="scatter" {...c.props} reference={reference}/>:c.name==='steward.form'?<InputForm props={c.props} reference={reference} disabled={value.disabled} submitted={value.submitted} response={value.response} respond={respondFields}/>:c.name==='steward.cards'?<CardCarousel props={c.props} reference={reference}/>:c.name==='steward.action'?<ToolActionButton props={c.props} reference={reference}/>:c.name==='plan_visualization'?<PlanCard status="complete" approach={c.props.approach} technology={c.props.technology} keyElements={c.props.key_elements}/>:<MeetingTimePicker {...c.props} status="executing" response={value.response} disabled={value.disabled||value.submitted} respond={respond}/>}
 </section>;
}

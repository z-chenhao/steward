import {LegacyToolActionButton} from '../../intelligent-ui/legacy/action-button-v1';
import {formSchema} from '../../intelligent-ui/form-schema';
import {LegacyInputForm} from '../../intelligent-ui/legacy/input-form-v1';
import {LegacyCardCarousel} from '../../intelligent-ui/legacy/card-carousel-v1';
import {z} from 'zod';
import {LegacyDataTable} from '../../intelligent-ui/legacy/table-v1';
import {tableSchema} from '../../intelligent-ui/table-schema';
import {BarChart,BarChartProps} from './charts/bar-chart';
import {PieChart,PieChartProps} from './charts/pie-chart';
import {LegacyPlanCard} from '../../intelligent-ui/legacy/plan-card-v1';
import {LegacyMeetingTimePicker} from '../../intelligent-ui/legacy/meeting-time-picker-v1';
import {lineChartSchema} from '../../intelligent-ui/line-schema';
import {LegacyChartExplorer} from '../../intelligent-ui/legacy/chart-explorer-v1';
import {cardsSchema,actionSchema} from '../../intelligent-ui/extensions';

const chart=BarChartProps.extend({title:z.string().max(300),description:z.string().max(2000),data:z.array(z.object({label:z.string().max(200),value:z.number().finite().min(-1e15).max(1e15)})).max(200)});
const plan=z.object({approach:z.string().max(8192),technology:z.string().max(300),key_elements:z.array(z.string().max(1000)).max(32)}).strict();
const meeting=z.object({reasonForScheduling:z.string().min(1).max(300),meetingDuration:z.number().positive().max(1440),title:z.string().max(300).optional(),timeSlots:z.array(z.object({date:z.string().min(1).max(200),time:z.string().min(1).max(200),duration:z.string().max(100).optional()}).strict()).min(1).max(16)}).strict();
export const nativeDefinition=z.discriminatedUnion('name',[
 z.object({name:z.literal('Table'),props:tableSchema.strict().refine(p=>p.rows.every(row=>row.length===p.columns.length))}),
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
export type NativeProjection={name:string;props:unknown;widget_ref?:string;view_ref?:string;meeting_view?:boolean;meeting_display?:unknown;action_view?:boolean;action_display?:unknown;form_id?:string;message_id?:string;submitted?:boolean;disabled?:boolean;response?:string};

// Private consumer port: only Host binds message/form identities and credentials.
export type SubmitFields=(values:Record<string,string>,signal?:AbortSignal)=>Promise<void>;
export function OpenIntelligentAnswer({value,fallback,submitFields}:{value:NativeProjection;fallback:string;submitFields?:SubmitFields}){
 const result=nativeDefinition.safeParse(value);
 if(!result.success)return <p className="semantic">{fallback}</p>;
 const c=result.data;
 async function respondFields(values:Record<string,string>){
  if(!submitFields||value.submitted||value.disabled)throw Error('Selection unavailable');
  await submitFields(values);
 }
 async function respond(response:string){
  if(c.name!=='scheduleTime'||!submitFields||value.submitted||value.disabled)throw Error('Selection unavailable');
  const i=c.props.timeSlots.findIndex(slot=>response===`Meeting scheduled for ${slot.date} at ${slot.time} (${c.props.meetingDuration} min).`);
  if(i<0&&response!=='The user declined all proposed meeting times. Please suggest alternative times or ask for their availability.')throw Error('Unknown selection');
  const controller=new AbortController(),timer=setTimeout(()=>controller.abort(),10000);
  try{await submitFields({choice:i<0?'decline':String(i)},controller.signal)}finally{clearTimeout(timer)}
 }
 const reference=value.widget_ref??'';
 return <section className="openintelligentui-native" data-openintelligentui={c.name} data-form-id={value.form_id} data-response={value.response}>
  {c.name==='Table'?<LegacyDataTable props={c.props} reference={reference}/>:c.name==='barChart'?<BarChart {...c.props} reference={reference}/>:c.name==='pieChart'?<PieChart {...c.props} reference={reference}/>:c.name==='steward.lineChart'?<LegacyChartExplorer kind="line" {...c.props} reference={reference}/>:c.name==='steward.scatterChart'?<LegacyChartExplorer kind="scatter" {...c.props} reference={reference}/>:c.name==='steward.form'?<LegacyInputForm props={c.props} reference={reference} disabled={value.disabled} submitted={value.submitted} response={value.response} respond={respondFields}/>:c.name==='steward.cards'?<LegacyCardCarousel props={c.props} reference={reference}/>:c.name==='steward.action'?<LegacyToolActionButton label={c.props.label} reference={reference}/>:c.name==='plan_visualization'?<LegacyPlanCard status="complete" approach={c.props.approach} technology={c.props.technology} keyElements={c.props.key_elements}/>:<LegacyMeetingTimePicker {...c.props} status="executing" response={value.response} disabled={value.disabled||value.submitted} respond={respond}/>}
 </section>;
}

import {actionViewSchema} from './intelligent-ui/action-view';
import {ActionControls} from './intelligent-ui/action-controls';
import {actionSchema} from './intelligent-ui/extensions';
import {MeetingControls,meetingViewSchema} from './intelligent-ui/meeting-view';
import {FrozenFormView} from './intelligent-ui/form-view';
import {FrozenChartView} from './intelligent-ui/chart-view';
import {t} from './i18n';
import {Markdown} from './markdown';
import {IntelligentWidget} from './widgets';
import {FrozenPlanView} from './intelligent-ui/plan-view';
import {FrozenCardsView} from './intelligent-ui/cards-view';
import {FrozenTableView} from './intelligent-ui/table-view';
import {Component,type ReactNode} from 'react';
import {flushSync} from 'react-dom';
import {createRoot,type Root} from 'react-dom/client';
import {z} from 'zod';
import {Button} from './components/ui/button';
import {Card, CardContent} from './components/ui/card';
import {InteractionGroup,type ToolAction} from './intelligent-ui/group';
import {OpenIntelligentAnswer,type NativeProjection,type SubmitFields} from './components/open-intelligent-ui/catalog';

// Host-owned native components and opaque-origin UI artifacts share the
// conversation renderer; no model provider or credentials run in the browser.
const text=z.string().max(128*1024);
const component=z.discriminatedUnion('type',[
  z.object({type:z.literal('widget'),title:z.string().max(200),text:text,props:z.object({initialHeight:z.number().min(50).max(4000)}).optional(),widget_ref:z.string().regex(/^ui:[^/\\\\\s\0]+$/)}),
  z.object({type:z.literal('markdown'),text:text.default('')}),
  z.object({type:z.literal('artifact'),text:text.default(''),artifact_ref:z.string().regex(/^artifact:[^/\\\s\0]*$/)}),
  z.object({type:z.literal('openintelligentui'),name:z.string(),props:z.unknown(),widget_ref:z.string().optional(),view_ref:z.string().optional(),meeting_view:z.boolean().optional(),action_view:z.boolean().optional(),text:text.optional(),message_id:z.string().optional(),form_id:z.string().optional(),submitted:z.boolean().optional(),disabled:z.boolean().optional(),response:z.string().optional()}),
  z.object({type:z.enum(['table','progress','form'])}),
  z.object({type:z.literal('confirmation'),text:text.default(''),approval_id:z.string().min(1),frozen_arguments_hash:z.string().length(64)}),

]);
const projection=z.object({action_views:z.record(z.string().startsWith('ui:').max(256),actionViewSchema).optional(),schema_version:z.literal(1),fallback:z.string(),components:z.array(component).max(128),meeting_views:z.record(z.string().regex(/^[a-f0-9]{64}$/),meetingViewSchema).optional()});
type UIComponent=z.infer<typeof component>&{meeting_display?:unknown;action_display?:unknown};

class RenderBoundary extends Component<{children:ReactNode;fallback:string},{failed:boolean}> {
  state={failed:false};
  static getDerivedStateFromError(){return {failed:true}}
  render(){return this.state.failed?<p className="semantic">{this.props.fallback}</p>:this.props.children}
}

// Host transport closes over the accepted projection. Candidate renderers only
// submit field values; they cannot choose an ingress identity or destination.
function BoundNativeAnswer({value,fallback}:{value:NativeProjection;fallback:string}){
 const submitFields:SubmitFields=async(values,signal)=>{
  if(!value.message_id||!value.form_id||value.submitted||value.disabled)throw Error('Selection unavailable');
  const body=new URLSearchParams({message_id:value.message_id,form_id:value.form_id});
  for(const [name,v] of Object.entries(values))body.set('field.'+name,v);
  const result=await fetch('/ui/form',{method:'POST',body,signal,credentials:'same-origin',headers:{Accept:'application/json'}});
  if(!result.ok||(await result.json()).accepted!==true)throw Error(value.name==='scheduleTime'?'Selection not confirmed':'Response not confirmed');
 };
 if(value.name==='steward.action'&&value.action_view){
  const view=actionViewSchema.safeParse(value.action_display),props=actionSchema.safeParse(value.props);
  if(!view.success||!props.success||!value.widget_ref)return <p className="semantic">{fallback}</p>;
  return <section className="openintelligentui-native" data-openintelligentui="steward.action"><ActionControls label={props.data.label} reference={value.widget_ref} view={view.data}/></section>;
 }
 if(value.name==='scheduleTime'&&value.meeting_view){
  const display=meetingViewSchema.safeParse(value.meeting_display);
  if(!display.success)return <p className="semantic">{fallback}</p>;
  const submit=async(choice:string)=>{const controller=new AbortController(),timer=setTimeout(()=>controller.abort(),10000);try{await submitFields({choice},controller.signal)}finally{clearTimeout(timer)}};
  return <section className="openintelligentui-native" data-openintelligentui="scheduleTime" data-form-id={value.form_id} data-response={value.response}><MeetingControls view={display.data} response={value.response} disabled={value.disabled||value.submitted} submit={submit}/></section>;
 }
 if(value.name==='steward.form'&&value.view_ref){
  if(!value.widget_ref||value.widget_ref!==value.view_ref)return <p className="semantic">{fallback}</p>;
  return <section className="openintelligentui-native" data-openintelligentui="steward.form" data-form-id={value.form_id} data-response={value.response}><FrozenFormView reference={value.view_ref} disabled={value.disabled} submitted={value.submitted} response={value.response} respond={values=>submitFields(values)} fallback={fallback}/></section>;
 }
 return <OpenIntelligentAnswer value={value} fallback={fallback} submitFields={submitFields}/>;
}

function NativeComponent({value:c,fallback}:{value:UIComponent;fallback:string}) {
  switch(c.type){
    case 'widget':return !c.widget_ref.includes('#')?<p className="semantic">{c.text}</p>:<IntelligentWidget initialHeight={c.props?.initialHeight} title={c.title} reference={c.widget_ref} fallback={c.text}/>;
    case 'markdown':return <Markdown text={c.text}/>;
    case 'artifact':return <Card className="artifact-card"><CardContent><a className="artifact" href={'/artifact?ref='+encodeURIComponent(c.artifact_ref)}>↓ {c.text} {t('attachment')}</a></CardContent></Card>;
    case 'openintelligentui':return ['barChart','pieChart','steward.lineChart','steward.scatterChart'].includes(c.name||'')&&c.view_ref?<FrozenChartView name={c.name||''} reference={c.view_ref} props={c.props} fallback={c.text||fallback}/>:c.name==='plan_visualization'&&c.view_ref?<FrozenPlanView reference={c.view_ref} fallback={c.text||fallback}/>:c.name==='steward.cards'&&c.view_ref?<FrozenCardsView reference={c.view_ref} props={c.props} fallback={c.text||fallback}/>:c.name==='Table'&&c.view_ref?<FrozenTableView reference={c.view_ref} props={c.props} fallback={c.text||fallback}/>:<BoundNativeAnswer value={c} fallback={fallback}/>;
    case 'table':case 'progress':case 'form':return <p className="semantic">{fallback}</p>;
    case 'confirmation':return <Card className="approval"><CardContent><p className="semantic">{c.text}</p><form action="/approval" method="post"><input type="hidden" name="approval_id" value={c.approval_id}/><input type="hidden" name="hash" value={c.frozen_arguments_hash}/><Button name="decision" value="approved">{t('Approve')}</Button><Button variant="secondary" name="decision" value="denied">{t('Decline')}</Button></form></CardContent></Card>;

  }
}

const roots=new Map<HTMLElement,Root>();
const renderedSpecs=new WeakMap<HTMLElement,string>();
export function retainProjectionNodes(previous:Element,incoming:Element):boolean{
 const before=Array.from(previous.querySelectorAll<HTMLElement>('[data-ui-spec]')),after=Array.from(incoming.querySelectorAll<HTMLElement>('[data-ui-spec]'));
 if(!before.length||before.length!==after.length||before.some(node=>!roots.has(node)))return false;
 // Same immutable message: only Host response metadata can change. Keep the
 // roots in the document; moving an iframe would destroy its browsing context.
 after.forEach((node,i)=>{before[i].dataset.uiSpec=node.dataset.uiSpec;before[i].dataset.uiFallback=node.dataset.uiFallback});
 return true;
}

export function enhanceProjections(scope:ParentNode=document){

for(const node of scope.querySelectorAll<HTMLElement>('[data-ui-spec]')){
  if(roots.has(node)&&renderedSpecs.get(node)===node.dataset.uiSpec)continue;
  try{
    const result=projection.safeParse(JSON.parse(node.dataset.uiSpec||''));
    // Retain the server's escaped fallback for unknown or unsupported specs.
    if(!result.success)continue;
    const spec=result.data;
    const fallback=node.dataset.uiFallback;
    const legacy=spec.components.some(c=>c.type==='table'||c.type==='progress'||c.type==='form');
    if(fallback===undefined)continue;
    if(legacy&&node.querySelector('form[action="/ui/form"]'))continue;
    const actions:ToolAction[]=spec.components.flatMap(c=>{if(c.type!=='openintelligentui'||c.name!=='steward.action'||!c.widget_ref)return [];const p=c.props as {id:string;label:string;tool:string;arguments:Record<string,unknown>};return [{reference:c.widget_ref,...p}]});
    const groupRef=spec.components.flatMap(c=>(c.type==='widget'||c.type==='openintelligentui')&&c.widget_ref?[c.widget_ref]:[])[0]??'';
    const root=roots.get(node)??createRoot(node);roots.set(node,root);renderedSpecs.set(node,node.dataset.uiSpec??'');
    flushSync(()=>root.render(<RenderBoundary fallback={fallback}><InteractionGroup reference={groupRef} actions={actions}>{legacy?<Markdown text={fallback}/>:spec.components.length?spec.components.map((c,i)=><RenderBoundary key={i} fallback={fallback}><NativeComponent value={c.type==='openintelligentui'?{...c,meeting_display:spec.meeting_views?.[c.form_id??''],action_display:spec.action_views?.[c.widget_ref??'']}:c} fallback={fallback}/></RenderBoundary>):<p className="semantic">{fallback}</p>}</InteractionGroup></RenderBoundary>));
    node.dataset.renderer='native';
  }catch{/* Malformed projection retains the existing semantic HTML. */}
}
}
export function releaseProjections(scope:HTMLElement){
 for(const [node,root] of roots){if(scope===node||scope.contains(node)){root.unmount();roots.delete(node)}}
}

import {type FormProps,type FormView} from './form-schema';
import {t} from '../i18n';
import {useState} from 'react';
import {z} from 'zod';
import {AnswerButton as Button,Field,FieldLabel,Input,Textarea} from '../components/ui/answer-controls';
import {NativeSelect} from '../components/ui/native-select';
import {AnswerSurface} from './surface';
import {Icon} from './icons';
import {useUIState,flushUIStates} from './state';
export const cardsSchema=z.object({title:z.string().max(300).optional(),items:z.array(z.object({title:z.string().min(1).max(300),text:z.string().max(2000).optional(),image_ref:z.string().regex(/^artifact:[A-Za-z0-9_-]+$/).optional(),url:z.string().url().refine(v=>{const u=new URL(v);return u.protocol==='https:'&&!u.username&&!u.password}).optional(),label:z.string().max(100).optional()}).strict()).min(1).max(16)}).strict();
export const actionSchema=z.object({id:z.string().regex(/^[A-Za-z][A-Za-z0-9_]{0,63}$/),label:z.string().min(1).max(100),tool:z.string().max(200),arguments:z.record(z.string(),z.unknown()),bindings:z.array(z.object({argument:z.string().regex(/^[A-Za-z][A-Za-z0-9_]{0,63}$/),component:z.number().int().min(0).max(15),path:z.string().startsWith('/').max(512)}).strict()).max(16).optional()}).strict();
export type FormControlProps={props:FormProps;defaults:Record<string,string>;reference:string;disabled?:boolean;submitted?:boolean;response?:string;respond:(values:Record<string,string>)=>Promise<void>};
// Fixed shared input/CAS/submit driver; defaults and display come from its owner.
export function NativeFormControls({props,defaults,reference,disabled,submitted,response,respond}:FormControlProps){
 const [state,update,saved]=useUIState(reference,{values:defaults},s=>({title:props.title,fields:s.values}));const[busy,setBusy]=useState(false),[sent,setSent]=useState(false),[error,setError]=useState('');
 let values=state.values;if(submitted&&response){try{values=JSON.parse(response)}catch{}}
 const set=(name:string,value:string)=>update({values:{...state.values,[name]:value}});
 async function send(event:React.SubmitEvent<HTMLFormElement>){event.preventDefault();if(busy||submitted||sent)return;setBusy(true);setError('');try{await flushUIStates().catch(()=>{});await respond(Object.fromEntries(props.fields.map(f=>[f.name,values[f.name]??defaults[f.name]])));setSent(true)}catch{setError('Response not confirmed. Retry.')}finally{setBusy(false)}}
 return <FormDrawing props={props} values={{...defaults,...values}} reference={reference} disabled={Boolean(disabled||submitted||sent||busy||!saved.ready)} busy={busy} sent={Boolean(submitted||sent)} change={set} submit={send} error={error||saved.error}/>;
}

// Static preview has no saved-state hook, ingress identity or submit authority.
export function PreviewFormView({view,id}:{view:FormView;id:string}){
 return <FormDrawing props={view.props} values={view.defaults} reference={id} disabled busy={false} sent={false} change={()=>{}} submit={event=>event.preventDefault()} error=""/>;
}

type FormDrawingProps={props:FormProps;values:Record<string,string>;reference:string;disabled:boolean;busy:boolean;sent:boolean;change:(name:string,value:string)=>void;submit:(event:React.SubmitEvent<HTMLFormElement>)=>void;error:string};
function FormDrawing({props,values,reference,disabled,busy,sent,change,submit,error}:FormDrawingProps){
 return <AnswerSurface title={props.title}><form className="native-input-form" onSubmit={submit}><h3 className="native-answer-title">{props.title}</h3><fieldset disabled={disabled}>{props.fields.map(f=>{
  const value=values[f.name]??'',id=reference+'-'+f.name;const common={id,name:f.name,required:f.required,'aria-label':f.label};
  return <Field key={f.name} data-type={f.type}>{f.type==='boolean'?<label className="native-form-choice"><input type="checkbox" checked={value==='true'} required={f.required} onChange={e=>change(f.name,String(e.target.checked))}/>{f.label}</label>
   :f.type==='multiselect'?<fieldset className="native-form-options"><legend>{f.label}</legend>{f.options?.map(o=>{let selected:string[]=[];try{selected=JSON.parse(value)}catch{};return <label className="native-form-choice" key={o.value}><input type="checkbox" name={f.name} checked={selected.includes(o.value)} onChange={event=>change(f.name,JSON.stringify(event.target.checked?[...selected,o.value]:selected.filter(v=>v!==o.value)))}/>{o.label}</label>})}</fieldset>
   :<><FieldLabel htmlFor={id}>{f.label}</FieldLabel>{f.type==='select'?<NativeSelect {...common} value={value} onChange={e=>change(f.name,e.target.value)}><option value=""></option>{f.options?.map(o=><option key={o.value} value={o.value}>{o.label}</option>)}</NativeSelect>
    :f.type==='textarea'?<Textarea {...common} rows={3} maxLength={4096} value={value} onChange={e=>change(f.name,e.target.value)}/>
    :<Input {...common} type={f.type} step={f.type==='number'?'any':undefined} min={f.min} max={f.max} maxLength={4096} value={value} onChange={e=>change(f.name,e.target.value)}/>}</>}
   </Field>})}<Button type="submit">{busy?<span className="typing-dots" aria-label={t('Sending')}><i/><i/><i/></span>:sent?<><Icon name="check"/>{t('Sent')}</>:props.submitLabel||t('Send')}</Button></fieldset></form>{error&&<p className="field-help" role="alert">{t(error)}</p>}</AnswerSurface>
}

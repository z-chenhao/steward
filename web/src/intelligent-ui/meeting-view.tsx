// Native drawing adapted from OpenIntelligentUI; retained attribution and MIT
// notice: web/licenses/OpenIntelligentUI.txt. Display policy belongs to the plugin.
import {useState} from 'react';
import {z} from 'zod';

const label=z.string().min(1).max(300);
export const meetingViewSchema=z.object({component:z.number().int().min(0).max(15),title:label,slots:z.array(z.object({choice:z.number().int().min(0).max(15),date:z.string().min(1).max(200),time:z.string().min(1).max(200),duration:z.string().max(100)}).strict()).min(1).max(16),decline_label:label,selected_title:label,declined_title:label,saving_label:label,select_error:label,decline_error:label,saved_label:label}).strict().refine(v=>new Set(v.slots.map(slot=>slot.choice)).size===v.slots.length);
type MeetingView=z.infer<typeof meetingViewSchema>;
type Choice=number|'decline'|null;

export function MeetingControls({view,response,disabled,submit}:{view:MeetingView;response?:string;disabled?:boolean;submit:(choice:string)=>Promise<void>}){
 const[local,setLocal]=useState<Choice>(null),[busy,setBusy]=useState(false),[error,setError]=useState('');
 const choice:Choice=response==='decline'?'decline':response!==undefined&&/^(?:[0-9]|1[0-5])$/.test(response)?Number(response):local;
 async function choose(value:Exclude<Choice,null>){
  if(busy||disabled)return;setBusy(true);setError('');
  try{await submit(String(value));setLocal(value)}catch{setError(value==='decline'?view.decline_error:view.select_error)}finally{setBusy(false)}
 }
 return <MeetingDrawing view={view} choice={choice} disabled={Boolean(disabled||busy)} busy={busy} error={error} choose={value=>void choose(value)}/>;
}

// Read-only preview renders the same controls without state or submission hooks.
export function PreviewMeetingView({view}:{view:MeetingView}){return <MeetingDrawing view={view} choice={null} disabled busy={false} error="" choose={()=>{}}/>}
function MeetingDrawing({view,choice,disabled,busy,error,choose}:{view:MeetingView;choice:Choice;disabled:boolean;busy:boolean;error:string;choose:(value:Exclude<Choice,null>)=>void}){
 const selected=typeof choice==='number'?view.slots.find(slot=>slot.choice===choice):undefined;
 return <section className="native-time-picker">
  {selected?<div className="native-time-confirmation"><h3 className="native-answer-title">{view.selected_title}</h3><p>{selected.date} at {selected.time}{selected.duration&&<span> · {selected.duration}</span>}</p></div>
   :choice==='decline'?<div className="native-time-confirmation"><h3 className="native-answer-title">{view.declined_title}</h3></div>
   :<><div className="native-answer-heading"><h3 className="native-answer-title">{view.title}</h3></div><fieldset disabled={disabled} className="native-time-options">
    {view.slots.map(slot=><button type="button" key={slot.choice} className="native-time-option" onClick={()=>choose(slot.choice)}><span className="native-time-label"><strong>{slot.date}</strong><span>{slot.time}</span></span>{slot.duration&&<span className="native-time-duration">{slot.duration}</span>}</button>)}
    <button type="button" className="native-time-decline" onClick={()=>choose('decline')}>{view.decline_label}</button>
   </fieldset></>}
  {busy&&<p className="native-answer-description" role="status">{view.saving_label}</p>}{error&&<p className="field-help" role="alert">{error}</p>}
 </section>;
}

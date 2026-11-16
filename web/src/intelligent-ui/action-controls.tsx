import {useEffect,useRef,useState} from 'react';
import {t} from '../i18n';
import {randomUUID} from '../lib/random-id';
import {AnswerButton as Button} from '../components/ui/answer-controls';
import {Icon} from './icons';
import {useUIState} from './state';
import {startAction,actionResult,watchAction,completeAction,type ActionReceipt} from './actions';
import {type ActionView} from './action-view';

// Fixed trusted-gesture/idempotency/CAS/receipt driver; display is owning data.
export function ActionControls({label,reference,view}:{label:string;reference:string;view:ActionView}){
 const[state,update,saved]=useUIState(reference,{source:''});const[status,setStatus]=useState(''),[busy,setBusy]=useState(false),[error,setError]=useState(''),[attempt,setAttempt]=useState(0),[proposed,setProposed]=useState(false);const active=useRef(false),button=useRef<HTMLButtonElement>(null);
 const observed=useRef<{source:string;receipt:ActionReceipt}|null>(null);
 useEffect(()=>watchAction(reference,()=>{setProposed(true);button.current?.focus()}),[reference]);
 useEffect(()=>{
  if(!saved.ready||!state.source)return;
  let alive=true,timer=0;
  const poll=async()=>{
   try{
    const receipt=await actionResult(reference,state.source);if(!alive)return;
    // A durable receipt wins over the original HTTP acknowledgement, even
    // when that older request rejects after polling has already succeeded.
    observed.current={source:state.source,receipt};setError('');setStatus(receipt.status);completeAction(reference,receipt);
    if(['pending','waiting'].includes(receipt.status)){setBusy(true);timer=window.setTimeout(poll,700)}
    else{setBusy(false);if(receipt.result?.error)setError(receipt.status==='unknown'?view.unknown_error:receipt.status==='cancelled'?view.cancelled_error:receipt.result.error.code==='ACTION_NOT_EXECUTED'?view.not_executed_error:view.failed_error)}
   }catch{
    if(!alive)return;
    const known=observed.current;
    if(known?.source===state.source&&!['pending','waiting'].includes(known.receipt.status))return;
    if(active.current){timer=window.setTimeout(poll,700)}else{setBusy(false);setError(view.unconfirmed_error)}
   }
  };
  void poll();return()=>{alive=false;clearTimeout(timer)};
 },[reference,state.source,saved.ready,attempt]);
 async function run(){
  if(active.current||busy)return;
  active.current=true;setProposed(false);setBusy(true);setError('');
  const source=state.source&&status!=='succeeded'&&status!=='failed'?state.source:randomUUID();update({source});
  try{await startAction(reference,source);if(observed.current?.source!==source)setStatus('pending')}
  catch{if(observed.current?.source!==source){setBusy(false);setError(view.unconfirmed_error)}}
  finally{active.current=false;setAttempt(n=>n+1)}
 }
 return <section className="native-tool-action"><Button ref={button} type="button" variant="secondary" data-proposed={proposed} disabled={busy||!saved.ready} onClick={event=>{if(event.nativeEvent.isTrusted)void run()}}>{busy?<span className="typing-dots" aria-label={t(view.working_label)}><i/><i/><i/></span>:status==='succeeded'&&view.success_indicator&&!proposed?<Icon name="check"/>:null}{label}</Button>{(error||saved.error)&&<p role="alert" className="field-help">{t(error||saved.error)}</p>}</section>
}

import {t} from './i18n';
import {createChatScroll} from './chat-scroll';
import {z} from 'zod';
import {enhanceProjections,releaseProjections,retainProjectionNodes} from './projections';
import {captureRichFormDrafts} from './rich-forms';
import {renderDraftMarkdown} from './markdown';
import {reconcileOutgoing} from './outgoing';
import {createUIPreview} from './intelligent-ui/preview';

const snapshot=z.object({view:z.string(),before:z.number().int().nonnegative(),latest:z.number().int().nonnegative(),regions:z.record(z.string(),z.string())});
const replyFrame=z.object({key:z.string().regex(/^[a-f0-9]{32}$/),state:z.enum(['draft','discarded','committed']),text:z.string().optional(),reset:z.boolean().optional()});
function viewRegions(view:string):string[]|null {
 if(view==='/')return ['workspace-status','conversation-attention','conversation-messages','conversation-approvals'];
 if(view==='/tasks/attention')return ['workspace-status','tasks-attention'];
 if(view==='/tasks'||view==='/tasks/configurations')return ['workspace-status','ongoing-work'];
 if(view.startsWith('/tasks/executions/'))return ['workspace-status','execution-header','execution-progress','task-messages','execution-side'];
 if(view.startsWith('/tasks/'))return ['workspace-status','task-header','task-feedback','task-progress','task-decisions','task-messages','task-milestones'];
 if(view.startsWith('/settings/'))return ['workspace-status','signin-status'];
 return null;
}

function keepFeedbackDraft(form:HTMLFormElement,message:string){
 const answer=form.querySelector<HTMLTextAreaElement>('[name=answer]');
 const status=document.getElementById('task-feedback-status');
 if(!answer?.value.trim()||!status||form.dataset.feedbackDraftKept)return;
 form.dataset.feedbackDraftKept='true';
 status.replaceChildren(document.createTextNode(t(message)));
 const details=document.createElement('details'),label=document.createElement('summary'),saved=document.createElement('textarea');
 details.open=true;label.textContent=t('Your saved draft');saved.readOnly=true;saved.rows=3;saved.value=answer.value;saved.setAttribute('aria-label',t('Your saved draft'));
 details.append(label,saved);status.append(details);
}

// Cache Host markup, not React's enhanced DOM. Stable keyed islands retain
// their component state, focus and nodes when unrelated content changes.
function regionPatcher(ids:readonly string[],scope:ParentNode=document){
 const originals=new WeakMap<Element,string>();
 const markup=new Map<string,string>();
 for(const id of ids){
  const region=scope.querySelector<HTMLElement>('#'+id);
  if(region){markup.set(id,region.innerHTML);for(const child of region.children)originals.set(child,child.outerHTML)}
 }
 return (id:string,html:string)=>{
  const region=scope.querySelector<HTMLElement>('#'+id);
  if(!region||markup.get(id)===html)return;
  const parsed=document.createElement('template');parsed.innerHTML=html;
  const restoreDrafts=captureRichFormDrafts(region);
  if(id==='signin-status'&&region.querySelector('[data-signin-pending]')&&parsed.content.querySelector('[data-account-reconnected]')){
   location.reload();
   return;
  }
  if(id==='workspace-status'||id==='signin-status'){
   releaseProjections(region);region.replaceChildren(parsed.content);
   if(id==='signin-status'&&!region.querySelector('[data-signin-pending]'))delete region.dataset.authWindowOpened;
  }else{
   const previous=Array.from(region.children);
   const keyed=new Map(previous.map((child,i)=>[(child as HTMLElement).dataset.liveKey??`position:${i}`,child]));
   const retained=new Set<Element>();
   Array.from(parsed.content.children).forEach((incoming,i)=>{
    const key=(incoming as HTMLElement).dataset.liveKey??`position:${i}`;
    const old=keyed.get(key),raw=incoming.outerHTML;
    let child=old&&originals.get(old)===raw?old:incoming;
    if(id==='execution-side'&&old instanceof HTMLDetailsElement&&incoming instanceof HTMLDetailsElement)incoming.open=old.open;
    if(id==='conversation-messages'&&old&&child!==old&&retainProjectionNodes(old,incoming))child=old;
    if(id==='task-feedback'&&old){
     const native=old.querySelector<HTMLFormElement>('form[action="/task/feedback"]');
     if(native&&incoming.querySelector('form[action="/task/feedback"]')){
      child=old;
      const button=native.querySelector<HTMLButtonElement>('button[type=submit]');
      const next=incoming.querySelector<HTMLButtonElement>('button[type=submit]');
      if(button&&next&&!native.dataset.submitting)button.disabled=next.disabled;
      const hint=native.querySelector<HTMLElement>('[data-feedback-model-needed]'),nextHint=incoming.querySelector<HTMLElement>('[data-feedback-model-needed]');
      if(hint&&nextHint)hint.hidden=nextHint.hidden;
     }
    }
    retained.add(child);originals.set(child,raw);
    if(region.children[i]!==child)region.insertBefore(child,region.children[i]??null);
   });
   for(const child of previous){if(!retained.has(child)){
    if(id==='task-feedback'){
     const form=child.querySelector<HTMLFormElement>('form[action="/task/feedback"]');
     if(form&&!form.dataset.feedbackAccepted)keepFeedbackDraft(form,form.dataset.submitting?'The request changed while your response was being sent. Your draft is kept; check the conversation before retrying.':'This question or review changed. Your draft is kept for reference and has not been submitted.');
    }
    releaseProjections(child as HTMLElement);child.remove()
   }}
  }
  markup.set(id,html);enhanceProjections(region);restoreDrafts();
 };
}

// An execution peek has its own read-only lifetime beside the live task list.
// Reuse the committed region protocol without installing conversation/form handlers.
export function watchExecutionDetail(root:HTMLElement,view:string){
 const regions=viewRegions(view)!.filter(id=>id!=='workspace-status');
 const patch=regionPatcher(regions,root);
 const status=document.createElement('p');status.className='field-help';status.setAttribute('role','status');status.hidden=true;root.append(status);
 let etag='',timer=0,delay=1000,stopped=false,request:AbortController|null=null;
 const pause=()=>{clearTimeout(timer);request?.abort();request=null};
 const refresh=async()=>{
  if(stopped||document.hidden)return;
  pause();const controller=new AbortController();request=controller;
  const timeout=setTimeout(()=>controller.abort(),10000);
  try{
   const response=await fetch('/api/updates?'+new URLSearchParams({view}),{credentials:'same-origin',cache:'no-store',signal:controller.signal,headers:etag?{'If-None-Match':etag}:{}});
   if(stopped||request!==controller)return;
   if(response.status===401||response.status===403||response.status===404){location.assign(view);return}
   if(response.status!==304){
    if(!response.ok)throw new Error('execution updates unavailable');
    const value=snapshot.parse(await response.json());
    if(stopped||request!==controller)return;
    if(value.view!==view||regions.some(id=>typeof value.regions[id]!=='string'))throw new Error('invalid execution view');
    for(const id of regions)patch(id,value.regions[id]);
    etag=response.headers.get('ETag')??'';
   }
   delay=1000;status.hidden=true;
  }catch{
   if(!stopped&&request===controller&&!document.hidden){status.textContent=t('Execution updates are temporarily unavailable. Retrying…');status.hidden=false;delay=Math.min(delay*2,30000)}
  }finally{
   clearTimeout(timeout);
   if(request===controller){request=null;if(!stopped&&!document.hidden)timer=window.setTimeout(refresh,delay)}
  }
 };
 const visibility=()=>{if(document.hidden)pause();else void refresh()};
 document.addEventListener('visibilitychange',visibility);void refresh();
 return ()=>{stopped=true;pause();document.removeEventListener('visibilitychange',visibility);status.remove()};
}

function viewportAnchor(){
 const focused=document.activeElement;
 if(focused instanceof HTMLElement&&focused!==document.body&&!focused.closest('#composer-root,.workspace-panel,.workspace-topbar'))return {node:focused,top:focused.getBoundingClientRect().top};
 const node=Array.from(document.querySelectorAll<HTMLElement>('[data-live-key]')).find(node=>{
  const box=node.getBoundingClientRect();return box.bottom>80&&box.top<innerHeight-200;
 });
 return node?{node,top:node.getBoundingClientRect().top}:null;
}

export function startLiveUpdates(){
 const main=document.querySelector<HTMLElement>('main[data-live-view]');
 if(!main)return null;
 const view=main.dataset.liveView??'';const regionSet=viewRegions(view);if(!regionSet)return null;const regions:readonly string[]=regionSet;
 const taskQuery=new URLSearchParams(location.search);
 const patch=regionPatcher(regions);
 const thread=view==='/'?document.querySelector<HTMLElement>('.thread'):null;
 const reader=thread?createChatScroll(thread,()=>syncJump()):null;
 const atBottom=()=>reader?.isFollowing()??(scrollY+innerHeight>=document.documentElement.scrollHeight-100);
 const scrollToLatest=()=>reader?reader.toBottom():window.scrollTo({top:document.documentElement.scrollHeight,behavior:'instant'});
 const feedback=document.getElementById('live-update-status');
 const jump=document.querySelector<HTMLButtonElement>('#new-messages');
 const draft=document.querySelector<HTMLElement>('#conversation-draft');
 const draftNode=draft?.querySelector<HTMLElement>('[data-draft-text]');
 let draftText='';
 let before=Number(main.dataset.liveBefore??0),latest=Number(main.dataset.liveLatest??0);
 let etag='',timer=0,generation=0,delay=1000,stopped=false,follow=false,pendingTarget='';
 let request:AbortController|null=null;
 let replies:EventSource|null=null,draftKey='',committing=false;
 const uiNode=document.getElementById('conversation-ui-preview');
 const uiPreview=uiNode?createUIPreview(uiNode):null;
 const clearDraft=()=>{draftKey='';committing=false;draftText='';if(draftNode)draftNode.replaceChildren();if(draft)draft.hidden=true};
 const stopReplies=()=>{replies?.close();replies=null;clearDraft();uiPreview?.clear()};
 const hasReply=(key:string)=>!!key&&!!document.querySelector(`[data-reply-key="${key}"]`);
 const startReplies=()=>{
  if(!draft||replies||stopped||before!==0||document.visibilityState!=='visible'||typeof EventSource==='undefined')return;
  const source=new EventSource('/api/conversation/stream');replies=source;
  source.addEventListener('ui-preview',(event:MessageEvent<string>)=>{
   if(source!==replies)return;
   try{const bottom=atBottom();if(uiPreview?.receive(JSON.parse(event.data))==='committed')void refresh();if(bottom)scrollToLatest();else showJump()}catch{/* Invalid preview keeps the current safe display. */}
  });
  source.addEventListener('reply',(event:MessageEvent<string>)=>{
   if(source!==replies)return;
   let data:unknown;
   try{data=JSON.parse(event.data)}catch{return}
   const parsed=replyFrame.safeParse(data);
   if(!parsed.success)return;
   const {key,state,text,reset}=parsed.data;
   if(state==='discarded'){clearDraft();return}
   if(state==='committed'){
    if(draftKey!==key||hasReply(key))clearDraft();else committing=true;
   }else if(reset||draftKey!==key){
    clearDraft();draftKey=key;
   }
   if(text&&draftKey===key&&!hasReply(key)){
    const bottom=atBottom();
    draftText+=text;if(draftNode)renderDraftMarkdown(draftNode,draftText);draft.hidden=false;
    if(bottom)scrollToLatest();else showJump();
   }
   if(state==='committed')void refresh();
  });
  source.onerror=()=>{if(source===replies){clearDraft();uiPreview?.clear();if(source.readyState===EventSource.CLOSED)replies=null}};
 };
 const say=(text:string)=>{if(feedback&&feedback.textContent!==t(text))feedback.textContent=t(text)};
 const accessLost=()=>{
  stopped=true;stopReplies();say('Updates paused. Your draft is kept. ');
  if(!feedback)return;
  const signin=document.createElement('a');signin.href='/';signin.target='_blank';signin.rel='noreferrer';signin.textContent=t('Sign in');
  const retry=document.createElement('button');retry.type='button';retry.textContent=t('Retry updates');
  retry.addEventListener('click',()=>{stopped=false;delay=1000;void refresh()});
  feedback.append(signin,document.createTextNode(' · '),retry);
 };
 const showJump=()=>{if(jump)jump.hidden=false};
 const pause=()=>{generation++;clearTimeout(timer);request?.abort();request=null;stopReplies()};
 const focusApproval=(id:string)=>{
  const target=document.getElementById(id);
  if(!target)return false;
  target.scrollIntoView({block:'center'});target.focus({preventScroll:true});return true;
 };
 const finishFollow=()=>{
  if(!pendingTarget||!focusApproval(pendingTarget))scrollToLatest();
  pendingTarget='';if(jump)jump.hidden=true;follow=false;
 };
 const syncJump=()=>{if(jump&&view==='/')jump.hidden=before===0&&scrollY+innerHeight>=document.documentElement.scrollHeight-100};
 if(view==='/')window.addEventListener('scroll',syncJump,{passive:true});
 document.addEventListener('click',event=>{
  const link=event.target instanceof Element?event.target.closest<HTMLAnchorElement>('[data-pending-jump]'):null;
  if(!link||view!=='/')return;
  event.preventDefault();
  const id=decodeURIComponent(new URL(link.href).hash.slice(1));
  if(focusApproval(id))return;
  if(stopped)return;
  pendingTarget=id;before=0;etag='';follow=true;
  history.replaceState(null,'','/');void refresh();
 });

 async function refresh(){
  if(stopped||document.visibilityState!=='visible')return;
  startReplies();
  clearTimeout(timer);request?.abort();
  const current=++generation,controller=new AbortController();request=controller;
  const timeout=setTimeout(()=>controller.abort(),10000);
  try{
   const query=new URLSearchParams({view});if(before)query.set('before',String(before));
   if(view==='/tasks'||view.startsWith('/tasks/')){for(const key of ['q','cursor','history','feedback_before','needs_me']){const value=taskQuery.get(key);if(value)query.set(key,value)}const taskView=taskQuery.get('view');if(taskView)query.set('task_view',taskView)}
   const response=await fetch('/api/updates?'+query,{credentials:'same-origin',cache:'no-store',signal:controller.signal,headers:etag?{'If-None-Match':etag}:{}});
   if(current!==generation)return;
   if(response.status===401||response.status===403){accessLost();return}
   if(response.status===304){if(follow)finishFollow();delay=1000;say('');return}
   if(!response.ok)throw new Error('updates unavailable');
   const value=snapshot.parse(await response.json());
   if(current!==generation)return;
   if(value.view!==view||value.before!==before||regions.some(id=>typeof value.regions[id]!=='string'))throw new Error('invalid view');
   const bottom=atBottom(),anchor=viewportAnchor();
   let deferMessages=before!==0;
   if(view==='/'&&!bottom&&!follow&&anchor?.node.dataset.liveKey?.startsWith('message:')){
    const parsed=document.createElement('template');parsed.innerHTML=value.regions['conversation-messages'];
    // If the bounded latest page has moved past the reading position, retain
    // that history until the user explicitly jumps to the new messages.
    if(!Array.from(parsed.content.querySelectorAll<HTMLElement>('[data-live-key]')).some(node=>node.dataset.liveKey===anchor.node.dataset.liveKey))deferMessages=true;
   }
   for(const id of regions){if(id!=='conversation-messages'||!deferMessages||follow)patch(id,value.regions[id])}
   reconcileOutgoing();
   uiPreview?.reconcile(deferMessages&&!follow);
   if(hasReply(draftKey)||committing&&deferMessages&&!follow)clearDraft();
   const send=document.querySelector<HTMLButtonElement>('.composer button[type=submit]');
   const form=send?.form;const readiness=document.querySelector<HTMLElement>('#workspace-status [data-can-send]')?.dataset.canSend;
   if(send&&form&&!form.dataset.submitting&&readiness)send.disabled=!!form.dataset.modelSelecting||readiness!=='true';
   delete document.body.dataset.waitingReply;

   if(view==='/'&&(follow||bottom&&before===0))finishFollow();
   else{
    if(anchor?.node.isConnected)window.scrollBy(0,anchor.node.getBoundingClientRect().top-anchor.top);
    if(view==='/'&&(value.latest>latest||before!==0))showJump();
   }
   latest=value.latest;etag=response.headers.get('ETag')??'';delay=1000;say('');
  }catch{
   if(current===generation&&!stopped&&document.visibilityState==='visible'){
    say('Connection lost. Reconnecting… Your draft is kept.');delay=Math.min(delay*2,30000);
   }
  }finally{
   clearTimeout(timeout);
   if(current===generation){request=null;if(!stopped&&document.visibilityState==='visible')timer=window.setTimeout(refresh,delay)}
  }
 }
 jump?.addEventListener('click',()=>{
  if(stopped)return;
  before=0;etag='';follow=true;pendingTarget='';
  history.replaceState(null,'','/');void refresh();
 });
 // Keep conversation drafts when approving a newly arrived action. Each POST
 // carries the displayed frozen hash and is attempted exactly once; a lost
 // acknowledgement triggers a state read, never an automatic resubmission.
 document.addEventListener('submit',event=>{
  const form=event.target;
  if(!(form instanceof HTMLFormElement)||form.getAttribute('action')!=='/approval')return;
  event.preventDefault();
  if(stopped)return;
  if(form.dataset.submitting)return;
  const submitter=(event as SubmitEvent).submitter;
  if(!(submitter instanceof HTMLButtonElement))return;
  const data=new URLSearchParams();
  for(const [key,value] of new FormData(form)){if(typeof value==='string')data.append(key,value)}
  data.set(submitter.name,submitter.value);
  const buttons=Array.from(form.querySelectorAll('button'));
  form.dataset.submitting='true';for(const button of buttons)button.disabled=true;
  void (async()=>{
   const controller=new AbortController(),timeout=setTimeout(()=>controller.abort(),10000);
   try{
    const response=await fetch('/approval',{method:'POST',body:data,credentials:'same-origin',headers:{Accept:'application/json'},signal:controller.signal});
    if(!response.ok||!(await response.json()).accepted)throw new Error('approval not confirmed');
    say('');
   }catch{say('Approval could not be confirmed. Checking its current state. Your draft is kept.')}
   finally{
    clearTimeout(timeout);delete form.dataset.submitting;for(const button of buttons)button.disabled=false;
    void refresh();
   }
  })();
 });
 // Saved questions and result reviews use the same committed refresh stream.
 // The form's native input survives unchanged keyed updates; an uncertain POST
 // is read back, never resubmitted automatically.
 document.addEventListener('submit',event=>{
  const form=event.target;
  if(!(form instanceof HTMLFormElement)||form.getAttribute('action')!=='/task/feedback')return;
  event.preventDefault();
  const status=document.getElementById('task-feedback-status');
  if(stopped||form.dataset.submitting)return;
  const answer=form.querySelector<HTMLTextAreaElement>('[name=answer]');
  const button=form.querySelector<HTMLButtonElement>('button[type=submit]');
  if(!answer||!button)return;
  if(new TextEncoder().encode(answer.value).length>16384){
   const message=t('This answer is too long. Shorten it before sending.');
   answer.setCustomValidity(message);answer.addEventListener('input',()=>answer.setCustomValidity(''),{once:true});answer.reportValidity();
   if(status)status.textContent=message;return;
  }
  if(!form.reportValidity())return;
  const data=new URLSearchParams();
  for(const [key,value] of new FormData(form)){if(typeof value==='string')data.append(key,value)}
  form.dataset.submitting='true';button.disabled=true;
  if(status)status.textContent=t('Sending your response…');
  void(async()=>{
   const controller=new AbortController(),timeout=setTimeout(()=>controller.abort(),10000);
   let message='Your response was not confirmed. Checking the current request. Your draft is kept.';
   try{
    const response=await fetch('/task/feedback',{method:'POST',body:data,credentials:'same-origin',headers:{Accept:'application/json'},signal:controller.signal});
    if(!response.ok){
     if(response.status===400)message='This answer is too long or invalid. Edit it before sending again.';
     if(response.status===409)message='This question or review changed. Your draft is kept for reference and has not been submitted.';
     if(response.status===401||response.status===403)message='Your access changed. Sign in again before sending. Your draft is kept.';
     throw new Error('response rejected');
    }
    if((await response.json()).accepted!==true)throw new Error('response not confirmed');
    form.dataset.feedbackAccepted='true';
    if(status){status.textContent=t('Your response was saved. Ghost will review it in the conversation.');status.tabIndex=-1;status.focus({preventScroll:true})}
   }catch{
    delete form.dataset.feedbackDraftKept;keepFeedbackDraft(form,message);
   }finally{
    clearTimeout(timeout);delete form.dataset.submitting;button.disabled=false;void refresh();
   }
  })();
 });
 document.addEventListener('visibilitychange',()=>{if(document.visibilityState==='visible')void refresh();else pause()});
 window.addEventListener('pagehide',pause);
 window.addEventListener('pagehide',event=>{if(!(event as PageTransitionEvent).persisted)reader?.dispose()});
 window.addEventListener('pageshow',event=>{if(event.persisted)void refresh()});
 if(before)showJump();
 return {messageAccepted:(target:URL)=>{
  before=0;etag='';follow=true;pendingTarget='';
  if(location.href!==target.href)history.replaceState(null,'',target);
  void refresh();
 },start:()=>{
  // The canonical home resumes the latest turn; explicit history links keep their anchor.
  const navigation=performance.getEntriesByType('navigation')[0] as PerformanceNavigationTiming|undefined;
  if(view==='/'&&before===0&&!location.hash&&navigation?.type!=='back_forward')scrollToLatest();
  syncJump();
  void refresh();
 }};
}

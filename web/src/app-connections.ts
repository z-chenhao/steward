import {t} from './i18n';
import {managedAppInterface} from './app-interface';
import {nativeAppInterface, legacyAppProjection, legacyAppPollPlan, legacyAppPollNotice, legacyAppActionView, legacyAppFailureView} from './app-interface-legacy';
import {connectAppsBrowser, validateAppsProjection, validateAppsPollPlans, validateAppsNotice, validateAppsActionView, validateAppsFailureView} from './apps-browser';
import type {AppsBrowser, AppsDisplayFacts, AppsPollFacts, AppsPollPlan, AppsActionFacts} from './apps-browser';
import {confirmSettingsAction} from './settings-controls';

interface AppReply {
 accounts_html?:string;
 sidebar_html?:string;
 authorization_url?:string;
 authorization_expires?:string;
 connection_id?:string;
 error?:string;
 code?:string;
 revision?:string;
 presentation_unavailable?:string;
}
interface PendingLink {url:string;expires:number}

function authorizationURL(raw:string):string|null{
 try{
  const url=new URL(raw);
  return url.protocol==='https:'&&!url.username&&!url.password&&!url.port&&['app.composio.dev','connect.composio.dev'].includes(url.hostname)?url.href:null;
 }catch{return null}
}

// Native forms remain the fallback. OAuth URLs live only in this page's memory;
// no credentials or pending links enter browser storage or navigation queries.
export function enhanceAppConnections(){
 const root=document.querySelector<HTMLElement>('[data-apps]');
 const accounts=root?.querySelector<HTMLElement>('#app-accounts');
 const feedback=root?.querySelector<HTMLElement>('[data-app-feedback]');
 if(!root||!accounts||!feedback)return;
 const presentation = root.dataset.appInterfaceMode === 'managed' ? managedAppInterface(root.dataset.appInterfaceProfile) : root.dataset.appInterfaceMode === undefined || root.dataset.appInterfaceMode === 'native' ? nativeAppInterface() : null;
 if(!presentation)return;
 const policy=presentation.browser,messages=policy.messages;
 const profileText=(text:string)=>root.dataset.appInterfaceLocalized==='true'?text:t(text);
 const interfaceRevision=root.dataset.appInterfaceRevision??'';
 let browser: AppsBrowser|undefined;
 let browserReady: Promise<AppsBrowser>|undefined;
 let projectionSequence=0;
 const owningBrowser=()=>browserReady??=(connectAppsBrowser(interfaceRevision,presentation).then(value=>(browser=value)));


 const dialog=root.querySelector<HTMLDialogElement>('[data-app-dialog]');
 const dialogBody=dialog?.querySelector<HTMLElement>('[data-app-dialog-body]');
 let detailGeneration=0,opener:HTMLElement|null=null;
 const category=root.querySelector<HTMLFormElement>('[data-app-category-form]');
 const autoCategory=()=>category?.querySelector('select')?.addEventListener('change',()=>category.requestSubmit());
 if(!presentation.browser_view)autoCategory();
 else void owningBrowser().then(owner=>{if(owner.categoryAutoSubmit&&!stopped)autoCategory()}).catch(()=>{});
 const links=new Map<string,PendingLink>();
 const timers=new Map<string,ReturnType<typeof setTimeout>>();
 const deadlines=new Map<string,number>();
 const inFlight=new Set<string>();
 const lifetime=new AbortController();
 let stopped=false,busy=false;
 let generation=0,revision=Number(root.dataset.appRevision??0);
 const say=(message:string,error=false)=>{for(const area of [feedback,...root.querySelectorAll<HTMLElement>('[data-app-detail-feedback]')]){area.textContent=profileText(message);area.hidden=false;area.classList.toggle('error',error)}};
 const row=(id:string)=>Array.from(accounts.querySelectorAll<HTMLElement>('[data-app-account]')).find(item=>item.dataset.appAccount===id);
 const remember=()=>{
  for(const item of accounts.querySelectorAll<HTMLElement>('[data-app-account]')){
   const anchor=item.querySelector<HTMLAnchorElement>('[data-app-auth-link]');
   if(anchor&&!links.has(item.dataset.appAccount!)){
    const url=authorizationURL(anchor.href),expires=Number(anchor.dataset.appExpires);
    if(url&&expires>Date.now())links.set(item.dataset.appAccount!,{url,expires});
   }
  }
 };
 const synchronize=async()=>{
  const snapshot=++projectionSequence;
  remember();
  for(const img of document.querySelectorAll<HTMLImageElement>('.app-logo img'))if(img.complete&&img.naturalWidth===0)img.dataset.logoFailed='';
  for(const [id,link] of links){
   const item=row(id);
   if(!item||item.dataset.appState!=='pending'||link.expires<=Date.now()){links.delete(id);continue}
   const area=item.querySelector<HTMLElement>('[data-app-authorization]');
   if(area){
    const anchor=document.createElement('a');anchor.href=link.url;anchor.target='_blank';anchor.rel='noopener noreferrer';anchor.className='button secondary';anchor.textContent=profileText(messages.open_authorization);anchor.dataset.appAuthLink='';anchor.dataset.appExpires=String(link.expires);area.replaceChildren(anchor);
   }
  }
  const all=Array.from(accounts.querySelectorAll<HTMLElement>('[data-app-account]'));
  const tiles=Array.from(root.querySelectorAll<HTMLElement>('[data-app-tile]'));
  const details=Array.from(root.querySelectorAll<HTMLElement>('[data-app-detail]'));
  const facts: AppsDisplayFacts={
   accounts:all.map(item=>({slug:item.dataset.appSlug??'',state:item.dataset.appState??'',public:item.dataset.appPublic==='true'})),
   tiles:tiles.map(item=>item.dataset.appTile??''),
   details:details.map(item=>({slug:item.dataset.appDetail??'',suggested:item.dataset.appSuggested==='true',no_auth:item.dataset.appNoAuth==='true',require_alias:item.dataset.requireAlias==='true'}))
  };
  const owner=presentation.browser_view?await owningBrowser():undefined;
  if(stopped||snapshot!==projectionSequence)return;
  const plan=owner?validateAppsProjection(await owner.call({type:'project',...facts}),facts):legacyAppProjection(facts,presentation);
  if(stopped||snapshot!==projectionSequence)return;
  tiles.forEach((tile,index)=>{
   for(const action of tile.querySelectorAll<HTMLElement>('[data-app-manage]'))action.hidden=plan.tiles[index].manage_hidden;
   for(const action of tile.querySelectorAll<HTMLElement>('[data-app-quick],[data-app-add]'))action.hidden=plan.tiles[index].add_hidden;
  });
  details.forEach((detail,index)=>{
   const view=plan.details[index];
   const matching=view.accounts.map(at=>all[at]);
   const target=detail.querySelector<HTMLElement>('[data-app-detail-accounts]');
   if(target){
    const focused=document.activeElement;
    const focusedRow=focused?.closest<HTMLElement>('[data-app-account]')?.dataset.appAccount;
    const action=focused instanceof HTMLButtonElement?focused.value:null;
    target.replaceChildren(...matching.map(item=>item.cloneNode(true)));
    if(focusedRow&&action)for(const button of target.querySelectorAll<HTMLButtonElement>('button'))if(button.value===action&&button.closest<HTMLElement>('[data-app-account]')?.dataset.appAccount===focusedRow)button.focus();
   }
   const empty=detail.querySelector<HTMLElement>('[data-app-no-accounts]');if(empty)empty.hidden=view.empty_hidden;
   const first=detail.querySelector<HTMLElement>('[data-app-first]');if(first)first.hidden=view.first_hidden;
   const extra=detail.querySelector<HTMLElement>('[data-app-add-account]');if(extra)extra.hidden=view.additional_hidden;
  });
 };
 const openDetails=async(slug:string,source?:HTMLElement)=>{
  if(!dialog||!dialogBody)return;
  const request=++detailGeneration;
  const response=await fetch('/api/apps/detail?app='+encodeURIComponent(slug)+(interfaceRevision?'&interface_revision='+encodeURIComponent(interfaceRevision):''),{signal:AbortSignal.any([lifetime.signal,AbortSignal.timeout(policy.detail_timeout_ms)]),cache:'no-store'});
  if(!response.ok)throw new Error(profileText(messages.details_failed));
  const html=await response.text();if(stopped||request!==detailGeneration)return;
  dialogBody.innerHTML=html;const title=dialogBody.querySelector('h2');if(title)title.id='app-dialog-title';await synchronize();if(stopped||request!==detailGeneration)return;
  if(!feedback.hidden){const area=dialogBody.querySelector<HTMLElement>('[data-app-detail-feedback]');if(area){area.textContent=feedback.textContent;area.hidden=false;area.classList.toggle('error',feedback.classList.contains('error'))}}
  if(!dialog.open){opener=source??document.activeElement as HTMLElement;dialog.showModal()}
 };
 dialog?.addEventListener('close',()=>{detailGeneration++;const target=opener?.getClientRects().length?opener:opener?.closest('[data-app-tile]')?.querySelector<HTMLElement>('[data-app-manage]');target?.focus()});
 root.querySelector('[data-app-close]')?.addEventListener('click',()=>dialog?.close());
 const send=async(body:URLSearchParams):Promise<AppReply>=>{
  if(interfaceRevision)body.set('interface_revision',interfaceRevision);
  const response=await fetch('/apps/configure',{method:'POST',body,headers:{Accept:'application/json'},signal:AbortSignal.any([lifetime.signal,AbortSignal.timeout(policy.action_timeout_ms)]),cache:'no-store'});
  const result:AppReply=await response.json();
  if(!response.ok){const error=new Error(result.error||profileText(messages.connection_unknown));Object.assign(error,{code:result.code});throw error}
  return result;
 };
 const apply=async(result:AppReply)=>{
  const incoming=Number(result.revision??revision);if(incoming<revision)return;
  revision=incoming;
  if(result.authorization_url&&result.connection_id){
   const url=authorizationURL(result.authorization_url),expires=Number(result.authorization_expires);
   if(url&&expires>Date.now())links.set(result.connection_id,{url,expires});
  }
  if(result.sidebar_html!==undefined){const sidebar=document.querySelector('#apps-sidebar');if(sidebar)sidebar.innerHTML=result.sidebar_html}
  if(result.accounts_html!==undefined){accounts.innerHTML=result.accounts_html;await synchronize()}
 };
 const stopPresentation=()=>{stopped=true;projectionSequence++;browser?.close();lifetime.abort();for(const timer of timers.values())clearTimeout(timer);timers.clear();accounts.replaceChildren();for(const target of root.querySelectorAll('[data-app-detail-accounts]'))target.replaceChildren();document.querySelector('#apps-sidebar')?.replaceChildren();for(const button of root.querySelectorAll<HTMLButtonElement>('[data-app-action] button'))button.disabled=true};
 const showAuthorization=(url:string)=>{const anchor=document.createElement('a');anchor.href=url;anchor.target='_blank';anchor.rel='noopener noreferrer';anchor.className='button secondary';anchor.textContent=t('Open authorization page');feedback.append(anchor)};
 const acceptedWithoutPresentation=(result:AppReply,popup?:Window|null)=>{
  stopPresentation();
  say(t('The account action was accepted. App presentation is unavailable. Restore the plugin before continuing.'));
  const url=result.authorization_url?authorizationURL(result.authorization_url):null;
  if(url){if(popup&&!popup.closed){popup.location.replace(url)}showAuthorization(url)}
 };
 const presentationFailed=()=>{
  const retained=Array.from(links).filter(([id,link])=>link.expires>Date.now()&&row(id)?.dataset.appState==='pending').map(([,link])=>link.url);
  stopPresentation();say(t('App presentation is unavailable. Reload after the owner restores the plugin.'),true);
  for(const url of retained)showAuthorization(url);
 };
 let watchSequence=0;
 const poll=async(id:string)=>{
   timers.delete(id);inFlight.add(id);
   const acceptedGeneration=generation;
   let accepted:AppReply|undefined;
   try{
    const result=await send(new URLSearchParams({action:'check',connection:id,automatic:'1'}));accepted=result;
    if(stopped||acceptedGeneration!==generation)return;
    if(result.presentation_unavailable==='true'){acceptedWithoutPresentation(result);return}
    try{await apply(result)}catch{acceptedWithoutPresentation(result);return}
    if(stopped||acceptedGeneration!==generation)return;
    const state=row(id)?.dataset.appState??'';
    const notice=presentation.browser_view?validateAppsNotice(await(await owningBrowser()).call({type:'poll-result',state})):legacyAppPollNotice(state,presentation);
    if(!stopped&&acceptedGeneration===generation&&notice.text)say(notice.text,notice.error);
   }catch(error){
    if(accepted){acceptedWithoutPresentation(accepted);return}
    if(error instanceof Error&&(error as Error&{code?:string}).code==='presentation_unavailable'){presentationFailed();return}
    if(!stopped&&acceptedGeneration===generation)say(messages.poll_failed,true);
   }finally{inFlight.delete(id);if(!stopped)watch()}
 };
 const watch=()=>{
  const snapshot=++watchSequence,atGeneration=generation,atRevision=revision,now=Date.now();
  const items=Array.from(accounts.querySelectorAll<HTMLElement>('[data-app-state="pending"]'));
  if(!items.length||stopped)return;
  const ids=items.map(item=>item.dataset.appAccount!);
  const facts:AppsPollFacts[]=ids.map(id=>({state:'pending',stopped,scheduled:timers.has(id),in_flight:inFlight.has(id),now,deadline:deadlines.get(id)}));
  const install=(plans:AppsPollPlan[])=>{
   if(stopped||snapshot!==watchSequence||atGeneration!==generation||atRevision!==revision)return;
   plans.forEach((plan,index)=>{
    const id=ids[index];
    if(timers.has(id)||inFlight.has(id)||row(id)?.dataset.appState!=='pending')return;
    deadlines.set(id,plan.deadline);
    if(plan.notice)say(plan.notice);
    if(plan.schedule)timers.set(id,setTimeout(()=>void poll(id),plan.delay));
   });
  };
  if(!presentation.browser_view){install(facts.map(fact=>legacyAppPollPlan(fact,presentation)));return}
  void owningBrowser().then(owner=>{
   if(stopped||snapshot!==watchSequence)return;
   return owner.call({type:'polls',items:facts});
  }).then(value=>{if(value)install(validateAppsPollPlans(value,facts))}).catch(presentationFailed);
 };
 root.addEventListener('submit',event=>{
  const form=event.target;if(!(form instanceof HTMLFormElement)||!form.hasAttribute('data-app-action'))return;
  event.preventDefault();if(stopped||busy)return;
  const button=(event as SubmitEvent).submitter;
  const body=new URLSearchParams();for(const [name,value] of new FormData(form))if(typeof value==='string')body.append(name,value);
  if(button instanceof HTMLButtonElement&&button.name)body.set(button.name,button.value);
  const action=body.get('action');
  if(action==='disconnect'&&confirmSettingsAction(form,button instanceof HTMLElement?button:null))return;
  generation++;
  // Reserve the tab synchronously while this click still has user activation.
  const popup=policy.popup_actions.includes(action??'')?window.open('about:blank','_blank'):null;
  if(popup)popup.opener=null;
  const controls=Array.from(root.querySelectorAll<HTMLButtonElement>('[data-app-action] button'));
  const previous=controls.map(item=>item.disabled);controls.forEach(item=>item.disabled=true);
  busy=true;form.setAttribute('aria-busy','true');
  void(async()=>{
   let opened=false,accepted:AppReply|undefined;
   try{
    const result=await send(body);accepted=result;if(stopped)return;
    if(result.presentation_unavailable==='true'){acceptedWithoutPresentation(result,popup);opened=Boolean(result.authorization_url);return}
    try{await apply(result)}catch{acceptedWithoutPresentation(result,popup);opened=Boolean(result.authorization_url);return}
    if(stopped)return;
    if(result.authorization_url){
     const url=authorizationURL(result.authorization_url);if(!url)throw new Error(profileText(messages.authorization_failed));
     if(popup&&!popup.closed){popup.location.replace(url);opened=true}
    }
    const facts:AppsActionFacts={action:action??'',authorization:Boolean(result.authorization_url),popup_opened:opened,dialog_open:Boolean(dialog?.open),detail_page:Boolean(root.querySelector('.app-detail-page'))};
    let view;
    try{view=presentation.browser_view?validateAppsActionView(await(await owningBrowser()).call({type:'action-result',...facts}),facts):legacyAppActionView(facts,presentation)}catch{acceptedWithoutPresentation(result,opened?undefined:popup);opened=Boolean(result.authorization_url);return}
    if(stopped)return;
    say(view.notice);
    watch();
    if(view.open_details){
     const slug=body.get('toolkit')||row(result.connection_id??'')?.dataset.appSlug;
     if(slug)await openDetails(slug,button instanceof HTMLElement?button:undefined);
    }
   }catch(error){
    if(stopped)return;
    say(error instanceof Error?error.message:messages.connection_unknown,true);
    const code=error instanceof Error&&(error as Error&{code?:string}).code==='alias_required'?'alias_required':'';
    let failure;
    try{failure=presentation.browser_view?validateAppsFailureView(await(await owningBrowser()).call({type:'action-failed',code}),code):legacyAppFailureView(code,presentation)}catch{presentationFailed();return}
    if(stopped)return;
    if(failure.reveal_alias){
     form.dataset.requireAlias='true';
     const slug=body.get('toolkit');if(slug){try{await openDetails(slug)}catch{say(messages.details_failed,true);return}const extra=dialogBody?.querySelector<HTMLDetailsElement>('[data-app-add-account]');if(extra){extra.closest<HTMLElement>('[data-app-detail]')!.dataset.requireAlias='true';extra.hidden=false;extra.open=true;extra.querySelector<HTMLInputElement>('input[name=alias]')?.focus()}}
    }
   }finally{
    if(!opened&&popup&&!popup.closed)popup.close();
    controls.forEach((item,index)=>item.disabled=stopped||previous[index]);busy=false;form.removeAttribute('aria-busy');
    if(!stopped)void synchronize().catch(()=>{if(accepted)acceptedWithoutPresentation(accepted);else{stopPresentation();say(t('App presentation is unavailable. Reload after the owner restores the plugin.'),true)}});
   }
  })();
 });
 document.addEventListener('click',event=>{
  const source=(event.target as Element).closest<HTMLAnchorElement>('[data-app-open]');
  if(source&&dialog&&!event.ctrlKey&&!event.metaKey&&!event.shiftKey&&event.button===0){event.preventDefault();void openDetails(source.dataset.appOpen!,source).catch(()=>location.assign(source.href));return}
  const anchor=(event.target as Element).closest<HTMLAnchorElement>('[data-app-auth-link]');
  if(!anchor)return;
  if(Number(anchor.dataset.appExpires)<=Date.now()){event.preventDefault();say(messages.link_expired,true)}
 });
 document.addEventListener('error',event=>{const img=event.target;if(img instanceof HTMLImageElement&&img.closest('.app-logo'))img.dataset.logoFailed=''},true);
 const start=async()=>{stopped=false;await synchronize();if(!stopped){watch();root.dataset.appsReady=''}};
 window.addEventListener('pagehide',()=>{stopped=true;projectionSequence++;browser?.close();lifetime.abort();for(const timer of timers.values())clearTimeout(timer);timers.clear()});
 window.addEventListener('pageshow',event=>{if(event.persisted)location.reload()});
 void start().catch(()=>{stopPresentation();say(t('App presentation is unavailable. Reload after the owner restores the plugin.'),true)});
}

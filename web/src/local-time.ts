// Host-authored timestamps share one browser timezone. Stored/API instants and
// scheduling rules retain their own semantics.
export type TimeStyle='message'|'date'|'datetime'|'clock';
export function userTimezone(){return Intl.DateTimeFormat().resolvedOptions().timeZone}
export function formatLocalTime(raw:string,style:TimeStyle='datetime',locale='en',zone=userTimezone(),now=new Date()){
 const date=new Date(raw);
 if(!raw||Number.isNaN(date.valueOf()))return null;
 const parts=(value:Date)=>new Intl.DateTimeFormat('en',{timeZone:zone,year:'numeric',month:'2-digit',day:'2-digit',hour:'2-digit',minute:'2-digit',second:'2-digit',hourCycle:'h23'}).formatToParts(value);
 const fields=(value:Date)=>{const values=parts(value);const part=(type:string)=>values.find(value=>value.type===type)!.value;return {day:`${part('year')}-${part('month')}-${part('day')}`,clock:`${part('hour')}:${part('minute')}:${part('second')}`,serial:Date.UTC(Number(part('year')),Number(part('month'))-1,Number(part('day')))/86400000}};
 const value=fields(date),today=fields(now),days=today.serial-value.serial,chinese=locale.startsWith('zh');
 const label=days===0?(chinese?'今天':'Today'):days===1?(chinese?'昨天':'Yesterday'):days===-1?(chinese?'明天':'Tomorrow'):days>1&&days<7?new Intl.DateTimeFormat(locale,{weekday:'long',timeZone:zone}).format(date):value.day;
 const text=style==='date'?label:style==='message'||style==='clock'||days===0?value.clock:`${label} ${value.clock}`;
 return {text,full:`${value.day} ${value.clock}`,day:value.day};
}

export function localTimeElement(raw:string,style:TimeStyle='datetime'){
 const element=document.createElement('time');element.dateTime=raw;element.dataset.localTime=style;
 localizeTime(element);return element;
}
function localizeTime(element:HTMLTimeElement){
 const value=formatLocalTime(element.dateTime,element.dataset.localTime as TimeStyle,document.documentElement.lang);
 if(!value)return;
 if(element.textContent!==value.text)element.textContent=value.text;
 if(element.title!==value.full)element.title=value.full;
 if(element.getAttribute('aria-label')!==value.full)element.setAttribute('aria-label',value.full);
}

export function enhanceLocalTimes(){
 const initializedInputs=new WeakSet<HTMLInputElement>();
 const originalInstants=new WeakMap<HTMLInputElement,{local:string;instant:string}>();
 const initialize=(scope:ParentNode)=>{
  const times=[...(scope instanceof HTMLTimeElement&&scope.matches('[data-local-time]')?[scope]:[]),...scope.querySelectorAll<HTMLTimeElement>('time[data-local-time]')];
  for(const element of times)localizeTime(element);
  for(const field of scope.querySelectorAll<HTMLInputElement>('input[data-local-input]')){
   if(initializedInputs.has(field))continue;
   initializedInputs.add(field);
   const date=new Date(field.dataset.localInput??'');
   if(!Number.isNaN(date.valueOf())){
    const pad=(value:number)=>String(value).padStart(2,'0');
    field.value=`${date.getFullYear()}-${pad(date.getMonth()+1)}-${pad(date.getDate())}T${pad(date.getHours())}:${pad(date.getMinutes())}:${pad(date.getSeconds())}`;
    originalInstants.set(field,{local:field.value,instant:date.toISOString()});
   }
  }
 };
 const dates=()=>{
  let day='';
  for(const divider of document.querySelectorAll<HTMLElement>('#conversation-messages .message-date')){
   const time=divider.querySelector<HTMLTimeElement>('time[data-local-time]');
   const value=time&&formatLocalTime(time.dateTime,'date');
   if(value){const hidden=day===value.day;if(divider.hidden!==hidden)divider.hidden=hidden;day=value.day}
  }
 };
 const syncZone=()=>{
  try{document.cookie=`steward-timezone=${encodeURIComponent(userTimezone())}; Path=/; Max-Age=31536000; SameSite=Strict`}catch{}
 };
 let midnight=0;
 const refreshDay=()=>{
  clearTimeout(midnight);initialize(document);dates();
  const now=new Date(),next=new Date(now.getFullYear(),now.getMonth(),now.getDate()+1);
  midnight=window.setTimeout(refreshDay,next.valueOf()-now.valueOf()+100);
 };
 syncZone();refreshDay();
 new MutationObserver(records=>{
  let changed=false;
  for(const record of records){
   for(const node of record.addedNodes)if(node instanceof HTMLElement){initialize(node);if(node.matches('.message-date')||node.querySelector('.message-date'))changed=true}
   for(const node of record.removedNodes)if(node instanceof HTMLElement&&(node.matches('.message-date')||node.querySelector('.message-date')))changed=true;
  }
  if(changed)dates();
 }).observe(document.body,{childList:true,subtree:true});
 // Native GET filters submit explicit instants, including the offset selected
 // by the browser during daylight-saving time. The server also accepts the
 // last known browser zone when scripting is unavailable.
 document.addEventListener('formdata',event=>{
  if(!(event.target instanceof HTMLFormElement))return;
  for(const control of Array.from(event.target.elements)){
   if(!(control instanceof HTMLInputElement)||!control.matches('[data-local-input]'))continue;
   const field=control;
   if(!field.name||!field.value||field.disabled)continue;
   const original=originalInstants.get(field);
   if(original?.local===field.value){event.formData.set(field.name,original.instant);continue}
   // For newly entered repeated wall times, Date chooses the earlier offset.
   const date=new Date(field.value);
   if(!Number.isNaN(date.valueOf())){
    const local=new Date(date.valueOf()-date.getTimezoneOffset()*60000).toISOString().slice(0,field.value.length);
    if(local===field.value)event.formData.set(field.name,date.toISOString());
   }
  }
 });
 window.addEventListener('pagehide',()=>clearTimeout(midnight));
 window.addEventListener('pageshow',event=>{if(event.persisted)refreshDay()});
 document.addEventListener('visibilitychange',()=>{if(!document.hidden){syncZone();refreshDay()}});
}

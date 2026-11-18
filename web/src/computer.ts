import {t} from './i18n';
import {connectWorkspacePanel} from './workspace-panels';

// Read-only viewer. Only the authenticated Host chooses the current instance;
// the browser sends no target, VM name, handle, lease, or input command.
export function enhanceComputerView(){
 const panel=document.querySelector<HTMLDialogElement>('#computer-panel');
 if(!panel)return;
 const screen=panel.querySelector<HTMLElement>('[data-computer-screen]')!;
 const placeholder=panel.querySelector<HTMLElement>('[data-computer-placeholder]')!;
 const status=panel.querySelector<HTMLElement>('[data-computer-status]')!;
 const caption=panel.querySelector<HTMLElement>('[data-computer-caption]')!;
 const activity=panel.querySelector<HTMLOListElement>('[data-computer-activity]')!;
 const say=(text:string)=>{const value=t(text);if(status.textContent!==value)status.textContent=value;};
 let generation=0,timer=0,request:AbortController|null=null,pendingImage:HTMLImageElement|null=null;
 let previousActivity='';
 const phaseLabels:Record<string,string>={ready:'Connected',unprovisioned:'Computer is not connected.',checkpointed:'Computer is paused.',checkpointing:'Computer is saving its state.',restoring:'Computer is restoring.'};
 const actionLabels:Record<string,string>={getState:'Inspect workspace',getApp:'Find application',getTab:'Inspect browser tab',getBrowser:'Inspect browser',createBrowserTab:'Open browser tab',getScreenshot:'Capture screen',getAXState:'Inspect interface',getAXStateAndScreenshot:'Inspect screen',observe:'Observe screen',click:'Click',typeText:'Type text',scroll:'Scroll',pressKey:'Press keys',key:'Press keys',move:'Move pointer'};
 function stop(){generation++;clearTimeout(timer);request?.abort();request=null;if(pendingImage){pendingImage.removeAttribute('src');pendingImage=null;}}
 function activityRows(items:unknown[]){
  const safe=items.slice(0,20).filter((item):item is {id:string;action:string;status:string;at:string}=>!!item&&typeof item==='object'&&['id','action','status','at'].every(key=>typeof (item as Record<string,unknown>)[key]==='string'));
  const key=JSON.stringify(safe);if(previousActivity===key)return;previousActivity=key;activity.replaceChildren();
  for(const item of safe){
   const row=document.createElement('li'),action=document.createElement('strong'),state=document.createElement('span'),time=document.createElement('time');
   action.textContent=t(actionLabels[item.action]??'Computer operation');state.textContent=t(item.status);time.dateTime=item.at;
   const at=new Date(item.at);time.textContent=Number.isNaN(at.valueOf())?'':at.toLocaleTimeString(document.documentElement.lang,{hour:'2-digit',minute:'2-digit',second:'2-digit'});
   row.append(action,state,time);activity.append(row);
  }
  if(!safe.length){const row=document.createElement('li');row.textContent=t('No Computer operations have been recorded yet.');activity.append(row);}
 }
 async function refresh(){
  if(!panel?.open||document.hidden)return;
  const current=++generation;
  const controller=new AbortController();request=controller;
  const timeout=setTimeout(()=>controller.abort(),6000);
  try{
   const response=await fetch('/api/computer/status',{credentials:'same-origin',cache:'no-store',signal:controller.signal});
   if(current!==generation||!panel.open)return;
   if(response.status===401||response.status===403){activity.replaceChildren();previousActivity='';caption.textContent=t('Read only');screen.querySelector('img')?.remove();placeholder.hidden=false;placeholder.textContent=t('Sign in to view Computer.');say('Sign in to view Computer.');stop();return;}
   if(!response.ok)throw Error('Computer unavailable');
   const value=await response.json();
   if(typeof value.phase!=='string'||!Array.isArray(value.activity))throw Error('Invalid Computer view');
   if(current!==generation||!panel.open)return;
   if(value.phase!=='ready')say(phaseLabels[value.phase]??'Computer status is unavailable.');if(value.activity_available===false){activity.replaceChildren();const row=document.createElement('li');row.textContent=t('Operation progress is temporarily unavailable.');activity.append(row);previousActivity='';}else activityRows(value.activity);
   if(value.phase==='ready'){
    const image=new Image();pendingImage=image;image.alt=t('Current Computer desktop');
    image.src='/api/computer/frame?at='+Date.now();
    let frameTimeout:ReturnType<typeof setTimeout>|undefined;
    try{await Promise.race([image.decode(),new Promise((_,reject)=>{frameTimeout=setTimeout(()=>reject(Error('Frame timeout')),6000);})]);}
    finally{clearTimeout(frameTimeout);}
    if(current!==generation||!panel.open||document.hidden)return;
    screen.querySelector('img')?.remove();screen.append(image);placeholder.hidden=true;pendingImage=null;say('Connected');
    caption.textContent=t('Read-only desktop · updated')+' '+new Date().toLocaleTimeString(document.documentElement.lang,{hour:'2-digit',minute:'2-digit',second:'2-digit'});
   }else{
    screen.querySelector('img')?.remove();placeholder.hidden=false;placeholder.textContent=t(phaseLabels[value.phase]??'No desktop image is available.');
    caption.textContent=t('Read only');
   }
  }catch{
   if(current===generation&&panel.open){say('Computer preview is unavailable. Retrying…');if(!screen.querySelector('img')){placeholder.hidden=false;placeholder.textContent=t('No desktop image is available.');}else{caption.textContent=t('Last received image · connection interrupted');}}
  }finally{
   clearTimeout(timeout);if(current===generation){request=null;pendingImage?.removeAttribute('src');pendingImage=null;if(panel.open&&!document.hidden)timer=window.setTimeout(refresh,2000);}
  }
 }
 connectWorkspacePanel(panel,()=>{stop();say('Connecting to Computer…');screen.querySelector('img')?.remove();placeholder.hidden=false;void refresh()},()=>{stop();screen.querySelector('img')?.remove();placeholder.hidden=false});
 document.addEventListener('visibilitychange',()=>{if(document.hidden)stop();else if(panel.open)void refresh();});
 window.addEventListener('pagehide',stop);
}

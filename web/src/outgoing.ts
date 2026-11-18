import {t} from './i18n';

export type Outgoing={node:HTMLElement;status:HTMLElement;confirmed:boolean};
const outgoing=new Map<string,Outgoing>();
export function showOutgoing(source:string,text:string,file:File|undefined):Outgoing{
 const region=document.getElementById('conversation-outgoing')!;
 const node=document.createElement('article');node.className='message user outgoing';node.dataset.clientSource=source;node.setAttribute('aria-label',t('You'));
 const content=document.createElement('div');content.className='semantic';content.textContent=text;node.append(content);
 if(file){const name=document.createElement('p');name.className='outgoing-file';name.textContent=file.name;node.append(name)}
 const status=document.createElement('div');status.className='outgoing-status';status.setAttribute('role','status');node.append(status);
 const entry={node,status,confirmed:false};outgoing.set(source,entry);region.append(node);return entry;
}
export function reconcileOutgoing(){
 for(const [source,entry] of outgoing){
  if(document.querySelector(`#conversation-messages [data-client-source="${source}"]`)){
   entry.confirmed=true;entry.node.remove();outgoing.delete(source);
  }
 }
}
export function outgoingState(entry:Outgoing,state:'sending'|'accepted'|'uncertain'|'rejected'|'state-failed',retry?:()=>void){
 if(entry.confirmed)return;
 entry.node.dataset.sendState=state;
 entry.status.replaceChildren(document.createTextNode(t(state==='sending'?'Sending…':state==='accepted'?'Sent':state==='rejected'?'This message is too large or invalid. Edit it before sending again.':state==='state-failed'?'Could not save interactive answers. Your message is ready to retry.':'Send not confirmed.')));
 if(retry){const button=document.createElement('button');button.type='button';button.textContent=t(state==='rejected'?'Edit message':'Retry sending');button.addEventListener('click',retry);entry.status.append(' ',button)}
}

export function discardOutgoing(entry:Outgoing){
 entry.node.remove();outgoing.delete(entry.node.dataset.clientSource??'');
}

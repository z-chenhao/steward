import {useEffect,useRef,useState} from 'react';

// Fixed Host controller for an accepted read-only view. It accepts height only;
// candidate content has no state, action or ordinary-input proposal channel.
export function FrozenPlanView({reference,fallback}:{reference:string;fallback:string}){
 return <ReadonlyFrame key={reference} reference={reference} source={'/ui/plan?'+new URLSearchParams({ref:reference})} fallback={fallback}/>;
}
export function PreviewPlanView({session,item,index,revision,fallback}:{session:string;item:string;index:number;revision:number;fallback:string}){
 const source='/ui/plan-preview?'+new URLSearchParams({key:session,item,component:String(index),revision:String(revision)});
 return <ReadonlyFrame key={source} reference={source} source={source} fallback={fallback}/>;
}
export function PreviewCardsView({session,item,index,revision,fallback}:{session:string;item:string;index:number;revision:number;fallback:string}){
 const source='/ui/cards-preview?'+new URLSearchParams({key:session,item,component:String(index),revision:String(revision)});
 return <ReadonlyFrame key={source} reference={source} source={source} fallback={fallback} name="steward.cards" title="Cards preview"/>;
}
export function PreviewTableView({session,item,index,revision,fallback}:{session:string;item:string;index:number;revision:number;fallback:string}){
 const source='/ui/table-preview?'+new URLSearchParams({key:session,item,component:String(index),revision:String(revision)});
 return <ReadonlyFrame key={source} reference={source} source={source} fallback={fallback} name="Table" title="Table preview" maxHeight={100000}/>;
}
function ReadonlyFrame({reference,source,fallback,name="plan_visualization",title="Plan view",maxHeight=4000}:{reference:string;source:string;fallback:string;name?:string;title?:string;maxHeight?:number}){
 const frame=useRef<HTMLIFrameElement>(null);
 const [failed,setFailed]=useState(false),[height,setHeight]=useState(50);
 const [theme]=useState(()=>document.documentElement.classList.contains('dark')?'dark':'light');
 const sendTheme=()=>{const style=getComputedStyle(document.documentElement);const tokens=Object.fromEntries(['--card','--border','--foreground','--muted-foreground','--hover','--font-ui'].map(key=>[key,style.getPropertyValue(key).trim()]));frame.current?.contentWindow?.postMessage({channel:'steward-plan-view',type:'theme',theme:document.documentElement.classList.contains('dark')?'dark':'light',tokens,viewportWidth:window.innerWidth,locale:Intl.Collator().resolvedOptions().locale,language:document.documentElement.lang==='zh-CN'?'zh-CN':'en'},'*')};
 useEffect(()=>{
  let alive=true,received=false;
  const timeout=window.setTimeout(()=>{if(alive&&!received)setFailed(true)},10000);
  const receive=(event:MessageEvent)=>{const value=event.data;if(event.source!==frame.current?.contentWindow||event.origin!=='null'||value?.channel!=='steward-plan-view')return;if(value.type==='failed'){setFailed(true);return}if(value.type!=='height'||!Number.isFinite(value.height))return;received=true;setHeight(Math.max(1,Math.min(maxHeight,Math.ceil(value.height))))};
  window.addEventListener('message',receive);window.addEventListener('resize',sendTheme);
  const observer=new MutationObserver(sendTheme);observer.observe(document.documentElement,{attributes:true,attributeFilter:['class','lang']});sendTheme();
  return()=>{alive=false;window.removeEventListener('message',receive);window.removeEventListener('resize',sendTheme);window.clearTimeout(timeout);observer.disconnect()};
 },[reference]);
 if(failed)return <p className="semantic">{fallback}</p>;
 return <section className="openintelligentui-native" data-openintelligentui={name} data-view-ref={reference}><iframe ref={frame} onLoad={sendTheme} title={title} sandbox="allow-scripts" referrerPolicy="no-referrer" onError={()=>setFailed(true)} src={source+'&theme='+theme} style={{display:'block',width:'100%',height,border:0}}/></section>;
}

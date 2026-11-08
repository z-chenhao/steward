// Shared top-layer tooltip for imperative charts. Text only; callers own the
// data and selection policy, this primitive owns placement and dismissal.
let nextTooltip=0;
export function createChartTooltip(){
 const element=document.createElement('div');element.className='control-chart-tooltip';element.id='chart-tooltip-'+(++nextTooltip);element.setAttribute('role','tooltip');element.popover='manual';element.hidden=true;document.body.append(element);
 const lifetime=new AbortController();let owner:Element|null=null;
 const hide=()=>{owner?.removeAttribute('aria-describedby');owner=null;if(element.matches(':popover-open'))element.hidePopover();element.hidden=true};
 const show=(text:string,x:number,y:number,target?:Element)=>{
  owner?.removeAttribute('aria-describedby');owner=target??null;owner?.setAttribute('aria-describedby',element.id);
  element.textContent=text;element.hidden=false;if(!element.matches(':popover-open'))element.showPopover();
  const zoom=parseFloat(getComputedStyle(document.documentElement).zoom)||1;
  element.style.maxWidth=Math.min(320,innerWidth-24)/zoom+'px';
  const rect=element.getBoundingClientRect();element.style.left=Math.max(12,Math.min(x+12,innerWidth-rect.width-12))/zoom+'px';element.style.top=Math.max(12,Math.min(y+12,innerHeight-rect.height-12))/zoom+'px';
 };
 document.addEventListener('keydown',event=>{if(event.key==='Escape')hide()},{signal:lifetime.signal});
 window.addEventListener('resize',hide,{signal:lifetime.signal});window.addEventListener('scroll',hide,{capture:true,signal:lifetime.signal});
 return {show,hide,dispose:()=>{hide();lifetime.abort();element.remove()}};
}

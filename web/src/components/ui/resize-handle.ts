// Shared pointer/keyboard behavior for the two real resizable trace surfaces.
export function createResizeHandle(label:string,options:{bounds:()=>{min:number;max:number;value:number};growLeft?:boolean;at:(clientX:number)=>number;change:(value:number,commit:boolean)=>void}){
 const element=document.createElement('div');element.className='control-resize-handle';element.tabIndex=0;element.setAttribute('role','separator');element.setAttribute('aria-orientation','vertical');element.setAttribute('aria-label',label);
 const lifetime=new AbortController();let pointer:number|null=null,start=0,origin=0;
 const sync=()=>{const range=options.bounds();element.setAttribute('aria-valuemin',String(Math.round(range.min)));element.setAttribute('aria-valuemax',String(Math.round(range.max)));element.setAttribute('aria-valuenow',String(Math.round(range.value)));element.setAttribute('aria-valuetext',Math.round(range.value)+'px')};
 const apply=(value:number,commit=false)=>{const range=options.bounds();options.change(Math.min(range.max,Math.max(range.min,value)),commit);sync()};
 element.addEventListener('pointerdown',event=>{if(event.button!==0||pointer!==null)return;event.preventDefault();pointer=event.pointerId;start=options.bounds().value;origin=options.at(event.clientX);element.setPointerCapture(pointer);element.dataset.dragging='';element.focus({preventScroll:true})},{signal:lifetime.signal});
 element.addEventListener('pointermove',event=>{if(pointer===event.pointerId)apply(start+options.at(event.clientX)-origin)},{signal:lifetime.signal});
 const end=(commit:boolean)=>{if(pointer===null)return;if(!commit)apply(start);else apply(options.bounds().value,true);if(element.hasPointerCapture(pointer))element.releasePointerCapture(pointer);pointer=null;delete element.dataset.dragging};
 element.addEventListener('pointerup',event=>{if(event.pointerId===pointer)end(true)},{signal:lifetime.signal});element.addEventListener('pointercancel',event=>{if(event.pointerId===pointer)end(false)},{signal:lifetime.signal});
 element.addEventListener('lostpointercapture',event=>{if(event.pointerId===pointer)end(false)},{signal:lifetime.signal});
 element.addEventListener('keydown',event=>{if(event.altKey||event.metaKey||event.ctrlKey)return;if(event.key==='Escape'&&pointer!==null){event.preventDefault();event.stopPropagation();end(false);return}const range=options.bounds();const step=options.growLeft?-16:16;const values:Record<string,number>={ArrowLeft:range.value-step,ArrowRight:range.value+step,Home:range.min,End:range.max};if(event.key in values){event.preventDefault();apply(values[event.key],true)}},{signal:lifetime.signal});
 sync();return {element,sync,dispose:()=>{lifetime.abort();element.remove()}};
}

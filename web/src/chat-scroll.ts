// Document-level reading state, independent of the Agent or transport.
// Content growth must not erase follow intent; upward input and text selection
// must cancel it before any queued resize callback can move the viewport.
export function createChatScroll(content:HTMLElement,changed:()=>void){
 let following=nearBottom(),escaped=false,lastTop=scrollY,frame=0,disposed=false,touchY:number|undefined;
 const interacting=()=>!!document.activeElement?.closest('.intelligent-widget,.openintelligentui-native,.rich-answer-form')||!!window.getSelection()?.toString();
 function nearBottom(){return scrollY+innerHeight>=document.documentElement.scrollHeight-100}
 const cancel=()=>{following=false;escaped=true;cancelAnimationFrame(frame);frame=0};
 const toBottom=()=>{following=true;escaped=false;window.scrollTo({top:document.documentElement.scrollHeight,behavior:'instant'});lastTop=scrollY};
 // Shrinking content can clamp scrollY without a reading gesture. Compare
 // against the previous position clamped to the current scrollable range.
 const movedUp=()=>scrollY<Math.min(lastTop,Math.max(0,document.documentElement.scrollHeight-innerHeight))-1;
 const resize=()=>{
  if(movedUp())cancel();lastTop=scrollY;
  changed();
  if(!following||interacting()||disposed||document.visibilityState!=='visible')return;
  cancelAnimationFrame(frame);frame=requestAnimationFrame(()=>{frame=0;if(following&&!interacting()&&!disposed)toBottom()});
 };
 const scroll=()=>{if(movedUp())cancel();else if(!escaped&&nearBottom())following=true;lastTop=scrollY};
 const wheel=(event:WheelEvent)=>{if(event.deltaY<0)cancel();else if(event.deltaY>0)escaped=false};
 const key=(event:KeyboardEvent)=>{if(event.altKey||event.ctrlKey||event.metaKey||event.target instanceof Element&&event.target.closest('input,textarea,select,[contenteditable]'))return;if(['ArrowUp','PageUp','Home'].includes(event.key))cancel();else if(['ArrowDown','PageDown','End'].includes(event.key))escaped=false};
 const touchStart=(event:TouchEvent)=>{touchY=event.touches[0]?.clientY};
 const touchMove=(event:TouchEvent)=>{const y=event.touches[0]?.clientY;if(y!==undefined&&touchY!==undefined){if(y>touchY+1)cancel();else if(y<touchY-1)escaped=false}touchY=y};
 const selection=()=>{if(window.getSelection()?.toString())cancel()};
 const observer=new ResizeObserver(resize);observer.observe(content);
 window.addEventListener('scroll',scroll,{passive:true});window.addEventListener('wheel',wheel,{passive:true});
 window.addEventListener('keydown',key);window.addEventListener('touchstart',touchStart,{passive:true});window.addEventListener('touchmove',touchMove,{passive:true});document.addEventListener('selectionchange',selection);
 return {
  isFollowing:()=>following&&!interacting(),
  toBottom,
  dispose(){disposed=true;cancel();observer.disconnect();window.removeEventListener('scroll',scroll);window.removeEventListener('wheel',wheel);window.removeEventListener('keydown',key);window.removeEventListener('touchstart',touchStart);window.removeEventListener('touchmove',touchMove);document.removeEventListener('selectionchange',selection)},
 };
}

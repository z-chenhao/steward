import {createResizeHandle} from './resize-handle';

export function enhanceSplitView(root:HTMLElement,label:string){
 const storage=root.dataset.splitStorage;let fraction=.45;
 try{const stored=Number(storage&&localStorage.getItem(storage));if(stored>=.2&&stored<=.7)fraction=stored}catch{}
 const bounds=()=>({min:200,max:Math.max(200,root.clientWidth-246),value:Math.min(Math.max(200,root.clientWidth*fraction),Math.max(200,root.clientWidth-246))});
 const render=()=>{root.style.gridTemplateColumns=bounds().value+'px 6px minmax(0,1fr)';handle.element.hidden=getComputedStyle(root).display!=='grid'||root.clientWidth<560;handle.sync()};
 const handle=createResizeHandle(label,{bounds,at:x=>x-root.getBoundingClientRect().left,change:(value,commit)=>{fraction=value/Math.max(1,root.clientWidth);render();if(commit&&storage)try{localStorage.setItem(storage,String(fraction))}catch{}}});
 handle.element.classList.add('control-split-handle');root.lastElementChild?.before(handle.element);
 const observer=new ResizeObserver(render);observer.observe(root);render();
 return ()=>{observer.disconnect();handle.dispose()};
}

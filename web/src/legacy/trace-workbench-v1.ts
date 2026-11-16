// Historical 1.0.1 and explicit unbundled read-only embedders only.
// Remove when those graphs/embedders no longer consume the native workbench.
export function legacyRenderTraceTree(pane:HTMLElement,closed:Set<string>,t:(text:string)=>string){

  const nodes=Array.from(pane.querySelectorAll<HTMLElement>('.observation-node')),byId=new Map(nodes.map(node=>[node.dataset.observationId,node]));
  const query=pane.querySelector<HTMLInputElement>('.obs-tree-search input')?.value.toLocaleLowerCase().trim()??'',visible=new Set<HTMLElement>();
  for(const node of nodes)if(!query||[node.querySelector('.obs-node-name')?.textContent,node.dataset.observationType,node.dataset.observationId].join(' ').toLocaleLowerCase().includes(query)){
   let current:HTMLElement|undefined=node,blocked=false;
   for(let depth=0;current&&depth<=nodes.length;depth++){current=byId.get(current.dataset.parentObservation);if(current&&closed.has(current.dataset.observationId??''))blocked=true}
   if(query){current=node;for(let depth=0;current&&depth<=nodes.length;depth++){visible.add(current);current=byId.get(current.dataset.parentObservation)}}else if(!blocked)visible.add(node);
  }
  for(const node of nodes){node.hidden=!visible.has(node);const button=node.querySelector<HTMLButtonElement>('[data-observation-toggle]');if(button){const expanded=query?nodes.some(child=>child.dataset.parentObservation===node.dataset.observationId&&visible.has(child)):!closed.has(node.dataset.observationId??'');button.disabled=!!query;button.setAttribute('aria-expanded',String(expanded));button.setAttribute('aria-label',t(expanded?'Collapse node':'Expand node')+': '+node.querySelector('.obs-node-name')?.textContent)}}

}
export function legacyTraceColumns(storage:Partial<Record<string,string>>,unavailable:string[]=[]):boolean[]{
 const toggles=Array(10).fill(null),localStorage={getItem:(key:string)=>{if(unavailable.includes(key))throw Error('storage unavailable');return storage[key]??null}};
  let visible=toggles.map(()=>true);
  try{
   let saved=JSON.parse(localStorage.getItem('steward.trace.columns.v5')??'null');
   if(saved===null){
    let legacy=JSON.parse(localStorage.getItem('steward.trace.columns.v4')??'null');
    if(legacy===null){
     let previous=JSON.parse(localStorage.getItem('steward.trace.columns.v3')??'null');
     if(previous===null){const older=JSON.parse(localStorage.getItem('steward.trace.columns.v2')??'null');if(Array.isArray(older)&&older.length===10)previous=[...older.slice(0,5),true,...older.slice(5)]}
     if(Array.isArray(previous)&&previous.length===11&&previous.every(value=>typeof value==='boolean'))legacy=[previous[0],previous[1],previous[9],...previous.slice(3,9),previous[10]];
    }
    if(Array.isArray(legacy)&&legacy.length===10&&legacy.every(value=>typeof value==='boolean'))saved=[legacy[9],...legacy.slice(0,9)];
   }
   if(Array.isArray(saved)&&saved.length===toggles.length&&saved.every(value=>typeof value==='boolean'))visible=saved;
  }catch{}

 return visible;
}

export function legacyTraceColumnControls(visible:boolean[],change?:{index:number;checked:boolean}){
 const next=visible.slice();if(change)next[change.index]=change.checked;
 return {visible:next,indexes:[0,1,2,3,5,6,7,8,9,10],storage_key:'steward.trace.columns.v5',persist:!!change};
}

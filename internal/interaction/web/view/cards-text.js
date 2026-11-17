// Literal display text only. Never receives identities, URLs or image references.
// Absent attributes preserve immutable older producer output.
()=>{
  const nodes=[...document.querySelectorAll('.native-carousel [data-card-text]')];
  if(nodes.length>65)return false;
  const changes=[];
  try{
    for(const node of nodes){
      const encoded=node.getAttribute('data-card-text');
      if(!['H3','BUTTON','P','A'].includes(node.tagName)||encoded.length>128*1024)return false;
      const value=new TextDecoder('utf-8',{fatal:true}).decode(Uint8Array.from(atob(encoded),c=>c.charCodeAt(0)));
      changes.push([node,value]);
    }
  }catch{return false}
  for(const [node,value] of changes){node.textContent=value;node.removeAttribute('data-card-text')}
  return true;
}

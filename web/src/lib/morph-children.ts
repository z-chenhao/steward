// Reconcile already-sanitized display nodes. Never pass untrusted markup here.
const same=(a:Node,b:Node)=>a.nodeType===b.nodeType&&a.nodeName===b.nodeName&&(a.nodeType!==1||(a as Element).namespaceURI===(b as Element).namespaceURI);
export function morphChildren(parent:Node,next:Node){
  const retained=new Set();
  for(const source of [...next.childNodes]){
    const id=source.nodeType===1?(source as Element).id:'';
    let node:Node|undefined=id?[...(parent as Element).children].find(n=>n.id===id&&same(n,source)):undefined;
    const position=parent.childNodes[retained.size];
    if(!node&&position&&!retained.has(position)&&same(position,source)&&!(position.nodeType===1&&(position as Element).id))node=position;
    if(!node){node=source.cloneNode(true);parent.insertBefore(node,position||null);}
    else{
      if(node!==position)parent.insertBefore(node,position||null);
      if(node.nodeType===1){
        for(const attr of [...(node as Element).attributes])if(!(source as Element).hasAttributeNS(attr.namespaceURI,attr.localName))(node as Element).removeAttributeNS(attr.namespaceURI,attr.localName);
        for(const attr of [...(source as Element).attributes])if((node as Element).getAttributeNS(attr.namespaceURI,attr.localName)!==attr.value)(node as Element).setAttributeNS(attr.namespaceURI,attr.name,attr.value);
        morphChildren(node,source);
      }else if(node.nodeValue!==source.nodeValue)node.nodeValue=source.nodeValue;
    }
    retained.add(node);
  }
  for(const node of [...parent.childNodes])if(!retained.has(node))parent.removeChild(node);
}

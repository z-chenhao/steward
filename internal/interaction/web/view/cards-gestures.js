// Trusted Host driver: executes after sanitized markup and before package code.
// Its private port is never exposed to the package runtime.
(()=>{
  const bridge=new MessageChannel();
  const send=bridge.port1.postMessage.bind(bridge.port1);
  const prevent=Function.prototype.call.bind(Event.prototype.preventDefault);
  const listen=EventTarget.prototype.addEventListener;
  const readKey=Function.prototype.call.bind(Object.getOwnPropertyDescriptor(KeyboardEvent.prototype,'key').get);
  listen.call(window,'keydown',event=>{
    if(!event.isTrusted||readKey(event)!=='Escape')return;
    prevent(event);send({type:'escape'});
  },true);
  const articles=document.querySelectorAll('.native-carousel-list > article');
  articles.forEach((article,item)=>{
    article.querySelectorAll('a').forEach(link=>{
      link.setAttribute('role','link');link.setAttribute('tabindex','0');
      const activate=event=>{if(!event.isTrusted)return;prevent(event);send({type:'open-link',item})};
      listen.call(link,'click',activate,true);
      listen.call(link,'keydown',event=>{if(event.isTrusted&&readKey(event)==='Enter')activate(event)},true);
    });
  });
  parent.postMessage({channel:'steward-cards-view',type:'gesture-port'},'*',[bridge.port2]);
})();

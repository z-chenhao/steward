package web

import (
	"context"
	"encoding/json"
	"fmt"
	"html"
	"net/http"
	"strings"

	"github.com/z-chenhao/assistant/internal/delivery/ui"
)

type widgetReader interface {
	WebWidget(context.Context, string) (ui.Widget, error)
}

func (s *Server) widget(w http.ResponseWriter, r *http.Request) {
	reader, ok := s.Core.(widgetReader)
	if !ok {
		fail(w, 503)
		return
	}
	doc, err := reader.WebWidget(r.Context(), r.URL.Query().Get("ref"))
	visual, valid := ui.Sandbox(doc)
	if err != nil || !valid {
		fail(w, 404)
		return
	}
	// CSP sandbox applies to every HTML response. Direct browser navigation
	// receives only text, keeping generated code inside the Host-owned frame.
	w.Header().Set("Content-Security-Policy", "sandbox allow-scripts; default-src 'none'; script-src 'unsafe-inline'; style-src 'unsafe-inline'; font-src data:; img-src data:; connect-src 'none'; frame-src 'none'; form-action 'none'; base-uri 'none'; frame-ancestors 'self'")
	w.Header().Set("Origin-Agent-Cluster", "?1")
	w.Header().Set("Permissions-Policy", "camera=(), microphone=(), geolocation=(), clipboard-read=(), clipboard-write=()")
	w.Header().Set("Referrer-Policy", "no-referrer")
	if r.Header.Get("Sec-Fetch-Dest") == "document" {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		_, _ = fmt.Fprint(w, doc.Fallback)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	// The upstream Websandbox bridge only proposes bounded text/links and resize events; the parent
	// requires its own visible Send button before accepting any user input.
	// Bootstrap precedes all generated CSS/HTML, which may contain closing tags.
	theme := "light"
	if r.URL.Query().Get("theme") == "dark" {
		theme = "dark"
	}
	styles, _ := assets.ReadFile("assets/widget.css")
	runtime, err := assets.ReadFile("assets/widget-runtime.js")
	if err != nil {
		fail(w, 503)
		return
	}
	meta, _ := json.Marshal(map[string]any{"type": "open-generative-ui", "content": map[string]any{"initialHeight": visual.InitialHeight, "generating": false, "css": visual.CSS, "cssComplete": true, "html": []string{visual.HTML}, "htmlComplete": true, "jsFunctions": visual.JSFunctions, "jsFunctionsComplete": true, "jsExpressions": visual.JSExpressions, "jsExpressionsComplete": true}})
	_, _ = fmt.Fprint(w, `<!doctype html><html class="ogui-theme" data-widget-theme="`+theme+`"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1"><title>`+html.EscapeString(doc.Title)+`</title><script>
(()=>{
for(const name of ['RTCPeerConnection','webkitRTCPeerConnection','Worker','SharedWorker']){try{Object.defineProperty(window,name,{value:undefined,writable:false,configurable:false})}catch{}}
const channel='open-generative-ui';let id=0,hydrated=false,restoring=false,interacted=false,gesture=-Infinity,local={},modelContext={},stateResolve,lastSaved='';
const stateReady=new Promise(resolve=>stateResolve=resolve),pending=new Map();
const send=(type,value={})=>parent.postMessage({channel,type,...value},'*');
const invoke=(method,args)=>new Promise(resolve=>{const key=String(++id);pending.set(key,resolve);send('invoke',{id:key,method,args})});
const controls=()=>Array.from(document.querySelectorAll('input:not([type=password]):not([type=hidden]):not([type=file]),textarea,select'));
const key=(node,index)=>node.dataset.stateKey||node.id||node.name||node.tagName.toLowerCase()+':'+index;
function capture(){const values={},context={};controls().forEach((node,index)=>{const name=key(node,index),value=node.type==='checkbox'||node.type==='radio'?node.checked:node.multiple?Array.from(node.selectedOptions,o=>o.value):node.value;values[name]=value;context[(node.getAttribute('aria-label')||node.labels?.[0]?.innerText||name).slice(0,128)]=value});const outputs={};document.querySelectorAll('output,[data-state-output]').forEach((node,index)=>outputs[node.id||String(index)]=node.textContent.slice(0,4096));return {state:{controls:values,outputs,local,interacted},model_context:interacted?{controls:context,widget:modelContext}:{}}}
function save(event){if(!hydrated||restoring)return;if(event?.type==='input'||event?.type==='change')interacted=true;const value=capture(),snapshot=JSON.stringify(value);if(snapshot===lastSaved)return;lastSaved=snapshot;send('view-state',value)}
function restore(state){restoring=true;local=state.local||{};interacted=state.interacted===true;controls().forEach((node,index)=>{const name=key(node,index);if(!Object.prototype.hasOwnProperty.call(state.controls||{},name))return;const value=state.controls[name];if(node.type==='checkbox'||node.type==='radio')node.checked=!!value;else if(node.multiple&&Array.isArray(value))Array.from(node.options,o=>o.selected=value.includes(o.value));else node.value=String(value??'');node.dispatchEvent(new Event('input',{bubbles:true}));node.dispatchEvent(new Event('change',{bubbles:true}))});document.querySelectorAll('output,[data-state-output]').forEach((node,index)=>{const value=state.outputs?.[node.id||String(index)];if(typeof value==='string')node.textContent=value});restoring=false;hydrated=true;stateResolve(local);reportHeight();send('restored',{graphic:!!document.querySelector('svg')})}
function trusted(method,args){if(performance.now()-gesture>1200)return Promise.resolve({accepted:false,error:'USER_GESTURE_REQUIRED'});gesture=-Infinity;if(hydrated)send('view-state',capture());return invoke(method,args)}
Object.defineProperty(window,'Websandbox',{value:Object.freeze({connection:Object.freeze({remote:Object.freeze({sendPrompt:args=>invoke('sendPrompt',args),openLink:args=>invoke('openLink',args),getState:()=>stateReady.then(()=>local),setState:async args=>{await stateReady;local=args?.state??args??{};if(args?.modelContext){modelContext=args.modelContext;interacted=true;}save();return {accepted:true}},updateModelContext:async value=>{await stateReady;modelContext=value??{};interacted=true;save();return {accepted:true}},callTool:args=>trusted('callTool',args),requestDisplayMode:args=>trusted('displayMode',args),requestModal:()=>trusted('displayMode',{mode:'modal'})})})}),writable:false,configurable:false});
function exportView(format){
 const snapshot=capture();if(format==='csv')return JSON.stringify([['Field','Value'],...Object.entries(snapshot.state.controls).map(([k,v])=>[k,Array.isArray(v)?v.join(', '):String(v)]),...Object.entries(snapshot.state.outputs)]);
 if(format==='html'){const copy=document.body.cloneNode(true);copy.querySelectorAll('script').forEach(node=>node.remove());copy.querySelectorAll('input,textarea,select').forEach((node,index)=>{const source=document.body.querySelectorAll('input,textarea,select')[index];node.disabled=true;if(node.tagName==='TEXTAREA')node.textContent=source.value;else if(node.tagName==='SELECT')Array.from(node.options,(o,i)=>o.toggleAttribute('selected',source.options[i].selected));else{node.setAttribute('value',source.value);node.toggleAttribute('checked',source.checked)}});copy.querySelectorAll('button').forEach(node=>node.disabled=true);return '<!doctype html><html class="ogui-theme" data-widget-theme="'+document.documentElement.dataset.widgetTheme+'"><head><meta charset="utf-8"><meta http-equiv="Content-Security-Policy" content="default-src &apos;none&apos;; style-src &apos;unsafe-inline&apos;; font-src data:; img-src data:; form-action &apos;none&apos;; base-uri &apos;none&apos;"><meta name="viewport" content="width=device-width,initial-scale=1"><style>'+Array.from(document.querySelectorAll('style'),node=>node.textContent).join('\n')+'</style></head><body>'+copy.innerHTML+'</body></html>'}
 const svg=document.querySelector('svg');if(!svg)throw Error('No SVG to export');const clone=svg.cloneNode(true),original=[svg,...svg.querySelectorAll('*')],copies=[clone,...clone.querySelectorAll('*')];original.forEach((node,i)=>{const style=getComputedStyle(node);for(const name of ['fill','stroke','stroke-width','font-family','font-size','font-weight','opacity','text-anchor'])copies[i].setAttribute(name,style.getPropertyValue(name))});const box=svg.getBoundingClientRect();clone.setAttribute('xmlns','http://www.w3.org/2000/svg');clone.setAttribute('width',String(box.width));clone.setAttribute('height',String(box.height));return new XMLSerializer().serializeToString(clone)
}
addEventListener('click',event=>{if(event.isTrusted){gesture=performance.now();interacted=true;}},true);
addEventListener('input',save);addEventListener('change',save);addEventListener('pagehide',()=>{if(hydrated)send('view-state',capture())});
addEventListener('keydown',event=>{if(event.key==='Escape')send('exit',{});if(event.target===document.documentElement&&!event.altKey&&!event.ctrlKey&&!event.metaKey&&['ArrowLeft','ArrowRight'].includes(event.key)){document.documentElement.scrollBy({left:event.key==='ArrowRight'?80:-80,behavior:'instant'});event.preventDefault()}});
addEventListener('message',event=>{if(event.source!==parent||event.data?.channel!==channel)return;const value=event.data;
 if(value.type==='theme'){document.documentElement.dataset.widgetTheme=value.theme==='dark'?'dark':'light';for(const [key,color] of Object.entries(value.tokens||{})){if((key.startsWith('--color-')||key==='--font-sans')&&typeof color==='string'&&color.length<512)document.documentElement.style.setProperty(key,color)}requestAnimationFrame(reportHeight)}
 if(value.type==='restore'){restore(value.state||{});modelContext=value.modelContext?.widget||{}}
 if(value.type==='capture'){send('view-state',{...capture(),id:value.id})}
 if(value.type==='export'){try{send('exported',{id:value.id,format:value.format,data:exportView(value.format)})}catch{send('exported',{id:value.id,error:'Export unavailable.'})}}
 if(value.type==='result'&&pending.has(value.id)){pending.get(value.id)(value.result);pending.delete(value.id)}
});
addEventListener('error',event=>{send('error',{});event.preventDefault()});addEventListener('unhandledrejection',event=>{send('error',{});event.preventDefault()});
function scrollRegions(){
 const root=document.documentElement;const wide=root.scrollWidth>root.clientWidth+1;
 root.tabIndex=wide?0:-1;if(wide){root.setAttribute('role','region');root.setAttribute('aria-label',document.title)}else{root.removeAttribute('role');root.removeAttribute('aria-label')}
 document.querySelectorAll('body *').forEach(node=>{const style=getComputedStyle(node),scrollable=node.scrollWidth>node.clientWidth+1&&['auto','scroll'].includes(style.overflowX);if(scrollable){node.dataset.uiHorizontal='';if(!node.matches('input,textarea,select,button,a[href],[tabindex]')){node.tabIndex=0;node.dataset.uiScrollFocus=''}}else{delete node.dataset.uiHorizontal;if(node.hasAttribute('data-ui-scroll-focus')){node.removeAttribute('tabindex');delete node.dataset.uiScrollFocus}}})
}
let layoutTimer;function scheduleLayout(){clearTimeout(layoutTimer);layoutTimer=setTimeout(reportHeight,100)}
let last=0;function reportHeight(){scrollRegions();const height=Math.ceil(document.body.scrollHeight);if(height!==last){last=height;send('resize',{height})}}
addEventListener('DOMContentLoaded',()=>{document.fonts.ready.then(()=>{send('ready',{});reportHeight()});new ResizeObserver(reportHeight).observe(document.body);new MutationObserver(()=>{save();scheduleLayout()}).observe(document.body,{subtree:true,childList:true,characterData:true});addEventListener('resize',reportHeight);addEventListener('load',reportHeight);reportHeight()});
})();
</script><script>`+strings.ReplaceAll(string(runtime), "</script", "<\\/script")+`</script><script type="application/json" id="open-generative-ui-activity">`+string(meta)+`</script><style>`+string(styles)+`
html,body{height:auto!important;min-height:0!important}html{overflow-x:auto!important;overflow-y:hidden!important}body{overflow:visible!important;margin:0;padding:0;background:var(--color-background-primary);color:var(--color-text-primary);font-family:var(--font-sans)}*{box-sizing:border-box}svg,img,canvas{max-width:100%}@media(prefers-reduced-motion:reduce){*,*::before,*::after{animation:none!important;transition:none!important}}
`+visual.CSS+`
html,[data-ui-horizontal]{scrollbar-width:none}html::-webkit-scrollbar,[data-ui-horizontal]::-webkit-scrollbar{display:none}html:focus-visible,[data-ui-scroll-focus]:focus-visible{outline:1px solid var(--color-border-primary);outline-offset:-1px}
</style></head><body>`+visual.HTML+`<script>`+visual.JSFunctions+`</script><script>`+strings.Join(visual.JSExpressions, ";\n")+`</script></body></html>`)
}

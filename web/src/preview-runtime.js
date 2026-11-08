import {morphChildren} from './lib/morph-children';
// Steward-owned display updater. The frame has no generated script, bridge,
// storage or network access. Only its parent can send sanitized snapshots.
const config=JSON.parse(document.getElementById('preview-config').textContent);
const content=document.getElementById('preview-content'),style=document.getElementById('preview-style');
let revision=-1;
function report(){parent.postMessage({channel:'steward-preview',key:config.key,type:'height',height:Math.ceil(document.body.scrollHeight)},'*');}
addEventListener('message',event=>{
  const data=event.data;
  if(event.source!==parent||data?.channel!=='steward-preview'||data.key!==config.key||!Number.isSafeInteger(data.revision)||data.revision<revision||typeof data.html!=='string'||data.html.length>160000||typeof data.css!=='string'||data.css.length>24000)return;
  revision=data.revision;
  document.documentElement.dataset.widgetTheme=data.theme==='dark'?'dark':'light';
  if(style.textContent!==data.css)style.textContent=data.css;
  const next=document.createElement('template');next.innerHTML=data.html;
  morphChildren(content,next.content);
  report();
});
new ResizeObserver(report).observe(document.body);
document.fonts.ready.then(report);
parent.postMessage({channel:'steward-preview',key:config.key,type:'ready'},'*');

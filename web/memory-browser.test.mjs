import test from 'node:test';
import assert from 'node:assert/strict';
import {buildSync} from 'esbuild';
import {runInNewContext} from 'node:vm';
const code=buildSync({entryPoints:[new URL('src/memory-browser.ts',import.meta.url).pathname],bundle:true,format:'cjs',write:false}).outputFiles[0].text;
const revision='sha256:'+'a'.repeat(64);
function setup(){
 const listeners=new Map(),timers=new Map();let timerID=0,port,removed=false;
 const frame={hidden:false,setAttribute(k,v){this[k]=v},remove(){removed=true},contentWindow:{postMessage(message,origin,ports){assert.equal(message.channel,'steward-memory-browser');assert.equal(origin,'*');assert.equal(ports.length,1)}}};
 class Channel{constructor(){port=this.port1={sent:[],postMessage(v){this.sent.push(v)},start(){},close(){this.closed=true}};this.port2={}}}
 const module={exports:{}};
 runInNewContext(code,{module,exports:module.exports,MessageChannel:Channel,document:{createElement:()=>frame,body:{append(v){assert.equal(v,frame)}}},window:{addEventListener(k,fn){listeners.set(k,fn)},removeEventListener(k){listeners.delete(k)}},setTimeout(fn){timers.set(++timerID,fn);return timerID},clearTimeout(id){timers.delete(id)}});
 return {connect:module.exports.connectMemoryBrowser,frame,listeners,timers,get port(){return port},get removed(){return removed},loaded(origin='null',source=frame.contentWindow){listeners.get('message')?.({source,origin,data:{channel:'steward-memory-browser',type:'loaded'}})},reply(data){port.onmessage({data})}};
}
const ready={type:'ready',seq:1,compact_width:760,sheet_modal:false,storage_key:'steward.memory.peek-width.v1',labels:{details:'Memory details',previous:'Previous memory',next:'Next memory',resize_sources:'Resize memory sources'},wide_open:true};
async function connected(h){const pending=h.connect(revision,{rail_present:true,rail_open:true});h.loaded();h.reply({type:'connected'});assert.deepEqual(JSON.parse(JSON.stringify(h.port.sent[0])),{type:'init',rail_present:true,rail_open:true,seq:1});h.reply(ready);return pending}
test('Memory transport binds opaque frame, frozen version, typed facts and lifecycle',async()=>{
 const h=setup(),promise=h.connect(revision,{rail_present:false,rail_open:false});
 assert.equal(h.frame.sandbox,'allow-scripts');assert.equal(h.frame.src,'/api/developer/memory/program?inspector_revision='+encodeURIComponent(revision));
 h.loaded('http://localhost');assert.equal(h.port,undefined);h.loaded('null',{});assert.equal(h.port,undefined);
 h.loaded();h.reply({type:'connected'});h.reply(ready);const browser=await promise;
 assert.equal(browser.defaults.compact_width,760);
 const call=browser.call({type:'history',kind:'close',list_page:true,has_ref:true,persisted:false,depth:2});
 h.reply({type:'history',mode:'back',steps:2,seq:2});assert.equal((await call).steps,2);
 const pending=browser.call({type:'fit',rail_present:true,rail_open:true,compact:false,wide_open:true});
 browser.close();await assert.rejects(pending,/unavailable/);assert.equal(h.removed,true);assert.equal(h.port.closed,true);assert.equal(h.timers.size,0);await assert.rejects(browser.call({type:'init',rail_present:false,rail_open:false}),/unavailable/);
});
test('Memory transport rejects forged sequence and failed Worker without unresolved requests',async()=>{
 for(const reply of [{seq:999,type:'fit'},{seq:2,type:'failed'},null]){const h=setup(),browser=await connected(h);const pending=browser.call({type:'history',kind:'close',list_page:true,has_ref:true,persisted:false,depth:1});h.reply(reply);await assert.rejects(pending,/unavailable/);assert.equal(h.removed,true)}
});
test('Memory startup validates defaults and terminates timed out Worker',async()=>{
 for(const changes of [{compact_width:1},{storage_key:'credentials'},{sheet_modal:'false'},{labels:null},{labels:{...ready.labels,details:''}},{labels:{...ready.labels,details:'x'.repeat(513)}},{labels:{...ready.labels,extra:'authority'}}]){const h=setup(),pending=h.connect(revision,{rail_present:true,rail_open:true});h.loaded();h.reply({type:'connected'});h.reply({...ready,...changes});await assert.rejects(pending,/unavailable/);assert.equal(h.removed,true)}
 const h=setup(),browser=await connected(h);const pending=browser.call({type:'history',kind:'close',list_page:true,has_ref:true,persisted:false,depth:1});[...h.timers.values()][0]();await assert.rejects(pending,/unavailable/);assert.equal(h.removed,true);
 await assert.rejects(setup().connect('wrong',{rail_present:false,rail_open:false}),/unavailable/);
});

test('Memory transport accepts independently owned English labels',async()=>{
 const h=setup(),pending=h.connect(revision,{rail_present:true,rail_open:true});h.loaded();h.reply({type:'connected'});h.reply({...ready,labels:{...ready.labels,details:'Independent memory details'}});const browser=await pending;assert.equal(browser.defaults.labels.details,'Independent memory details');browser.close();
});

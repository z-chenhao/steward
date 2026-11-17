import test from 'node:test';
import assert from 'node:assert/strict';
import {readFileSync} from 'node:fs';
import {runInNewContext} from 'node:vm';
const source=readFileSync(new URL('../internal/plugin/memoryinspector/view/memory-worker.js',import.meta.url),'utf8');
const original=JSON.parse(readFileSync(new URL('../tests/fixtures/plugins/memory-inspector-original-v1/client-decisions.json',import.meta.url),'utf8'));
let seq=0,reply;const self={postMessage:v=>reply=JSON.parse(JSON.stringify(v))};runInNewContext(source,{self});
const call=(type,input)=>{reply=null;const id=++seq;self.onmessage({data:{type,...input,seq:id}});assert.equal(reply.seq,id);assert.notEqual(reply.type,'failed');const {type:kind,seq:n,...result}=reply;return result};
test('Memory fit and row-click decisions match executed original client branches',()=>{
 for(const c of original.fit)assert.deepEqual(call('fit',c.input),c.expected);
 for(const c of original.click)assert.deepEqual(call('click',c.input),c.expected);
});
test('Memory commit retains original adjacent links, history and same-record scroll',()=>{
 for(const c of original.navigation){const got=call('commit',{...c.input,list_page:true,same_record:false,has_focused:false,push:false,depth:null});for(const [k,v]of Object.entries(c.expected))assert.equal(got[k],v)}
 for(const c of original.push){const got=call('commit',{index:0,count:1,same_record:false,has_focused:false,...c.input});assert.equal(got.history_push,c.expected.push);assert.equal(got.history_depth,c.expected.depth)}
 for(const c of original.restore_source){const got=call('commit',{index:0,count:1,list_page:true,has_focused:false,push:false,depth:null,same_record:c.same_record});assert.equal(got.restore_source,c.expected)}
});
test('Memory close/pop/pageshow history decisions match executed original client',()=>{for(const c of original.history)assert.deepEqual(call('history',c.input),c.expected)});
test('Memory worker fails malformed facts and exposes no DOM/effect operation',()=>{
 for(const request of [{type:'effect',seq:1},{type:'commit',index:500,count:1,seq:2},{type:'click',anchor_kind:'external',seq:3},{type:'fit',rail_present:'true',seq:4},{type:'init',seq:0}]){self.onmessage({data:request});assert.equal(reply.type,'failed')}
 assert.deepEqual(call('init',{rail_present:true,rail_open:true}),{compact_width:760,sheet_modal:false,storage_key:'steward.memory.peek-width.v1',labels:{details:'Memory details',previous:'Previous memory',next:'Next memory',resize_sources:'Resize memory sources'},wide_open:true});
});

test('Memory client labels and adjacent visibility remain original feature policy',()=>{
 const archived=readFileSync(new URL('../tests/fixtures/plugins/memory-inspector-original-v1/memory.ts.txt',import.meta.url),'utf8');
 const defaults=call('init',{rail_present:true,rail_open:true});
 for(const [key,text] of Object.entries({details:'Memory details',previous:'Previous memory',next:'Next memory',resize_sources:'Resize memory sources'})){assert.equal(defaults.labels[key],text);assert.ok(archived.includes("t('"+text+"')"))}
 assert.ok(archived.includes('sheet.previous.hidden = false; sheet.next.hidden = false;'));
 const plan=call('commit',{list_page:true,same_record:false,has_focused:false,push:false,count:1,index:0,depth:null});assert.equal(plan.previous_hidden,false);assert.equal(plan.next_hidden,false);
});

import test from 'node:test';
import assert from 'node:assert/strict';
import {readFileSync} from 'node:fs';
import vm from 'node:vm';

const program=readFileSync(new URL('../internal/plugin/appsinterface/view/browser-worker.js',import.meta.url),'utf8');
// Captured original presentation, independent of the candidate producer.
const original=JSON.parse(readFileSync(new URL('../tests/fixtures/plugins/apps-interface-profile-native-v1.json',import.meta.url),'utf8'));
function start(profile=original){
 const replies=[];
 const context=vm.createContext({postMessage:value=>replies.push(structuredClone(value))});
 new vm.Script(program).runInContext(context,{timeout:1000});
 let seq=0;
 const call=value=>{const expected=++seq;context.input=structuredClone({...value,seq:expected});vm.runInContext('onmessage({data:input})',context,{timeout:1000});const reply=replies.shift();assert.equal(reply?.seq,expected);return reply};
 assert.equal(call({type:'init',presentation:profile}).type,'ready');
 return call;
}

test('Apps owning browser reconciles real mixed account states without receiving identities',()=>{
 const call=start();
 const reply=call({type:'project',accounts:[
  {slug:'mail',state:'pending',public:false},
  {slug:'drive',state:'active',public:true},
  {slug:'mail',state:'active',public:false},
  {slug:'mail',state:'active',public:true},
 ],tiles:['mail','drive','calendar'],details:[
  {slug:'mail',suggested:false,no_auth:false,require_alias:false},
  {slug:'drive',suggested:false,no_auth:true,require_alias:false},
  {slug:'calendar',suggested:false,no_auth:false,require_alias:false},
  {slug:'calendar',suggested:false,no_auth:false,require_alias:true},
 ]});
 assert.deepEqual(reply.tiles,[{manage_hidden:false,add_hidden:true},{manage_hidden:false,add_hidden:true},{manage_hidden:true,add_hidden:false}]);
 assert.deepEqual(reply.details,[
  {accounts:[0,2,3],empty_hidden:true,first_hidden:true,additional_hidden:false},
  {accounts:[1],empty_hidden:true,first_hidden:true,additional_hidden:true},
  {accounts:[],empty_hidden:false,first_hidden:false,additional_hidden:true},
  {accounts:[],empty_hidden:false,first_hidden:true,additional_hidden:false},
 ]);
 assert.equal(JSON.stringify(reply).includes('authorization'),false);
});

test('Apps browser poll deadline is retained and exact expiry does not restart a window',()=>{
 const call=start();
 const first=call({type:'poll',now:100,state:'pending'});
 assert.equal(first.schedule,true);assert.equal(first.deadline,600100);assert.equal(first.delay,5000);
 const expired=call({type:'poll',now:first.deadline,deadline:first.deadline,state:'pending'});
 assert.equal(expired.schedule,false);assert.equal(expired.deadline,first.deadline);assert.match(expired.notice,/paused/);
 for(const facts of [{stopped:true},{scheduled:true},{in_flight:true},{state:'active'}]){
  const reply=call({type:'poll',now:100,state:'pending',...facts});assert.equal(reply.schedule,false);assert.equal(reply.notice,'');
 }
});

test('Apps browser retains failure, authorization and disconnect notices',()=>{
 const call=start();
 assert.deepEqual(call({type:'poll-result',state:'expired'}),{seq:2,type:'notice',text:'Authorization expired. Reconnect the account to continue.',error:true});
 assert.equal(call({type:'action-result',action:'disconnect',authorization:false}).notice,'Account disconnected.');
 const blocked=call({type:'action-result',action:'connect',authorization:true,popup_opened:false,dialog_open:false,detail_page:false});
 assert.match(blocked.notice,/blocked/);assert.equal(blocked.open_details,true);
 assert.equal(call({type:'action-result',action:'connect',authorization:true,popup_opened:true,dialog_open:true,detail_page:false}).open_details,false);
 assert.equal(call({type:'action-failed',code:'alias_required'}).reveal_alias,true);
 assert.equal(call({type:'action-failed',code:'unknown'}).reveal_alias,false);
});

test('Apps candidate browser decisions change independently of the fixed DOM consumer',()=>{
 const changed=structuredClone(original);changed.browser.poll_interval_ms=10000;changed.browser.open_details_after_authorization=false;changed.browser.reveal_alias=false;
 const call=start(changed);
 assert.equal(call({type:'poll',now:100,state:'pending'}).delay,10000);
 assert.equal(call({type:'action-result',action:'connect',authorization:true,popup_opened:true,dialog_open:false,detail_page:false}).open_details,false);
 assert.equal(call({type:'action-failed',code:'alias_required'}).reveal_alias,false);
});

test('Apps plans more than 32 pending accounts in one positional request',()=>{
 const call=start();
 const items=Array.from({length:96},(_,at)=>({state:'pending',now:100,deadline:1000+at,scheduled:at===0,in_flight:at===1}));
 const result=call({type:'polls',items});
 assert.equal(result.type,'poll-plans');assert.equal(result.plans.length,96);
 assert.equal(result.plans[0].schedule,false);assert.equal(result.plans[1].schedule,false);
 assert.equal(result.plans[95].schedule,true);assert.equal(result.plans[95].deadline,1095);
});

import {test} from 'node:test';
import assert from 'node:assert/strict';
import {randomUUID} from 'node:crypto';
import {request} from 'node:http';
import {startFixture} from './account-fixture.mjs';
import {nativeFixture} from './native-fixture.mjs';

const API = 'https://api.openai.com/v1';

async function account(t, observe){
 const peer=await startFixture();t.after(()=>peer.close());const {rpc,handle}=await nativeFixture(t,(url,init)=>{
  observe?.(url,init);
  return peer.fetch(url,init);
 });
 const start=()=>rpc('auth.start',{device_id:randomUUID()});
 const finish=async()=>{for(let i=0;i<200;i++){const next=await rpc('auth.continue');if(next.status==='connected')return next.credential;await new Promise(resolve=>setTimeout(resolve,5));}throw Error('native login timeout');};
 const complete=async login=>{const callback=await peer.authorize(login.events[0].url);assert.equal((await fetch(callback)).status,200);return finish();};
 return {peer,rpc,handle,start,complete,finish};
}
test('native Go PKCE, JWT verification, credential rotation, catalog and revocation',async t=>{
 const f=await account(t);const login=await f.start();const u=new URL(login.events[0].url);assert.equal(u.searchParams.get('code_challenge_method'),'S256');
 const wrong=new URL(u.searchParams.get('redirect_uri'));assert.equal(wrong.hostname,'127.0.0.1');wrong.search='state=wrong&code=wrong';assert.equal((await fetch(wrong)).status,400);
 const callback=await f.peer.authorize(u);
 const invalidHost=await new Promise((resolve,reject)=>{
  const req=request(callback,{headers:{host:'invalid.test'}},res=>{res.resume();resolve(res.statusCode);});
  req.on('error',reject);req.end();
 });
 assert.equal(invalidHost,400);assert.equal(f.peer.stats.exchanges,0);
 assert.equal((await fetch(callback)).status,200);assert.equal((await fetch(callback)).status,400);
 const c=await f.finish();await assert.rejects(f.rpc('auth.continue'),/AUTH_REQUIRED/);assert.equal(f.peer.stats.exchanges,1);
 const connection={endpoint:API,access_token:c.access_token};
 assert.deepEqual((await f.rpc('models.list',{connection})).map(m=>m.model_id),['fixture-account-model']);
 await f.peer.control({models:['next-model']});
 assert.deepEqual((await f.rpc('models.list',{connection})).map(m=>m.model_id),['next-model']);
 await assert.rejects(f.rpc('models.list',{connection:{...connection,endpoint:'https://invalid.test/v1'}}),/MODEL_INCOMPATIBLE/);
 const refreshed=await f.rpc('auth.refresh',{credential:c});assert.notEqual(refreshed.refresh_token,c.refresh_token);assert.equal(refreshed.subject_hash,c.subject_hash);
 await assert.rejects(f.rpc('auth.refresh',{credential:c}),/AUTH_REQUIRED/);
 const returning=await f.rpc('auth.start',{device_id:randomUUID(),registration:{client_id:refreshed.client_id,subject_hash:refreshed.subject_hash},credential:refreshed});
 const authorize=new URL(returning.events[0].url);
 assert.equal(authorize.searchParams.get('client_id'),c.client_id);assert.equal(authorize.searchParams.has('agent_name_hint'),false);assert.equal(authorize.searchParams.get('id_token_hint'),refreshed.id_token);
 const next=await f.complete(returning);assert.equal(next.subject_hash,c.subject_hash);
 assert.equal((await f.rpc('auth.logout',{connection_id:'c',credential:next})).revoked,true);
});
for(const [name,claims] of Object.entries({issuer:{iss:'https://evil.test'},audience:{aud:'other'},nonce:{nonce:'wrong'},expired:{exp:1},future:{iat:9999999999},subject:{sub:''}}))test('native Go rejects ID token '+name,async t=>{const f=await account(t);await f.peer.control({claims});await assert.rejects(f.complete(await f.start()),/AUTH_REQUIRED/);});
for(const mode of ['signature','no-scope','platform-key'])test('native Go rejects grant '+mode,async t=>{const f=await account(t);await f.peer.control({mode});await assert.rejects(f.complete(await f.start()),/AUTH_REQUIRED/);});
test('native Go cancel closes callback; dynamic registration stays bound to identity',async t=>{
 const f=await account(t);const login=await f.start();const callback=await f.peer.authorize(login.events[0].url);await f.rpc('auth.cancel');await assert.rejects(fetch(callback));assert.equal(f.peer.stats.exchanges,0);
 const c=await f.complete(await f.start());const next=await f.rpc('auth.start',{device_id:randomUUID(),registration:{client_id:c.client_id,subject_hash:c.subject_hash}});await f.peer.control({claims:{sub:'substitution'}});await assert.rejects(f.complete(next),/AUTH_REQUIRED/);
});

test('native returning callback rejects a different client and consumes OAuth errors',async t=>{
 const f=await account(t);
 const c=await f.complete(await f.start());
 const returning=await f.rpc('auth.start',{device_id:randomUUID(),registration:{client_id:c.client_id,subject_hash:c.subject_hash}});
 const altered=new URL(await f.peer.authorize(returning.events[0].url));
 altered.searchParams.set('client_id','oaiapp_other');
 assert.equal((await fetch(altered)).status,400);await assert.rejects(f.rpc('auth.continue'),/AUTH_REQUIRED/);
 const next=new URL((await f.start()).events[0].url);
 const valid=await f.peer.authorize(next);
 const denied=new URL(valid);denied.search=new URLSearchParams({state:next.searchParams.get('state'),error:'denied'});
 assert.equal((await fetch(denied)).status,400);assert.equal((await fetch(valid)).status,400);
 await assert.rejects(f.rpc('auth.continue'),/AUTH_REQUIRED/);assert.equal(f.peer.stats.exchanges,1);
});

test('native account rejects redirects and classifies malformed catalogs',async t=>{
 const requests=[];
 let catalog='not JSON';
 const {rpc}=await nativeFixture(t,url=>{
  const path=new URL(url).pathname;requests.push(path);
  if(path==='/v1/models')return new Response(catalog);
  return new Response('private fixture material',{status:307,headers:{location:'https://auth.openai.com/redirected'}});
 });
 await assert.rejects(rpc('auth.refresh',{credential:{client_id:'client',refresh_token:'fixture',subject_hash:'hash'}}),{message:'AUTH_REQUIRED'});
 assert.deepEqual(requests,['/api/accounts/oauth/token']);
 for(const body of ['not JSON','{"models":[{"slug":5,"visibility":"list"}]}','{"models":{}}']){
  catalog=body;
  await assert.rejects(rpc('models.list',{connection:{endpoint:API,access_token:'owned-fixture'}}),{message:'INVALID_PROVIDER_RESPONSE'});
 }
});

test('native Go account continuation replays the fixed steward namespace on the wire',{timeout:10000},async t=>{
 let captured;
 const f=await account(t,(url,init)=>{
  if(new URL(url).pathname!=='/v1/responses')return;
  const body=JSON.parse(init.body);
  if(body.input.some(i=>i.type==='function_call_output'))captured=body;
 });
 const credential=await f.complete(await f.start());
 const run=async(method,params)=>{
  const events=[];
  let terminal;
  const done=new Promise(resolve=>{terminal=resolve;});
  const result=await f.handle({jsonrpc:'2.0',method,params},frame=>{
   if(frame.method==='generation.event' && frame.params.generation_id===params.generation_id){
    events.push(frame.params);
    if(['completed','error'].includes(frame.params.kind))terminal();
   }
  });
  assert.equal(result.error,undefined);
  await done;assert.equal(events.at(-1).kind,'completed');return events;
 };
 const r={generation_id:'first',run_id:'run',session_id:'run',connection:{endpoint:API,access_token:credential.access_token,connection_id:'account'},
  model_snapshot:{snapshot_id:'s',provider_kind:'openai_account',wire_api:'openai_responses',endpoint_hash:'',model_id:'fixture-account-model',context_window:8192,max_output_tokens:1024,compatibility:{tool_result_format:'openai'}},
  messages:[{role:'user',text:'Call probe_echo'}],tools:[{name:'probe_echo',input_schema:'{"type":"object","properties":{"value":{"type":"string"}}}'}]};
 const first=await run('generation.start',r);
 const call=first.find(e=>e.kind==='tool_call').tool_call;
 r.generation_id='second';
 r.messages.push({role:'assistant',tool_calls:[call]},{role:'tool',tool_call_id:call.id,text:'PROBE_RESULT_OK'});
 await run('generation.continue',r);
 const wireCall=captured.input.find(i=>i.type==='function_call');
 assert.equal(captured.tools[0].name,'steward');
 assert.equal(wireCall.namespace,'steward');
 assert.equal(wireCall.name,call.name);assert.equal(wireCall.arguments,call.arguments);
 assert.equal(captured.input.find(i=>i.type==='function_call_output').call_id,wireCall.call_id);
 assert.equal(f.peer.stats.request_valid,true);
});

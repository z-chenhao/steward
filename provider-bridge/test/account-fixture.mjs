// Deterministic local OAuth/Responses peer. Keys and tokens exist only in memory.
import http from 'node:http';
import { generateKeyPairSync, sign, randomBytes, createHash } from 'node:crypto';
import { once } from 'node:events';
import { fileURLToPath } from 'node:url';

export async function startFixture() {
  const {privateKey,publicKey}=generateKeyPairSync('rsa',{modulusLength:2048});
  const jwk={...publicKey.export({format:'jwk'}),kid:'owned-fixture',alg:'RS256',use:'sig'};
  const refreshes=new Map(),accesses=new Map();
  const stats={exchanges:0,refreshes:0,revocations:0,catalogs:0,generations:0,closed:0,request_valid:true};
  let mode='success',claimsPatch={},models=['fixture-account-model'], lastAuthorize;
  const jwt=claims=>{
    const h=Buffer.from(JSON.stringify({alg:'RS256',kid:jwk.kid})).toString('base64url');
    const p=Buffer.from(JSON.stringify(claims)).toString('base64url');
    return h+'.'+p+'.'+sign('sha256',Buffer.from(h+'.'+p),privateKey).toString('base64url');
  };
  const credential=(client,subject,nonce)=>{
    const access='fixture-access-'+randomBytes(24).toString('hex'),refresh='fixture-refresh-'+randomBytes(24).toString('hex');
    refreshes.set(refresh,{client,subject});accesses.set(access,{client,subject});
    const now=Math.floor(Date.now()/1000);
    return {access_token:mode==='platform-key'?'sk-fixture-forbidden':access,refresh_token:refresh,token_type:'Bearer',expires_in:3600,scope:mode==='no-scope'?'openid profile':'openid profile offline_access resource.invoke chatgpt.tokens.use.direct',
      id_token:jwt({iss:'https://auth.openai.com',sub:subject,aud:client,iat:now,exp:now+3600,...(nonce?{nonce}:{}),...claimsPatch})+(mode==='signature'?'wrong':'')};
  };
  const server=http.createServer(async(req,res)=>{
    let body='';for await(const chunk of req){body+=chunk;if(body.length>1<<20){res.writeHead(413);res.end();return;}}
    const json=(value,status=200)=>{res.writeHead(status,{'content-type':'application/json'});res.end(JSON.stringify(value));};
    try {
      if(req.url==='/fixture/control'){
        const c=JSON.parse(body);if(c.mode)mode=c.mode;if(c.claims)claimsPatch=c.claims;if(c.models)models=c.models;return json({ok:true});
      }
      if(req.url==='/fixture/stats')return json(stats);
      if(req.url==='/fixture/authorize'){
        const u=new URL(JSON.parse(body).url);lastAuthorize=u;
        const client=u.searchParams.get('client_id')==='dynamic_agent_client'?'oaiapp_owned_fixture':u.searchParams.get('client_id');
        const code=Buffer.from(JSON.stringify({nonce:u.searchParams.get('nonce'),challenge:u.searchParams.get('code_challenge'),client,subject:'owned-account'})).toString('base64url');
        const callback=new URL(u.searchParams.get('redirect_uri'));callback.search=new URLSearchParams({code,state:u.searchParams.get('state'),client_id:client}).toString();return json({callback:callback.toString()});
      }
      if(req.url==='/.well-known/jwks.json')return json({keys:[jwk]});
      if(req.url==='/.well-known/openid-configuration')return json({issuer:'https://auth.openai.com',revocation_endpoint:'https://auth.openai.com/api/accounts/oauth/revoke'});
      if(req.url==='/api/accounts/oauth/revoke'){stats.revocations++;const p=new URLSearchParams(body);refreshes.delete(p.get('token'));return json({});}
      if(req.url==='/api/accounts/oauth/token'){
        const p=new URLSearchParams(body);
        if(p.get('grant_type')==='authorization_code'){
          stats.exchanges++;const code=JSON.parse(Buffer.from(p.get('code'),'base64url'));
          if(createHash('sha256').update(p.get('code_verifier')).digest('base64url')!==code.challenge || p.get('client_id')!==code.client || p.get('redirect_uri')!==lastAuthorize?.searchParams.get('redirect_uri'))return json({},400);
          return json(credential(code.client,code.subject,code.nonce));
        }
        stats.refreshes++;
        const prior=refreshes.get(p.get('refresh_token'));refreshes.delete(p.get('refresh_token'));
        if(!prior || mode==='refresh-fail')return json({error:'invalid_grant',unsafe:'fixture-secret-marker'},400);
        if(mode==='hold-refresh'){await new Promise(r=>setTimeout(r,15000));}
        return json(credential(prior.client,prior.subject));
      }
      const auth=req.headers.authorization?.replace(/^Bearer /,'');
      if(!accesses.has(auth) || mode==='auth-loss')return json({error:'unauthorized'},401);
      if(req.url==='/v1/models'){stats.catalogs++;return json({models:[...models.map(slug=>({slug,visibility:'list',display_name:slug,context_window:8192,max_output_tokens:1024,input_modalities:['text']})),{slug:'hidden-model',visibility:'hide'}]});}
      if(req.url!=='/v1/responses')return json({},404);
      stats.generations++;const b=JSON.parse(body);
      stats.request_valid &&= b.stream===true && b.store===false && !('max_output_tokens' in b) && !('temperature' in b) && !('previous_response_id' in b) && (!b.tools?.length || b.tools.every(t=>t.type==='namespace' && typeof t.description==='string' && t.description.length>0));
      if(b.tools?.some(t=>t.type==='namespace' && !t.description))return json({error:{code:'missing_required_parameter',param:'tools[0].description'}},400);
      // Validate the actual replay wire, not just the declared Tool schema.
      if(b.input.some(i=>i.type==='function_call_output')) {
        const call=b.input.find(i=>i.type==='function_call');
        const declared=b.tools?.flatMap(t=>t.tools ?? []) ?? [];
        const valid=call?.namespace==='steward' && declared.some(t=>t.name===call.name) && b.input.some(i=>i.type==='function_call_output' && i.call_id===call.call_id);
        stats.request_valid &&= valid;
        if(!valid)return json({error:{code:'invalid_tool_namespace'}},400);
      }
      res.writeHead(200,{'content-type':'text/event-stream'});
      const emit=(type,rest)=>res.write('event: '+type+'\ndata: '+JSON.stringify({type,...rest})+'\n\n');
      emit('response.created',{response:{id:'owned-response',status:'in_progress'}});
      const hasResult=b.input.some(i=>i.type==='function_call_output');
      if(b.tools?.length && !hasResult){
        const names=b.tools[0].tools.map(t=>t.name);
        const name=names.includes('builtin__artifact_search')?'builtin__artifact_search':names.includes('builtin__codemode_execute')?'builtin__codemode_execute':names[0];
        const item={type:'function_call',id:'fc_owned',call_id:'call_owned',name,namespace:'steward',arguments:name==='builtin__codemode_execute'?JSON.stringify({source:'const value=await tools.builtin__artifact_manage({operation:"publish",artifactRefs:[]});text(JSON.stringify(value));'}):name==='builtin__artifact_search'?'{}':'{"value":"OK"}'};
        emit('response.output_item.added',{output_index:0,item:{...item,arguments:''}});
        emit('response.function_call_arguments.delta',{output_index:0,item_id:item.id,delta:item.arguments});
        emit('response.output_item.done',{output_index:0,item});
      }else{
        const text=hasResult?'PROBE_RESULT_OK':'OK ABCDEFGHIJKLMNOPQRSTUVWXYZ';
        const item={type:'message',id:'msg_owned',role:'assistant',content:[{type:'output_text',text,annotations:[]}]};
        emit('response.output_item.added',{output_index:0,item:{...item,content:[]}});
        emit('response.output_text.delta',{output_index:0,item_id:item.id,content_index:0,delta:text});
        if(mode==='hold-generation' || JSON.stringify(b.input).includes('until cancelled')){res.on('close',()=>stats.closed++);return;}
        if(mode==='partial'){res.end();return;}
        emit('response.output_item.done',{output_index:0,item});
      }
      emit(mode==='incomplete'?'response.incomplete':'response.completed',{response:{id:'owned-response',status:mode==='incomplete'?'incomplete':'completed',incomplete_details:{reason:'max_output_tokens'},usage:{input_tokens:7,output_tokens:8,total_tokens:15}}});res.end();
    }catch{res.writeHead(400);res.end();}
  });
  server.listen(0,'127.0.0.1');await once(server,'listening');
  const url=`http://127.0.0.1:${server.address().port}`;
  const network=globalThis.fetch;
  return {url,stats,control:async c=>{await network(url+'/fixture/control',{method:'POST',body:JSON.stringify(c)});},
    authorize:async u=>(await (await network(url+'/fixture/authorize',{method:'POST',body:JSON.stringify({url:u})})).json()).callback,
    fetch:(input,init)=>{const u=new URL(String(input));if(!['https://auth.openai.com','https://api.openai.com'].includes(u.origin))throw new Error('forbidden fixture network');return network(url+u.pathname+u.search,init);},
    close:async()=>{server.closeAllConnections();await new Promise(r=>server.close(r));}};
}
if(process.argv[1]===fileURLToPath(import.meta.url)){
  const fixture=await startFixture();process.stdout.write(fixture.url+'\n');
  process.on('SIGTERM',async()=>{await fixture.close();process.exit(0);});
}

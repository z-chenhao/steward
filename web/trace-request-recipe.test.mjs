import assert from 'node:assert/strict';
import test from 'node:test';
import {createHash} from 'node:crypto';
import {readFileSync} from 'node:fs';
import {transformSync,buildSync} from 'esbuild';
import {runInNewContext} from 'node:vm';
const read=path=>readFileSync(new URL(path,import.meta.url),'utf8');
const exact={exports:{}};runInNewContext(buildSync({entryPoints:[new URL('src/components/ui/read-only-json.ts',import.meta.url).pathname],bundle:true,format:'cjs',write:false}).outputFiles[0].text,{module:exact,exports:exact.exports,document:{documentElement:{lang:'en'}}});
const contract={exports:{}};runInNewContext(transformSync(read('src/trace-request-recipe.ts'),{loader:'ts',format:'cjs'}).code,{module:contract,exports:contract.exports});
const original=transformSync(read('../tests/fixtures/plugins/trace-workbench-original-v1/web__src__trace-request.ts.txt').replace(/^import .*;\n/gm,''),{loader:'ts',format:'cjs'}).code;
const current=transformSync(read('src/trace-request.ts').replace(/^import .*;\n/gm,''),{loader:'ts',format:'cjs'}).code;
const legacy=transformSync(read('src/legacy/trace-request-v1.ts').replace(/^import .*;\n/gm,''),{loader:'ts',format:'cjs'}).code;
const worker=read('../internal/plugin/traceinspector/view/trace-worker.js'),chinese=JSON.parse(read('../tests/fixtures/plugins/trace-workbench-original-v1/request-reader-labels-v1.json'));
const provenance=JSON.parse(read('../tests/fixtures/plugins/trace-workbench-original-v1/provenance.json'));
assert.equal(createHash('sha256').update(read('../tests/fixtures/plugins/trace-workbench-original-v1/request-reader-labels-v1.json')).digest('hex'),provenance.request_reader_localization.sha256);
const tick=()=>new Promise(resolve=>setImmediate(resolve));
function fixture(code,body,language='en',managed=true){
 const node=tag=>({tag,dataset:{},children:[],append(...children){this.children.push(...children)},prepend(child){this.children.unshift(child)}});
 const source=node('pre');source.textContent=body;
 const host=node('div');host.querySelector=()=>source;
 const root={dataset:managed?{traceProgramRevision:'sha256:'+'a'.repeat(64)}:{},isConnected:true,querySelectorAll:()=>[host],querySelector:selector=>selector==='[data-wire-reader]'?host:null};
 const calls=[];let closed=0,reply;
 const self={postMessage:value=>{reply=JSON.parse(JSON.stringify(value))}};runInNewContext(worker,{self},{timeout:1000});
 const entry={active:true,browser:Promise.resolve({call:request=>new Promise(resolve=>calls.push({request,resolve})),close:()=>closed++})};
 const t=key=>language==='zh-CN'?(chinese[key]??key):key;
 const native={exports:{}};runInNewContext(legacy,{module:native,exports:native.exports,t});
 const module={exports:{}};runInNewContext(code,{module,exports:module.exports,document:{documentElement:{lang:language},createElement:node},parseExactJson:exact.exports.parseExactJson,t,traceProgram:()=>entry,acceptTraceRequestRecipe:contract.exports.acceptTraceRequestRecipe,enhanceReadOnlyJson(){},setReadOnlyJsonMode(){},legacyTraceRequestRecipe(){if(managed)throw Error('native fallback');return native.exports.legacyTraceRequestRecipe()}});
 const stop=module.exports.enhanceTraceRequests(root);
 const respond=(modify=value=>value)=>{self.onmessage({data:{seq:1,...calls[0].request}});calls[0].resolve(modify(reply.result))};
 const snapshot=()=>JSON.parse(JSON.stringify({host,source}));
 return {host,source,root,entry,calls,stop,respond,snapshot,get closed(){return closed}};
}
test('managed and native request recipes preserve original DOM and source across provider shapes and languages',async()=>{
 const values=[null,[],42,{model:'private-model',instructions:'one\ntwo',messages:[{role:'system',content:'secret'},{role:'user',content:[{text:'line1'},{parts:['line2',null,0]}]}],tools:[{function:{name:'tool-name',parameters:{type:'object'}}}],temperature:.5},{system:'fallback',contents:[{parts:[{text:'gemini'}]}],tools:[{}]},[{attempt:'a',body:{input:[{type:'message',content:'responses'}]}},{attempt:'b',body:null,omitted:'saved omission'}],{instructions:null,system:'null fallback',messages:null,input:'implicit user',contents:'not used'}, {messages:[{},null,true,{role:1,type:'tool_result',parts:['a','b']},{content:{unknown:'json fallback'}}]}, {messages:[{content:'private-'+'x'.repeat(100000)}]}];
 const sources=[...values.map(value=>JSON.stringify(value)),'{"a":1,"a":2}','{"n":9007199254740993}','{"unfinished":'];
 for(const language of ['en','zh-CN'])for(const body of sources){
  const before=fixture(original,body,language),after=fixture(current,body,language),native=fixture(current,body,language,false);await tick();after.respond();await tick();
  assert.deepEqual(after.snapshot(),before.snapshot(),body.slice(0,100)+' '+language);assert.equal(after.source.textContent,body);
  assert.deepEqual(native.snapshot(),before.snapshot(),body.slice(0,100)+' '+language);assert.equal(native.source.textContent,body);
  assert.deepEqual(JSON.parse(JSON.stringify(after.calls[0].request)),{type:'request_reader',input:{language}});
  assert.equal(native.calls.length,0);
  after.stop();
 }
});
test('changed owning recipe changes actual field priority without moving raw bodies or changing source',async()=>{
 const body=JSON.stringify({messages:[{content:'original-priority'}],contents:[{content:'independent-priority'}]});
 const f=fixture(current,body);await tick();f.respond(plan=>({...plan,messages:['contents','messages','input']}));await tick();const dom=JSON.stringify(f.snapshot());assert.ok(dom.includes('independent-priority'));assert.equal(f.source.textContent,body);assert.ok(!JSON.stringify(f.calls).includes('priority'));f.stop();
});
test('owning request recipe controls ordering, expansion, scalar wrapping and text composition',async()=>{
 const body=JSON.stringify({instructions:['alpha',null,'omega'],input:'single',tools:[{name:'direct'}],temperature:.4});
 const f=fixture(current,body);await tick();f.respond(plan=>({...plan,sections:['parameters','tools','messages','system'],expanded:{system:true,messages:'none',tools:true,tool:true,parameters:true},textRules:{separator:' / ',skipEmpty:false},singleMessage:{roleField:'role',role:'assistant',contentField:'content'},uppercaseRole:false,labels:{...plan.labels,indexPrefix:'#',indexSuffix:': ',roleSuffix:' => '}}));await tick();
 const attempt=f.host.children[0].children[0],sections=attempt.children.slice(1);
 assert.deepEqual(sections.map(section=>section.children[0].textContent),['Parameters','Tools · 1','#0: assistant => single','System instructions']);
 assert.deepEqual(sections.map(section=>section.open),[true,true,false,true]);
 assert.equal(sections[1].children[1].children[0].open,true);
 assert.equal(sections[3].children[1].textContent,'alpha /  / omega');
 assert.equal(f.source.textContent,body);assert.equal(f.calls.length,1);assert.ok(!JSON.stringify(f.calls[0].request).includes('alpha'));f.stop();
 const all=fixture(current,JSON.stringify({messages:[{role:'user',content:'one'},{role:'tool',content:'two'}]}));await tick();all.respond(plan=>({...plan,expanded:{...plan.expanded,messages:'all'}}));await tick();assert.deepEqual(all.host.children[0].children[0].children.slice(1).map(section=>section.open),[true,true]);all.stop();
});
test('disposed and malformed request recipes retain original evidence and never use native policy',async()=>{
 const late=fixture(current,'{"messages":[{"content":"private"}]}');await tick();late.stop();late.respond();await tick();assert.equal(late.host.children.length,0);assert.equal(late.host.dataset.wireReady,undefined);
 for(const modify of [plan=>({...plan,sections:['system','messages','tools','tools']}),plan=>({...plan,expanded:{...plan.expanded,messages:['last']}}),plan=>({...plan,textRules:{separator:'x'.repeat(17),skipEmpty:true}}),plan=>({...plan,singleMessage:{roleField:'role',role:'user',contentField:'role'}}),plan=>({...plan,uppercaseRole:'yes'}),plan=>({...plan,excerpt:Infinity}),plan=>({...plan,messages:[]}),plan=>({...plan,labels:{...plan.labels,system:{private:'raw error'}}})]){
  const f=fixture(current,'{"instructions":"private"}');await tick();f.respond(modify);await tick();assert.equal(f.closed,1);assert.equal(f.entry.active,false);assert.equal(f.host.children.length,0);assert.equal(f.source.dataset.jsonOnly,undefined);
 }
});

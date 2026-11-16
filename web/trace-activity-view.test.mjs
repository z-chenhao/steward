import assert from 'node:assert/strict';
import test from 'node:test';
import {readFileSync} from 'node:fs';
import {transformSync} from 'esbuild';
import {runInNewContext} from 'node:vm';
const source=readFileSync(new URL('src/trace-activity-view.ts',import.meta.url),'utf8')
 .replace("import {formatLocalTime} from './local-time';","const formatLocalTime=globalThis.fixtureTime;")
 .replace("import {traceProgram} from './trace-program';","const traceProgram=globalThis.fixtureProgram;")
 .replace("import {legacyTraceActivityView} from './legacy/trace-activity-view-v1';","const legacyTraceActivityView=()=>{throw Error('native fallback')};");
const code=transformSync(source,{loader:'ts',format:'cjs',target:'es2022'}).code;
const tick=()=>new Promise(resolve=>setImmediate(resolve));
function fixture(){
 const pending=[],axis={nodes:['original'],replaceChildren(...nodes){this.nodes=nodes}},maximum={textContent:'original'};
 let resize,closed=0,disconnected=0;
 const entry={active:true,browser:Promise.resolve({call:request=>new Promise(resolve=>pending.push({request,resolve})),close:()=>closed++})};
 const root={isConnected:true,closest:()=>({dataset:{traceProgramRevision:'sha256:'+'a'.repeat(64)}}),querySelector:selector=>selector==='.obs-activity-axis'?axis:maximum},plot={clientWidth:500};
 const module={exports:{}};runInNewContext(code,{module,exports:module.exports,fixtureTime:(time,mode)=>({text:mode+':'+time}),fixtureProgram:()=>entry,document:{documentElement:{lang:'en'},createElement:()=>({textContent:''})},ResizeObserver:class{constructor(fn){resize=fn}observe(){}disconnect(){disconnected++}}});
 const stop=module.exports.enhanceTraceActivityView(root,plot,[{at:'2026-11-08T00:00:00Z',until:'2026-11-08T00:01:00Z',count:1}]);
 return {stop,pending,axis,maximum,entry,plot,resize:()=>resize(),get closed(){return closed},get disconnected(){return disconnected}};
}
const plan=(maximum=1)=>({maximum,axis:{mode:'clock',times:['2026-11-08T00:00:00Z','2026-11-08T00:01:00Z']}});
test('activity renderer ignores old resize replies and shares the frozen page program',async()=>{
 const f=fixture();await tick();assert.equal(f.pending[0].request.type,'activity_view');f.plot.clientWidth=390;f.resize();await tick();
 f.pending[1].resolve(plan(2));await tick();assert.equal(f.maximum.textContent,'2');assert.equal(f.axis.nodes.length,2);
 f.pending[0].resolve(plan(9));await tick();assert.equal(f.maximum.textContent,'2');assert.equal(f.closed,0);
 f.stop();assert.equal(f.disconnected,1);assert.equal(f.closed,0);
});
test('malformed activity projection preserves original facts and closes without native fallback',async()=>{
 for(const bad of [{maximum:1,axis:{mode:'script',times:plan().axis.times}},{maximum:-1,axis:plan().axis},{maximum:1,axis:{mode:'clock',times:['private-marker','2026-11-08']}}]){
  const f=fixture();await tick();f.pending[0].resolve(bad);await tick();assert.equal(f.maximum.textContent,'original');assert.deepEqual(f.axis.nodes,['original']);assert.equal(f.closed,1);assert.equal(f.disconnected,1);
 }
});
test('disposed or revoked activity cannot apply a late accepted projection',async()=>{
 for(const revoke of [f=>f.stop(),f=>{f.entry.active=false}]){
  const f=fixture();await tick();revoke(f);f.pending[0].resolve(plan());await tick();assert.equal(f.maximum.textContent,'original');assert.deepEqual(f.axis.nodes,['original']);
 }
});

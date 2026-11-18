import assert from 'node:assert/strict';
import test from 'node:test';
import {buildSync} from 'esbuild';
import {runInNewContext} from 'node:vm';
function bundle(path,globals={}){const source=buildSync({entryPoints:[new URL(path,import.meta.url).pathname],bundle:true,format:'cjs',write:false}).outputFiles[0].text;const module={exports:{}};runInNewContext(source,{module,exports:module.exports,...globals});return module.exports}
test('CSV exports preserve fields and prevent spreadsheet formula execution',()=>{
 const {csv}=bundle('src/intelligent-ui/exports.ts');const result=csv([['Title','Value'],['=HYPERLINK("https://bad")','a,b\nc'],['@SUM(1)','normal']]);assert(result.startsWith('\ufeff'));assert(result.includes('"\'=HYPERLINK(""https://bad"")"'));assert(result.includes('"a,b\nc"'));assert(result.includes('"\'@SUM(1)"'));assert.equal(csv([[-3,'-4']]),'\ufeff"-3","-4"');
});
test('concurrent flushes serialize actual saves, preserve edits during a write and merge explicit context',async()=>{
 const listeners=new Map();let releaseLoad,releaseWrite,firstWrite;const loading=new Promise(r=>releaseLoad=r),writing=new Promise(r=>releaseWrite=r),started=new Promise(r=>firstWrite=r);let state={version:0,state:{retained:7},model_context:{}},inflight=0,max=0,writes=0;
 const window={addEventListener:(name,fn)=>listeners.set(name,fn),dispatchEvent:event=>{listeners.get(event.type)?.(event)},setTimeout,clearTimeout};
 const api=bundle('src/intelligent-ui/state.ts',{window,setTimeout,clearTimeout,AbortSignal,CustomEvent:class {constructor(type,{detail}){this.type=type;this.detail=detail}},fetch:async(url,options)=>{if(!options?.method){await loading;return {ok:true,json:async()=>JSON.parse(JSON.stringify(state))}}const input=JSON.parse(options.body);inflight++;max=Math.max(max,inflight);writes++;if(writes===1){firstWrite();await writing}assert.equal(input.version,state.version);state={version:state.version+1,state:input.state,model_context:input.model_context};inflight--;return {ok:true,json:async()=>state}}});
 api.setUIState('ui:real#0',{filter:'A'},{selection:'A'});const a=api.flushUIStates(),b=api.flushUIStates();releaseLoad();await started;api.setUIState('ui:real#0',{filter:'B'},{sort:'Value'});releaseWrite();await Promise.all([a,b]);assert.equal(max,1);assert.equal(writes,2);assert.equal(state.state.retained,7);assert.equal(state.state.filter,'B');assert.equal(state.model_context.selection,'A');assert.equal(state.model_context.sort,'Value');
});

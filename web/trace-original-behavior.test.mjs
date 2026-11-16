import assert from 'node:assert/strict';
import test from 'node:test';
import {readFileSync} from 'node:fs';
import {createHash} from 'node:crypto';
import {transformSync} from 'esbuild';
import {runInNewContext} from 'node:vm';

const fixtures=new URL('../tests/fixtures/plugins/trace-workbench-original-v1/',import.meta.url);
const read=name=>readFileSync(new URL(name,fixtures),'utf8');
const oracle=JSON.parse(read('client-behavior.json'));
const plain=value=>JSON.parse(JSON.stringify(value));

test('Trace migration baseline retains the independently captured original source fingerprints',()=>{
 const provenance=JSON.parse(read('provenance.json'));
 for(const file of [...provenance.files,...provenance.activity_sources]){
  const bytes=readFileSync(new URL(file.capture,fixtures));
  assert.equal(bytes.length,file.bytes,file.source);
  assert.equal(createHash('sha256').update(bytes).digest('hex'),file.sha256,file.source);
 }
 const activity=provenance.activity_behavior;assert.equal(createHash('sha256').update(read(activity.capture)).digest('hex'),activity.sha256);assert.equal(JSON.parse(read(activity.capture)).length,activity.cases);
 const locale=provenance.tooltip_localization;assert.equal(createHash('sha256').update(read(locale.capture)).digest('hex'),locale.sha256);
 for(const [key,file] of [['client','web__src__observability.ts.txt'],['time','web__src__trace-time.ts.txt']])assert.equal(createHash('sha256').update(read(file)).digest('hex'),oracle.source_sha256[key]);
});

const source=readFileSync(new URL('src/trace-time.ts',import.meta.url),'utf8');
const module={exports:{}};
runInNewContext(transformSync(source,{loader:'ts',format:'cjs',target:'es2022'}).code,{module,exports:module.exports});
const legacyTimeModule={exports:{}};
runInNewContext(transformSync(readFileSync(new URL('src/legacy/trace-time-v1.ts',import.meta.url),'utf8'),{loader:'ts',format:'cjs',target:'es2022'}).code,{module:legacyTimeModule,exports:legacyTimeModule.exports});
Object.assign(module.exports,legacyTimeModule.exports);
const operations={
 bounded_window:({domain,start,end})=>module.exports.boundedWindow(domain,start,end),
 zoom_window:({domain,current,factor,anchor})=>module.exports.zoomWindow(domain,current,factor,anchor),
 bucket_selection:({count,first,last})=>module.exports.bucketSelection(count,first,last),
 elapsed_label:value=>module.exports.elapsedLabel(value),
 time_micros:value=>module.exports.timeMicros(value),
 activity_geometry:value=>module.exports.activityGeometry(value),
 activity_index:({geometry,fraction})=>module.exports.activityIndex(geometry,fraction),
};
for(const [name,execute] of Object.entries(operations))test('Trace original behavior: '+name,()=>{
 for(const [index,example] of oracle[name].entries())assert.deepEqual(plain(execute(example.input)),example.expected,name+' case '+index);
});

// Execute the original browser function with only its documented DOM reads and
// writes. This is a deterministic policy oracle, not a browser lifecycle test.
const legacyModule={exports:{}};
const legacySource=readFileSync(new URL('src/legacy/trace-workbench-v1.ts',import.meta.url),'utf8');
runInNewContext(transformSync(legacySource,{loader:'ts',format:'cjs',target:'es2022'}).code,{module:legacyModule,exports:legacyModule.exports});
test('Trace original tree search, ancestry, collapse and cycle behavior',()=>{
 for(const [index,example] of oracle.tree.entries()){
  const {nodes:records,query,closed}=example.input;
  const nodes=records.map(record=>{
   const button=records.some(child=>child.parent===record.id)?{disabled:false,attrs:{},setAttribute(key,value){this.attrs[key]=value}}:null;
   return {dataset:{observationId:record.id,parentObservation:record.parent,observationType:record.type},hidden:false,button,querySelector(selector){if(selector==='.obs-node-name')return {textContent:record.name};if(selector==='[data-observation-toggle]')return button;throw Error(selector)}};
  });
  const pane={querySelector(selector){assert.equal(selector,'.obs-tree-search input');return {value:query}},querySelectorAll(selector){assert.equal(selector,'.observation-node');return nodes}};
  legacyModule.exports.legacyRenderTraceTree(pane,new Set(closed),text=>text);
  const actual=nodes.map(node=>({id:node.dataset.observationId,hidden:node.hidden,disabled:node.button?.disabled??null,expanded:node.button?.attrs['aria-expanded']??null,label:node.button?.attrs['aria-label']??null}));
  assert.deepEqual(actual,example.expected,'tree case '+index);
 }
});

test('Trace original column preference migrations and malformed storage precedence',()=>{
 for(const [index,example] of oracle.columns.entries()){
  const actual=legacyModule.exports.legacyTraceColumns(example.storage);
  assert.deepEqual(plain(actual),example.expected,'columns case '+index);
 }
});

import assert from 'node:assert/strict';
import test from 'node:test';
import {readFileSync} from 'node:fs';
import {fileURLToPath} from 'node:url';
import {runInNewContext} from 'node:vm';
import {buildSync} from 'esbuild';
const bundle=buildSync({entryPoints:[fileURLToPath(new URL('./src/model-picker-view.ts',import.meta.url))],bundle:true,platform:'node',format:'cjs',write:false}).outputFiles[0].text;
const module={exports:{}};runInNewContext(bundle,{module,exports:module.exports});
const {pickerPresentation,modelPickerView}=module.exports;
const original=JSON.parse(readFileSync(new URL('../tests/fixtures/plugins/model-picker-native-v1/original.json',import.meta.url),'utf8'));
test('all256 executed native states retain their actual labels and presentation',()=>{
 const policy=pickerPresentation.parse(original);
 for(const language of ['en','zh-CN'])for(let outcome=0;outcome<4;outcome++)for(let flags=0;flags<32;flags++){
 const error=['','partial','unavailable','selection'][outcome];
 const facts={ready:!!(flags&1),loading:!!(flags&2),saving:!!(flags&4),sending:!!(flags&8),hasCurrent:!!(flags&16)};
 const view=language==='en'?original.en:original.zh_cn;
 assert.deepEqual(JSON.parse(JSON.stringify(modelPickerView(policy,facts,error,language))),{...view.states[flags+outcome*32],label:view.label,placeholder_label:view.placeholder_label});
 }
});
test('independent presentation changes without touching the request coordinator',()=>{
 const variant=structuredClone(original);variant.en.label='Versioned model';variant.en.states[2].busy=false;
 const view=modelPickerView(pickerPresentation.parse(variant),{ready:false,loading:true,saving:false,sending:false,hasCurrent:false},'','en');
 assert.equal(view.label,'Versioned model');assert.equal(view.busy,false);assert.equal(view.disabled,false);
});
test('missing duplicate or out-of-bound state tables never select native defaults',()=>{
 for(const mutate of [v=>v.en.states.pop(),v=>v.en.states[4].id=3,v=>v.en.states[0].pending_text='\u001b[2J',v=>v.en.label='x'.repeat(513)]){
 const value=structuredClone(original);mutate(value);assert.throws(()=>pickerPresentation.parse(value));
 }
});

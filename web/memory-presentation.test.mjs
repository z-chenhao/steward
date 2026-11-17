import test from 'node:test';
import assert from 'node:assert/strict';
import {readFileSync} from 'node:fs';
import {buildSync} from 'esbuild';
import {runInNewContext} from 'node:vm';
const code=buildSync({entryPoints:[new URL('src/memory-presentation.ts',import.meta.url).pathname],bundle:true,format:'cjs',write:false}).outputFiles[0].text;
const revision='sha256:'+'a'.repeat(64),original=JSON.parse(readFileSync(new URL('../tests/fixtures/plugins/memory-inspector-original-v1/localization-v1.json',import.meta.url),'utf8'));
let link=null,scripts=[];
class Script{constructor(text){this.textContent=text}}
const module={exports:{}};runInNewContext(code,{module,exports:module.exports,URL,location:{href:'http://127.0.0.1:8080/developer/memory'},HTMLScriptElement:Script,document:{querySelector:()=>link,querySelectorAll:()=>scripts}});
const {validateMemoryPresentation,memoryCopy,readMemoryPresentation}=module.exports;
const presentation=()=>({revision,copy:structuredClone(original)});
test('Memory client preserves all original bilingual copy and unknown diagnostic text',()=>{
 const p=validateMemoryPresentation(presentation(),revision);
 for(const entry of original){assert.equal(memoryCopy(p,'en',entry.english),entry.english);assert.equal(memoryCopy(p,'zh-CN',entry.english),entry.chinese);assert.equal(memoryCopy(p,'zh-CN',' '+entry.english+' '),' '+entry.chinese+' ')}
 assert.equal(memoryCopy(p,'zh-CN','UNCLASSIFIED_ERROR'),'UNCLASSIFIED_ERROR');
 const row=p.copy.find(e=>e.english==='Memory details');row.chinese='$& <img> independent';assert.equal(memoryCopy(p,'zh-CN','Memory details'),row.chinese);
});
test('Memory rejects version drift, extra authority, missing, duplicate and malformed copy',()=>{
 for(const mutate of [p=>p.revision='sha256:'+'b'.repeat(64),p=>p.copy=[],p=>p.copy.push(p.copy[0]),p=>p.copy[0].chinese='',p=>p.copy[0].english=42,p=>p.copy[0].effect='send',p=>p.effect='send',p=>p.copy=original[0]]){const p=presentation();mutate(p);assert.throws(()=>validateMemoryPresentation(p,revision),/Memory presentation unavailable/)}
 assert.throws(()=>validateMemoryPresentation(presentation(),'not-a-graph'),/unavailable/);
});
test('Memory managed config cannot fall back to native after missing, forged or duplicate metadata',()=>{
 link=null;scripts=[];assert.equal(readMemoryPresentation(),null);
 link={href:'http://127.0.0.1:8080/api/developer/memory/style?inspector_revision='+encodeURIComponent(revision)};
 assert.throws(()=>readMemoryPresentation(),/unavailable/);
 scripts=[{textContent:JSON.stringify(presentation())}];assert.throws(()=>readMemoryPresentation(),/unavailable/);
 scripts=[new Script(JSON.stringify(presentation()))];assert.equal(readMemoryPresentation().revision,revision);
 scripts.push(scripts[0]);assert.throws(()=>readMemoryPresentation(),/unavailable/);
 scripts=[new Script('{broken')];assert.throws(()=>readMemoryPresentation());
 link=null;scripts=[];
});

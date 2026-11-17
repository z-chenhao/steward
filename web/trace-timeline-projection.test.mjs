import assert from 'node:assert/strict';
import test from 'node:test';
import {readFileSync} from 'node:fs';
import {transformSync} from 'esbuild';
import {runInNewContext} from 'node:vm';
const source=readFileSync(new URL('src/trace-timeline-projection.ts',import.meta.url),'utf8'),module={exports:{}};
runInNewContext(transformSync(source,{loader:'ts',format:'cjs',target:'es2022'}).code,{module,exports:module.exports});
const accept=module.exports.acceptTraceTimelineProjection;
const plan=()=>({drag_threshold:5,ticks:['0.00ms','1.00s'],range_label:'Time since trace start: 0.00ms – 1.00s',grid:[0,250,500,750,1000],full:true,buttons:{in:false,out:true,fit:true,selected:false},rows:[{bar:{x:100,width:800},point:null,first_output:200,tooltip:['Start +0.00ms','Duration 1.00s']}]});
test('timeline projection accepts bounded feature-owned geometry and rejects any malformed lane',()=>{
 const good=plan();assert.equal(accept(good,1,true),good);
 for(const change of [p=>{p.rows=[]},p=>{p.rows[0].bar.x=NaN},p=>{p.rows[0].bar.width=0},p=>{p.rows[0].point=1001},p=>{p.rows[0].first_output=-1},p=>{p.grid=[Infinity]},p=>{p.ticks=['one']},p=>{p.ticks=Array(17).fill('x')},p=>{p.range_label='x'.repeat(513)},p=>{p.full=false},p=>{p.buttons.fit='false'},p=>{p.drag_threshold=-1},p=>{p.drag_threshold=Infinity},p=>{p.rows[0].tooltip=['x'.repeat(513)]},p=>{p.rows[0].tooltip='private-marker'}]){
  const p=plan();change(p);assert.throws(()=>accept(p,1,true));
 }
});

test('timeline brush projection accepts only bounded finite geometry and text',()=>{
 const acceptBrush=module.exports.acceptTraceTimelineBrush,good={x:100,width:800,label:'100.00ms – 900.00ms'};assert.equal(acceptBrush(good),good);
 for(const change of [p=>{p.x=-1},p=>{p.width=1001},p=>{p.x=NaN},p=>{p.label='x'.repeat(513)},p=>{p.label=null}]){const p={...good};change(p);assert.throws(()=>acceptBrush(p))}
});

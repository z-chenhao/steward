import assert from 'node:assert/strict';
import {readFile} from 'node:fs/promises';
import test from 'node:test';
import vm from 'node:vm';
const source=await readFile(new URL('../internal/plugin/intelligentui/view/charts-worker.js',import.meta.url),'utf8');
function worker(facts){
 const messages=[],scope=vm.createContext({postMessage:value=>messages.push(JSON.parse(JSON.stringify(value)))});
 vm.runInContext(source,scope);let seq=0;
 const send=value=>{const before=messages.length;scope.onmessage({data:{...value,seq:++seq}});return messages.length===before?undefined:messages.at(-1)};
 send({type:'init',facts});return send;
}
test('chart policy preserves original query, zoom range and local reset semantics',()=>{
 const facts={kind:'line',title:'Trend',data:[{label:'A',value:10},{label:'B',value:42},{label:'C',value:25}]};
 const send=worker(facts);assert.equal(send({type:'export'}),undefined);
 assert.deepEqual(send({type:'restore',state:{start:0,end:1}}).projection.rows,[0,1,2]);
 assert.deepEqual(send({type:'zoom'}).projection.rows,[0,1]);
 assert.deepEqual(send({type:'group',query:'b',selected:'B'}).projection.rows,[1]);
 assert.deepEqual(send({type:'export'}).rows,[1]);
 assert.equal(send({type:'select',item:0}),undefined);assert.equal(send({type:'select',item:1}).item,1);
 const reset=send({type:'reset'}).projection;assert.deepEqual(reset.rows,[0,1,2]);assert.equal(reset.zoom,false);assert.equal(reset.end,2);
 assert.equal(send({type:'restore',state:{start:2,end:2}}),undefined);
});
test('multi-series numeric X and export preserve original row positions',()=>{
 const send=worker({kind:'line',labels:['0','1000','10000'],xValues:[0,1000,10000],series:[{name:'A',values:[99,179,899]},{name:'B',values:[299,329,599]}],unit:'元'});
 assert.equal(send({type:'restore',state:{}}).projection.numericLine,true);
 assert.deepEqual(send({type:'export'}).rows,[0,1,2]);
 send({type:'zoom'});send({type:'range',state:{start:1,end:1}});assert.deepEqual(send({type:'export'}).rows,[1]);
});
test('bar orientation follows visible category count and full UTF-16 label length',()=>{
 const send=worker({kind:'bar',data:Array.from({length:7},(_,i)=>({label:String(i),value:i}))});
 assert.equal(send({type:'restore',state:{}}).projection.horizontal,true);
 assert.equal(send({type:'group',query:'1'}).projection.horizontal,false);
 const long=worker({kind:'bar',data:[{label:'Research department 7',value:7e9}]});assert.equal(long({type:'restore',state:{}}).projection.horizontal,true);
});
test('pie stays unzoomed; raw labels and scatter coordinates are never normalized',()=>{
 const raw='Choice\r\nline\0中文🌙';
 const pie=worker({kind:'pie',data:[{label:raw,value:2},{label:'Other',value:3}]});pie({type:'restore',state:{}});
 assert.equal(pie({type:'zoom'}).projection.zoom,false);assert.deepEqual(pie({type:'group',selected:raw}).projection.opacity,[1,.3]);
 assert.deepEqual(pie({type:'export'}).rows,[0,1]);
 const scatter=worker({kind:'scatter',data:[{label:raw,x:-1,y:0}]});scatter({type:'restore',state:{}});assert.deepEqual(scatter({type:'export'}).rows,[0]);
});

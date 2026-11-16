import assert from 'node:assert/strict';
import test from 'node:test';
import {buildSync} from 'esbuild';
import {runInNewContext} from 'node:vm';

const code=buildSync({entryPoints:[new URL('src/components/open-intelligent-ui/export-utils.ts',import.meta.url).pathname],bundle:true,format:'cjs',write:false}).outputFiles[0].text;
const module={exports:{}};runInNewContext(code,{module,exports:module.exports});
test('native chart exports preserve text labels without creating executable markup',()=>{
 const label='</script><img src=x onerror=alert(1)>';
 for(const kind of ['bar','pie']){
  const html=module.exports.chartToStandaloneHtml(kind,{title:'<script>literal title</script>',description:'Source: user data',data:[{label,value:42}]});
  assert(!html.includes(label));assert(html.includes('&lt;script&gt;literal title&lt;/script&gt;'));
  const labels=JSON.parse(html.match(/labels: (\[[^\n]+\])/)[1]);assert.equal(labels[0],label);
 }
});

test('shared-axis series have complete aligned values and distinct names',()=>{
 const source=buildSync({entryPoints:[new URL('src/intelligent-ui/line-schema.ts',import.meta.url).pathname],bundle:true,format:'cjs',write:false}).outputFiles[0].text;
 const chart={exports:{}};runInNewContext(source,{module:chart,exports:chart.exports});
 const schema=chart.exports.lineChartSchema;
 const props={title:'Growth',description:'User data',unit:'万人',labels:['1月','2月'],series:[{name:'A',values:[12,15]},{name:'B',values:[10,13]}]};
 assert(schema.safeParse(props).success);
 assert(schema.safeParse({...props,xValues:[1000,10000]}).success);
 for(const xValues of [[0],[0,0],[1,0],[0,Infinity]])assert(!schema.safeParse({...props,xValues}).success);
 assert(schema.safeParse({title:'Legacy',description:'User',data:[{label:'A',value:1}]}).success);
 for(const bad of [{...props,data:[]},{...props,series:[{name:'A',values:[1]}]},{...props,series:[{name:'A',values:[1,2]},{name:'A',values:[3,4]}]},{...props,series:[{name:'A',values:[1,Infinity]}]}])assert(!schema.safeParse(bad).success);
});

test('preview wire accepts the same Unicode character limits as the catalog',()=>{
 const source=buildSync({entryPoints:[new URL('src/intelligent-ui/preview.tsx',import.meta.url).pathname],bundle:true,format:'cjs',jsx:'automatic',write:false,define:{'process.env.NODE_ENV':'"production"'}}).outputFiles[0].text;
 const preview={exports:{}};runInNewContext(source,{module:preview,exports:preview.exports,setTimeout,clearTimeout,URL,TextEncoder,TextDecoder,window:{addEventListener(){}}});
 const key='a'.repeat(32);
 for(const character of ['中','😀'])for(const length of [8192,8193]){
  const frame={key,revision:1,state:'draft',items:[{key,title:character.repeat(200),text:character.repeat(length),components:[]}]};
  assert.equal(preview.exports.uiPreviewFrame.safeParse(frame).success,length===8192);
 }
});

test('native form rendering cannot submit without a Host port, including inert previews',async()=>{
 const source=buildSync({entryPoints:[new URL('src/components/open-intelligent-ui/catalog.tsx',import.meta.url).pathname],bundle:true,format:'cjs',jsx:'automatic',write:false,define:{'process.env.NODE_ENV':'"production"'}}).outputFiles[0].text;
 const catalog={exports:{}};let requests=0;
 runInNewContext(source,{module:catalog,exports:catalog.exports,setTimeout,clearTimeout,URL,TextEncoder,TextDecoder,AbortController,window:{addEventListener(){}},fetch:()=>{requests++;throw Error('renderer attempted direct ingress')}});
 const render=catalog.exports.OpenIntelligentAnswer;
 const value={name:'steward.form',props:{title:'Question',fields:[{name:'answer',label:'Answer',type:'text',required:true}]},message_id:'display-only-message',form_id:'display-only-form'};
 const fields=tree=>tree.props.children.props.respond;
 await assert.rejects(fields(render({value,fallback:''}))({answer:'No Host authority'}),/Selection unavailable/);
 let accepted=[];
 const submitFields=async(values,signal)=>accepted.push({values,signal});
 for(const status of [{disabled:true},{submitted:true}])await assert.rejects(fields(render({value:{...value,...status},fallback:'',submitFields}))({answer:'Rejected'}),/Selection unavailable/);
 await fields(render({value,fallback:'',submitFields}))({answer:'Field text'});
 assert.equal(accepted.length,1);assert.equal(JSON.stringify(accepted[0].values),'{"answer":"Field text"}');assert.equal(accepted[0].signal,undefined);
 const meeting={name:'scheduleTime',props:{reasonForScheduling:'Review',meetingDuration:30,timeSlots:[{date:'2026-12-01',time:'10:00'}]}};
 const choose=fields(render({value:meeting,fallback:'',submitFields}));
 await choose('Meeting scheduled for 2026-12-01 at 10:00 (30 min).');
 assert.equal(JSON.stringify(accepted[1].values),'{"choice":"0"}');assert(accepted[1].signal instanceof AbortSignal);assert.equal(accepted[1].signal.aborted,false);
 await assert.rejects(choose('Forged selection'),/Unknown selection/);
 await choose('The user declined all proposed meeting times. Please suggest alternative times or ask for their availability.');
 assert.equal(JSON.stringify(accepted[2].values),'{"choice":"decline"}');
 assert.equal(requests,0);
});

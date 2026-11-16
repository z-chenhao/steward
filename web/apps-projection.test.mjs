import test from 'node:test';
import assert from 'node:assert/strict';
import {readFileSync} from 'node:fs';
import {transform} from 'esbuild';
const source=readFileSync(new URL('./src/apps-browser.ts',import.meta.url),'utf8');
const {code}=await transform(source,{loader:'ts',format:'esm'});
const {validateAppsProjection,validateAppsPollPlans,validateAppsNotice,validateAppsActionView,validateAppsFailureView}=await import('data:text/javascript;base64,'+Buffer.from(code).toString('base64'));
const facts={accounts:[{slug:'mail',state:'active',public:false},{slug:'drive',state:'active',public:false},{slug:'mail',state:'pending',public:false}],tiles:['mail'],details:[{slug:'mail',suggested:false,no_auth:false,require_alias:false}]};
const projection=()=>({type:'projection',tiles:[{manage_hidden:false,add_hidden:true}],details:[{accounts:[0,2],empty_hidden:true,first_hidden:true,additional_hidden:false}]});
test('Apps allows independent ordering while retaining every original scoped account',()=>{
 const value=projection();value.details[0].accounts=[2,0];
 assert.deepEqual(validateAppsProjection(value,facts).details[0].accounts,[2,0]);
});
test('Apps rejects omitted, duplicated, cross-app, invented and malformed projection slots',()=>{
 for(const accounts of [[],[0],[0,0],[0,1],[0,3],[-1,2],[0,2.5],[0,'2']]){
  const value=projection();value.details[0].accounts=accounts;
  assert.throws(()=>validateAppsProjection(value,facts),/Invalid Apps/);
 }
 for(const mutate of [v=>v.tiles.pop(),v=>v.details.push(v.details[0]),v=>v.details[0].first_hidden='false',v=>v.tiles[0].add_hidden=0,v=>v.type='effect']){
  const value=projection();mutate(value);assert.throws(()=>validateAppsProjection(value,facts),/Invalid Apps/);
 }
});


test('Apps poll projection keeps original deadlines, live-call fences and bounded delay',()=>{
 const facts=[{state:'pending',stopped:false,scheduled:false,in_flight:false,now:100,deadline:200}];
 const value={type:'poll-plans',plans:[{schedule:true,deadline:200,delay:5000,notice:''}]};
 assert.equal(validateAppsPollPlans(value,facts)[0].schedule,true);
 for(const change of [{deadline:201},{delay:1},{delay:60001},{deadline:Infinity},{notice:'a'.repeat(513)}]){
  const candidate=structuredClone(value);Object.assign(candidate.plans[0],change);assert.throws(()=>validateAppsPollPlans(candidate,facts));
 }
 for(const change of [{stopped:true},{scheduled:true},{in_flight:true},{state:'active'},{now:200}])assert.throws(()=>validateAppsPollPlans(value,[{...facts[0],...change}]));
});
test('Apps display replies cannot invent an alias error or an unrelated details opening',()=>{
 const facts={action:'check',authorization:false,popup_opened:false,dialog_open:false,detail_page:false};
 assert.throws(()=>validateAppsActionView({type:'action-view',notice:'done',open_details:true},facts));
 assert.throws(()=>validateAppsFailureView({type:'failure-view',reveal_alias:true},'unknown'));
 assert.deepEqual(validateAppsFailureView({type:'failure-view',reveal_alias:true},'alias_required'),{reveal_alias:true});
 assert.throws(()=>validateAppsNotice({type:'notice',text:'a'.repeat(513),error:false}));
 assert.deepEqual(validateAppsNotice({type:'notice',text:'<escaped as text>',error:false}),{text:'<escaped as text>',error:false});
});

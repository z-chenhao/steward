import assert from 'node:assert/strict';
import test from 'node:test';
import {readFileSync} from 'node:fs';
import {fileURLToPath} from 'node:url';
import {runInNewContext} from 'node:vm';
import {buildSync} from 'esbuild';
const source=readFileSync(new URL('../internal/plugin/modelconnections/view/models.html',import.meta.url),'utf8');
const cases=JSON.parse(readFileSync(new URL('../tests/fixtures/plugins/model-connections-original-v1/provider-v1.json',import.meta.url),'utf8'));
const bundle=buildSync({entryPoints:[fileURLToPath(new URL('./src/model-connection-controls.ts',import.meta.url))],bundle:true,platform:'node',format:'cjs',write:false}).outputFiles[0].text;
const module={exports:{}};runInNewContext(bundle,{module,exports:module.exports});
const legacy={exports:{}};runInNewContext(buildSync({entryPoints:[fileURLToPath(new URL('./src/legacy/model-connection-v1.ts',import.meta.url))],bundle:true,platform:'node',format:'cjs',write:false}).outputFiles[0].text,{module:legacy,exports:legacy.exports});
function policy(html,service){
 const option=[...html.matchAll(/<option value="(openai|custom)" ([\s\S]*?)>/g)].find(match=>match[1]===service);assert.ok(option);
 return Object.fromEntries([...option[2].matchAll(/data-model-owner-([a-z-]+)="([^"]*)"/g)].map(match=>[('model-owner-'+match[1]).replace(/-([a-z])/g,(_,c)=>c.toUpperCase()),match[2]]));
}
function drive(facts,html=source,enhance=module.exports.enhanceModelConnectionControls){
 const key={required:false},endpoint={value:facts.value,readOnly:false},advanced={open:facts.open};let change;
 const provider={value:facts.service,selectedOptions:[{dataset:policy(html,facts.service)}],addEventListener:(_,fn)=>change=fn};
 const fields={'[name=service]':provider,'[name=api_key]':key,'[name=endpoint]':endpoint,'.model-connection-advanced':advanced};
 enhance({querySelector:selector=>fields[selector]});if(facts.change)change();
 return {required:key.required,value:endpoint.value,readOnly:endpoint.readOnly,open:advanced.open};
}
test('owning field policy matches 32 executed native initialization/change scenarios',()=>{
 assert.equal(cases.length,32);for(const item of cases)assert.deepEqual(drive(item.facts),item.expected,JSON.stringify(item.facts));
});
test('independent owning default changes without changing the host field bindings',()=>{
 const changed=source.replace('data-model-owner-endpoint-default="http://127.0.0.1:8000/v1"','data-model-owner-endpoint-default="http://127.0.0.1:9000/v1"');assert.notEqual(changed,source);
 assert.deepEqual(drive({service:'custom',value:'https://api.openai.com/v1',open:false,change:true},changed),{required:false,value:'http://127.0.0.1:9000/v1',readOnly:false,open:true});
});

test('explicit historical interpreter retains the same 32 captured behaviors',()=>{for(const item of cases)assert.deepEqual(drive(item.facts,source,legacy.exports.enhanceLegacyModelConnectionV1),item.expected)});

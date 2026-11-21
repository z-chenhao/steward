import assert from 'node:assert/strict';
import {after,before,test} from 'node:test';
import {fileURLToPath} from 'node:url';
import {chromium} from 'playwright-core';
import {build} from '../web/node_modules/esbuild/lib/main.js';

let browser,script;
before(async()=>{
 browser=await chromium.launch({headless:true,...(process.env.STEWARD_BROWSER_EXECUTABLE?{executablePath:process.env.STEWARD_BROWSER_EXECUTABLE}:{})});
 const entry=`
 export * from './src/composer';
 export * from './src/live-updates';
 export * from './src/projections';
 export * from './src/rich-forms';
 export * from './src/intelligent-ui/state';
 import {NativeFormControls} from './src/intelligent-ui/extensions';
 import {IntelligentWidget} from './src/widgets';
 import {createRoot} from 'react-dom/client';
 export function showControls(){
 const props={title:'Form',fields:[{name:'extras',label:'Extras',type:'multiselect',required:true,options:[{value:'meal',label:'Meal'},{value:'bag',label:'Bag'}]},{name:'comment',label:'Comment',type:'textarea'}]};
 createRoot(document.querySelector('#forms')).render(<NativeFormControls reference="ui:form#0" props={props} defaults={{extras:'[]',comment:''}} respond={async values=>{window.formAnswers=values}}/>);
 createRoot(document.querySelector('#widget')).render(<IntelligentWidget title="Widget" reference="ui:widget#0" fallback="widget"/>);
 }`;
 script=(await build({stdin:{contents:entry,loader:'tsx',resolveDir:fileURLToPath(new URL('../web/',import.meta.url))},jsx:'automatic',bundle:true,format:'iife',globalName:'ui',write:false})).outputFiles[0].text;
});
after(async()=>{await browser?.close()});
const state={version:1,state:{},model_context:{}};
const deferred=()=>{let resolve;const promise=new Promise(done=>{resolve=done});return {promise,resolve}};
async function open(t,body,handle,path='/'){
 const context=await browser.newContext();t.after(()=>context.close());
 const page=await context.newPage();
 await page.route('http://answers.test/**',async route=>{
  const url=new URL(route.request().url());
  if(await handle(route,url))return;
  if(url.pathname==='/style.css')return route.fulfill({path:fileURLToPath(new URL('../internal/interaction/web/assets/style-base.css',import.meta.url)),contentType:'text/css'});
  if(url.pathname==='/api/ui/state')return route.fulfill({json:state});
  if(route.request().isNavigationRequest()&&route.request().frame()===page.mainFrame())return route.fulfill({contentType:'text/html',body:'<link rel="stylesheet" href="/style.css">'+body});
  return route.fulfill({status:404});
 });
 await page.goto('http://answers.test'+path);await page.addScriptTag({content:script});return page;
}
test('message waits for an active state POST; failed persistence preserves a retry with the same content',async t=>{
 const started=deferred(),finish=deferred();let writes=0;const delivered=[];
 const body='<section id="composer-root" data-source-id="first"><form><input name="source_id" value="first"><textarea name="text"></textarea><button type="submit">Send</button></form></section><div id="conversation-outgoing"></div>';
 const page=await open(t,body,async(route,url)=>{
  if(url.pathname==='/api/ui/state'&&route.request().method()==='POST'){writes++;if(writes===1){started.resolve();await finish.promise;await route.fulfill({status:503});}else await route.fulfill({json:{...state,version:2}});return true}
  if(url.pathname==='/message'){delivered.push(route.request().postData());await route.fulfill({json:{accepted:true,location:'/'}});return true}
 });
 await page.evaluate(async()=>{ui.enhanceComposer(()=>{});await ui.getUIState('ui:form#0');ui.setUIState('ui:form#0',{choice:'A'})});
 await started.promise;
 const cancelled=await page.evaluate(async()=>{
  const controller=new AbortController();
  const wait=ui.flushUIStates(controller.signal);controller.abort();
  try{await wait;return false}catch(error){return error.name==='AbortError'}
 });
 assert.equal(cancelled,true);
 await page.locator('textarea').fill('use my choice');await page.getByText('Send',{exact:true}).click();
 assert.equal(await page.locator('[data-send-state=sending]').count(),1);assert.equal(delivered.length,0);
 finish.resolve();await page.locator('[data-send-state=state-failed]').waitFor();assert.equal(delivered.length,0);
 await page.getByRole('button',{name:'Retry sending'}).click();await page.locator('[data-send-state=accepted]').waitFor();
 assert.equal(writes,2);assert.equal(delivered.length,1);assert.match(delivered[0],/use my choice/);assert.match(delivered[0],/first/);
});
test('pending task feedback is read-only until rejection and the draft is editable again',async t=>{
 const start=deferred(),finish=deferred();const page=await open(t,'<main data-live-view="/tasks/test"><div id="task-feedback-status"></div><form action="/task/feedback"><textarea name="answer"></textarea><button type="submit">Reply</button></form></main>',async(route,url)=>{
  if(url.pathname==='/task/feedback'){start.resolve();await finish.promise;await route.fulfill({status:409});return true}
  if(url.pathname==='/api/updates'){await route.fulfill({status:304});return true}
 },'/tasks/test');
 await page.evaluate(()=>ui.startLiveUpdates());const answer=page.locator('textarea[name=answer]');await answer.fill('A');await page.getByText('Reply').click();await start.promise;
 assert.equal(await answer.isEditable(),false);finish.resolve();await page.getByText('Your saved draft',{exact:true}).waitFor();assert.equal(await answer.isEditable(),true);assert.equal(await answer.inputValue(),'A');
});
test('frozen form retains native submission and its unsent answers survive a live window change',async t=>{
 const spec=JSON.stringify({schema_version:1,fallback:'History',components:[{type:'form',form_id:'original'}]});
 const form='<form method="post" action="/ui/form" data-form-id="original"><input name="form_id" value="original" type="hidden"><fieldset><label>Answer<textarea name="field.note"></textarea></label><button>Submit</button></fieldset><p role="status"></p></form>';
 const body=`<main data-live-view="/"><div id="conversation-messages"><article data-live-key="old"><div data-ui-spec='${spec}' data-ui-fallback="History">${form}</div></article></div><div id="unsent-form-drafts" hidden></div><div id="live-update-status"></div></main>`;
 const start=deferred(),finish=deferred();
 let attempt;
 const page=await open(t,body,async(route,url)=>{
  if(url.pathname==='/ui/form'){attempt=route.request().postData();await route.fulfill({status:503});return true}
  if(url.pathname==='/api/updates'){
   start.resolve();await finish.promise;await route.fulfill({json:{view:'/',before:0,latest:2,regions:{'workspace-status':'','conversation-attention':'','conversation-messages':'<article>New message</article>','conversation-approvals':''}}});return true;
  }
 });
 await page.evaluate(()=>{const live=ui.startLiveUpdates();ui.enhanceProjections();ui.enhanceRichForms();live.start()});
 await start.promise;await page.getByLabel('Answer').fill('draft note');await page.getByRole('button',{name:'Submit'}).click();await page.getByText(/not confirmed/).waitFor();assert.equal(new URLSearchParams(attempt).get('form_id'),'original');
 assert.equal(await page.locator('#unsent-form-drafts').isHidden(),true);finish.resolve();await page.getByText('New message').waitFor();await page.locator('#unsent-form-drafts textarea').waitFor({state:'visible'});assert.equal(await page.locator('#unsent-form-drafts textarea').inputValue(),'draft note');
});
test('empty required multiselect and excess UTF-8 bytes focus the field; an empty widget editor remains available',async t=>{
 const page=await open(t,'<div id="forms"></div><div id="widget"></div>',async(route,url)=>{
  if(url.pathname==='/ui/widget'){await route.fulfill({contentType:'text/html',body:`<script>addEventListener("message",event=>{if(event.data.type==="restore")parent.postMessage({channel:"open-generative-ui",type:"restored"},"*")});parent.postMessage({channel:"open-generative-ui",type:"ready"},"*")</script><button onclick="parent.postMessage({channel:'open-generative-ui',type:'invoke',id:'prompt',method:'sendPrompt',args:{text:'original'}},'*')">Suggest</button>`});return true}
 });
 await page.evaluate(()=>ui.showControls());const send=page.locator('#forms button[type=submit]');await send.click();
 assert.equal(await page.getByLabel('Meal').evaluate(n=>document.activeElement===n),true);await page.getByLabel('Bag').check();
 const comment=page.getByLabel('Comment');await comment.fill('中'.repeat(1400));await send.click();assert.equal(await comment.evaluate(n=>n.validity.customError&&document.activeElement===n),true);
 await comment.fill('中'.repeat(1365));await send.click();await page.getByText('Sent',{exact:true}).waitFor();assert.equal(await page.evaluate(()=>window.formAnswers.extras),'["bag"]');
 await page.frameLocator('#widget iframe').getByText('Suggest').click();const editor=page.locator('#widget textarea');await editor.fill('');assert.equal(await editor.isVisible(),true);await editor.fill('replacement');assert.equal(await editor.inputValue(),'replacement');
});

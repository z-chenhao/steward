import {selectValue} from './channels_controls.mjs';
import {writeFile} from 'node:fs/promises';

export async function modelConnections(page,context,url,evidence){
 const endpoint=process.env.STEWARD_MODEL_CONNECTION_ENDPOINT??'http://127.0.0.1:1/v1';
 const privateKey='SYNTHETIC_WEB_CONNECTION_KEY_NEVER_ECHO';
 const keyed=!!process.env.STEWARD_MODEL_CONNECTION_ENDPOINT;
 const read=async path=>(await context.request.get(url+path)).json();
 await page.goto(url+'/settings/models');
 await page.getByRole('heading',{name:'Models',exact:true}).waitFor();
 if(await page.getByRole('link',{name:'Resources',exact:true}).count()||await page.getByRole('link',{name:'App settings',exact:true}).count())throw Error('Models mixes unrelated settings');
 let authPosts=0;page.on('request',request=>{if(new URL(request.url()).pathname==='/auth/login'&&request.method()==='POST')authPosts++});
 await page.getByRole('link',{name:'Sign in with an account',exact:true}).click();
 await page.getByRole('button',{name:'Sign in with ChatGPT',exact:true}).waitFor();
 if(authPosts||await page.locator('form[action="/auth/login"] input[name=provider]').inputValue()!=='chatgpt')throw Error('account chooser starts implicit authorization');
 await page.screenshot({path:evidence+'/account-provider-choice.png',fullPage:true});
 await page.getByRole('link',{name:'Models',exact:true}).last().click();
 if(await page.getByRole('heading',{name:'Connections',exact:true}).count())throw Error('account identities are mixed with API connections');
 await page.getByRole('heading',{name:'Accounts',exact:true}).waitFor();
 await page.getByRole('heading',{name:'API connections',exact:true}).waitFor();
 const connect=async name=>{
  await page.getByRole('link',{name:'Connect with an API key',exact:true}).click();
  await selectValue(page,page.locator('[name=service]'),'custom');
  await page.getByLabel('Endpoint URL',{exact:true}).fill(endpoint);
  await page.getByLabel('Model ID',{exact:true}).fill('fixture-model');
  await page.locator('[name=name]').fill(name);
  if(keyed)await page.getByLabel('API key',{exact:true}).fill(privateKey);
  await page.getByRole('button',{name:'Connect',exact:true}).click();
  await page.waitForURL(u=>u.pathname==='/settings/models'&&u.searchParams.has('model'));
  const id=new URL(page.url()).searchParams.get('model');
  if((await page.content()).includes(privateKey)||page.url().includes(privateKey))throw Error('credential leaked into rendered metadata');
  if(await page.getByRole('button',{name:/Test connection|Use model/}).count())throw Error('manual model setup remains');
  return id;
 };
 const first=await connect('Primary connection');
 const initial=await read('/api/models');if(!initial.current.startsWith('profile:'+first+':'))throw Error('first connection was not selected automatically');
 const backup=await connect('Backup connection');
 const ready=await read('/api/models');if(ready.choices.length!==2||ready.current!==initial.current)throw Error('connected models are unavailable or default was overwritten');
 // Selecting a model belongs to the composer and preserves a user draft.
 await page.goto(url+'/');await page.getByLabel('Message Ghost',{exact:true}).fill('Keep my draft');
 const picker=page.getByRole('combobox',{name:'Model',exact:true});await picker.click();await page.getByRole('option',{name:'Backup connection',exact:true}).click();await picker.filter({hasText:'Backup connection'}).waitFor();
 if(await page.getByLabel('Message Ghost',{exact:true}).inputValue()!=='Keep my draft')throw Error('model selection lost draft');
 await page.goto(url+'/settings/models/'+encodeURIComponent(first));await page.waitForURL(u=>u.pathname==='/settings/models'&&u.searchParams.get('model')===first);await page.locator('#model-'+first+'>details[open]').waitFor();
 await page.setViewportSize({width:320,height:980});if(await page.evaluate(()=>document.documentElement.scrollWidth>innerWidth))throw Error('expanded connection details overflow');await page.screenshot({path:evidence+'/models-details-mobile.png',fullPage:true});await page.setViewportSize({width:1400,height:980});
 if(keyed){
  const row=page.locator('#model-'+backup),trigger=row.getByRole('button',{name:/^Manage connection:/});
  const open=async()=>{await trigger.click();await page.getByRole('menuitem',{name:'Disconnect',exact:true}).click()};
  await open();const dialog=page.getByRole('dialog',{name:'Disconnect',exact:true});await dialog.getByRole('button',{name:'Cancel',exact:true}).click();await trigger.and(page.locator(':focus')).waitFor();
  if((await read('/api/models')).choices.length!==2)throw Error('cancel disconnected key');
  await open();await dialog.getByRole('button',{name:'Disconnect',exact:true}).click();await page.waitForURL(u=>u.pathname==='/settings/models'&&!u.search);
  const remaining=await read('/api/models');if(remaining.choices.some(choice=>choice.key.startsWith('profile:'+backup+':')))throw Error('disconnected key remains usable');
  await page.goto(url+'/');await picker.click();await page.getByRole('option',{name:'Primary connection',exact:true}).click();await picker.filter({hasText:'Primary connection'}).waitFor();
 }
 for(const theme of ['light','dark']){
  await page.evaluate(value=>localStorage.setItem('steward-theme',value),theme);
  for(const width of [1400,390,320]){
   await page.setViewportSize({width,height:980});await page.goto(url+'/settings/models');await page.evaluate(()=>document.fonts.ready);
   if(await page.evaluate(()=>document.documentElement.scrollWidth>innerWidth))throw Error('model connections overflow');
   await page.screenshot({path:`${evidence}/models-${theme}-${width}.png`,fullPage:true});
   await page.getByRole('link',{name:'Connect with an API key',exact:true}).click();await selectValue(page,page.locator('[name=service]'),'custom');
   if(await page.evaluate(()=>document.documentElement.scrollWidth>innerWidth))throw Error('API form overflow');
   await page.screenshot({path:`${evidence}/api-connection-${theme}-${width}.png`,fullPage:true});
  }
 }
 // Native navigation and a password input remain usable without JavaScript.
 const native=await context.browser().newContext({javaScriptEnabled:false,viewport:{width:390,height:844}});
 try{await native.addCookies(await context.cookies());const plain=await native.newPage();await plain.goto(url+'/settings/models');await plain.getByRole('link',{name:'Connect with an API key',exact:true}).click();await plain.locator('.model-connection-advanced>summary').click();await plain.getByLabel('Endpoint URL',{exact:true}).waitFor();if(await plain.locator('[name=api_key]').getAttribute('type')!=='password')throw Error('native key input is visible text')}finally{await native.close()}
 await page.setViewportSize({width:1400,height:980});await page.goto(url+'/settings/models');
 await writeFile(evidence+'/models-connections.json',JSON.stringify({keyed,first,backup,defaultPreserved:true,composerSelection:true,credentialNotEchoed:true,nativeFallback:true},null,2));
}


// Controlled HTTP responses exercise the real picker and shared Select/Combobox.
// Actual account admission and version fences remain covered by the account flow.
export async function composerModelLoading(page,context,url,evidence){
 const key=name=>'account:owned:'+name;
 const choices=['gpt-6-luna','gpt-5.6-luna',...Array.from({length:13},(_,i)=>'fixture-model-'+String(i+1).padStart(2,'0'))].map(label=>({key:key(label),label}));
 choices.push({key:key('retired'),label:'Unavailable model',disabled:true},{key:'profile:api-connection:1',label:'API connection'});
 choices.sort((a,b)=>a.label.toLowerCase()>b.label.toLowerCase()?-1:1);
 // Match the actual SSR configuration fence before installing controlled reads.
 // React has already replaced the initial select. Read the original SSR
 // markup, rather than assuming a fixture model or inspecting an empty locator.
 const initialMarkup=await(await context.request.get(url)).text();
 const nativeFacts=await page.evaluate(markup=>{const doc=new DOMParser().parseFromString(markup,'text/html');return {picker:JSON.parse(doc.querySelector('[data-composer-model]').getAttribute('data-model-picker')),reader_revision:doc.querySelector('[data-composer-model]').getAttribute('data-reader-revision')||undefined,version:doc.querySelector('[data-composer-model]')?.getAttribute('data-config-version'),choices:[...doc.querySelectorAll('#composer-model option')].filter(option=>option.value).map(option=>({key:option.value,label:option.label}))}},initialMarkup);
 if(!nativeFacts.choices.length||!nativeFacts.version)throw Error('saved native model fallback unavailable');
 const nativeChoice=nativeFacts.choices[0];
 const initialVersion=Number(nativeFacts.version);
 if(!Number.isSafeInteger(initialVersion))throw Error('initial model configuration version unavailable');
 let current=key('gpt-6-luna'),version=initialVersion,reads=0,mode='initial',release,started,dropSwitch=false,switches=0;
 const blocked=new Promise(resolve=>{release=resolve});const firstRead=new Promise(resolve=>{started=resolve});
 let held=blocked,begin=started;
 const json=body=>({status:200,contentType:'application/json',body:JSON.stringify(body)});
 await page.route('**/api/models',async route=>{
  reads++;const snapshot={choices,current,config_version:version,picker:nativeFacts.picker,...(nativeFacts.reader_revision?{reader_revision:nativeFacts.reader_revision}:{})};
  if(mode==='initial'||mode==='delayed'){begin();await held}
  if(mode==='failed'){await route.fulfill({status:503});return}
  if(mode==='partial'){await route.fulfill(json({...snapshot,choices:[...choices.filter(choice=>choice.key===current||choice.label.startsWith('gpt-')||choice.key.startsWith('profile:')),{...choices.find(choice=>choice.label==='fixture-model-09'),disabled:true}],unavailable:true}));return}
  await route.fulfill(json(snapshot)).catch(()=>{});
 });
 await page.route('**/model/choose',async route=>{
  const form=new URLSearchParams(route.request().postData());switches++;
  if(Number(form.get('config_version'))!==version)throw Error('stale catalog overwrote confirmed selection version');
  current=form.get('choice');version++;
  if(current.startsWith('profile:')){
   const choice=choices.find(choice=>choice.key===current);
   current='profile:api-connection:'+version;choice.key=current;
  }
  if(dropSwitch){dropSwitch=false;await route.abort();return}
  // The production acknowledgement deliberately includes only saved profiles.
  await route.fulfill(json({picker:nativeFacts.picker,...(nativeFacts.reader_revision?{reader_revision:nativeFacts.reader_revision}:{}),current,config_version:version,choices:choices.filter(choice=>choice.key===current||choice.key.startsWith('profile:'))}));
 });
 await page.reload();await firstRead;
 const picker=page.locator('#composer-model-picker');await picker.waitFor();
 const draft=page.getByLabel('Message Ghost',{exact:true});await draft.fill('Keep this delayed model-switch draft.');
 await page.getByLabel('Attach a file').setInputFiles({name:'picker-draft.txt',mimeType:'text/plain',buffer:Buffer.from('keep attachment')});
 await picker.click();await page.getByRole('status').filter({hasText:'Loading models…'}).waitFor();
 if(await page.getByRole('option').count()||reads!==1)throw Error('first open exposed partial models or duplicated the prefetch');
 await page.screenshot({path:evidence+'/model-picker-loading.png'});
 await page.keyboard.press('Escape');await picker.and(page.locator(':focus')).waitFor();await picker.click();
 if(reads!==1)throw Error('reopening duplicated an in-flight catalogue read');
 mode='ready';release();
 await page.getByRole('option',{name:'fixture-model-13',exact:true}).waitFor();
 if(await page.getByRole('option').count()!==choices.length)throw Error('full catalogue did not replace loading state');
 await page.getByRole('option',{name:'fixture-model-13',exact:true}).click();await picker.filter({hasText:'fixture-model-13'}).waitFor();
 const assertDraft=async()=>{if(await draft.inputValue()!=='Keep this delayed model-switch draft.'||await page.getByLabel('Attach a file').evaluate(node=>node.files?.[0]?.name)!=='picker-draft.txt')throw Error('model switch discarded draft or attachment')};
 await assertDraft();
 mode='delayed';let reading;const nextRead=new Promise(resolve=>{reading=resolve});begin=reading;held=new Promise(resolve=>{release=resolve});
 await picker.click();await nextRead;
 const available=page.getByRole('option',{name:'fixture-model-12',exact:true});
 if(await page.getByRole('option').count()!==choices.length||await available.getAttribute('aria-disabled')==='true')throw Error('background refresh shrank or disabled the complete catalogue');
 if(await page.getByRole('option',{name:'Unavailable model',exact:true}).getAttribute('aria-disabled')!=='true')throw Error('real unavailable model became selectable');
 await available.click();await picker.filter({hasText:'fixture-model-12'}).waitFor();mode='ready';release();
 await assertDraft();
 mode='partial';await picker.click();await page.getByText('Some account models are unavailable. Try again.',{exact:true}).waitFor();
 if(await page.getByRole('option').count()!==choices.length)throw Error('partial catalogue response discarded the complete list');
 if(await page.getByRole('option',{name:'fixture-model-09',exact:true}).getAttribute('aria-disabled')!=='true')throw Error('partial catalogue ignored an authoritative disabled flag');
 await page.keyboard.press('Escape');
 mode='failed';await picker.click();await page.getByText('The account model list is unavailable.',{exact:true}).waitFor();
 if(await page.getByRole('option').count()!==choices.length)throw Error('failed refresh lost cached choices');
 await page.getByRole('option',{name:'fixture-model-11',exact:true}).click();await picker.filter({hasText:'fixture-model-11'}).waitFor();
 mode='ready';dropSwitch=true;await picker.click();await page.getByRole('option',{name:'fixture-model-10',exact:true}).click();await picker.filter({hasText:'fixture-model-10'}).waitFor();await assertDraft();
 if(await page.getByText('Model selection failed. Try again.',{exact:true}).count())throw Error('unknown applied selection failed readback');
 dropSwitch=true;await picker.click();await page.getByRole('option',{name:'API connection',exact:true}).click();await picker.filter({hasText:'API connection'}).waitFor();
 if(await page.getByText('Model selection failed. Try again.',{exact:true}).count())throw Error('canonical profile selection failed readback after a lost response');
 const changedProfile=choices.find(choice=>choice.key.startsWith('profile:'));changedProfile.key='profile:api-connection:'+ ++version;current=changedProfile.key;
 mode='partial';await picker.click();await page.getByText('Some account models are unavailable. Try again.',{exact:true}).waitFor();await page.getByRole('option',{name:'API connection',exact:true}).waitFor();
 if(await page.getByRole('option').count()!==choices.length)throw Error('new profile version duplicated or removed cached choices');
 mode='ready';await page.getByRole('option',{name:'fixture-model-10',exact:true}).click();await picker.filter({hasText:'fixture-model-10'}).waitFor();await assertDraft();
 mode='failed';dropSwitch=true;const beforeUnknown=switches;
 await picker.click();await page.getByRole('option',{name:'fixture-model-12',exact:true}).click();
 await page.getByText('The account model list is unavailable.',{exact:true}).waitFor();
 if(await page.getByText('Model selection failed. Try again.',{exact:true}).count()||switches!==beforeUnknown+1)throw Error('unavailable readback invented a failed outcome or replayed the selection');
 mode='ready';await picker.click();await picker.filter({hasText:'fixture-model-12'}).waitFor();await page.keyboard.press('Escape');await assertDraft();

 for(const language of ['en','zh-CN']){
  await context.request.post(url+'/settings/language',{form:{language}});
  for(const theme of ['light','dark'])for(const width of [1400,320]){
   await page.setViewportSize({width,height:980});await page.reload();await picker.waitFor();
   await page.evaluate(theme=>{localStorage.setItem('steward-theme',theme);document.documentElement.classList.toggle('dark',theme==='dark')},theme);
   await picker.click();await page.getByRole('option',{name:'fixture-model-13',exact:true}).waitFor();
   await page.evaluate(()=>document.fonts.ready);
   if(await page.evaluate(()=>document.documentElement.scrollWidth>innerWidth))throw Error('model picker overflowed');
   await page.screenshot({path:`${evidence}/model-picker-${language}-${theme}-${width}.png`});await page.keyboard.press('Escape');
  }
 }
 await context.request.post(url+'/settings/language',{form:{language:'en'}});
 mode='failed';await page.reload();await page.getByText('The account model list is unavailable.',{exact:true}).waitFor();
 await picker.click();const fallback=page.getByRole('option',{name:nativeChoice.label,exact:true});await fallback.waitFor();
 if(await fallback.getAttribute('aria-disabled')==='true')throw Error('initial read failure disabled saved fallback');
 await page.keyboard.press('Escape');mode='ready';await picker.click();await page.getByRole('option',{name:'fixture-model-13',exact:true}).waitFor();await page.keyboard.press('Escape');
 for(const mode of ['no-script','fetch-unavailable']){
  const plain=await context.browser().newContext({javaScriptEnabled:mode!=='no-script'});
  try{
   await plain.addCookies(await context.cookies());
   if(mode==='fetch-unavailable')await plain.addInitScript(()=>{window.fetch=undefined});
   const fallback=await plain.newPage();await fallback.goto(url);
   await fallback.locator('#composer-model').waitFor();await fallback.locator('.model-native-submit').waitFor();
   await fallback.locator('#composer-model').selectOption(nativeChoice.key);
   if(await fallback.locator('#composer-model').inputValue()!==nativeChoice.key)throw Error(mode+' lost native model selection');
  }finally{await plain.close()}
 }
 await writeFile(evidence+'/composer-model-loading.json',JSON.stringify({prefetch:true,singleFlight:true,noPartialMenu:true,completeAfterSwitch:true,canonicalProfileVersion:true,usableDuringRefresh:true,failedRefreshRetainsChoices:true,initialFailureRetry:true,partialCatalogueRetainsChoices:true,nativeFallback:true,lateReadFenced:true,unknownSelectionReadback:true,unavailableReadbackDoesNotInventFailure:true,draftAndAttachment:true,models:choices.length},null,2));
}

export async function modelPluginPolicy(page,context,url,evidence){
 const expected=process.env.STEWARD_MODEL_POLICY_ENDPOINT;
 if(!expected)throw Error('owner policy expectation missing');
 await page.goto(url+'/settings/models/new');
 await page.getByRole('heading',{name:'Connect with an API key',exact:true}).waitFor();
 const provider=page.locator('[name=service]'),endpoint=page.locator('[name=endpoint]'),key=page.locator('[name=api_key]');
 await selectValue(page,provider,'openai');
 if(await endpoint.inputValue()!=='https://api.openai.com/v1'||!await endpoint.evaluate(node=>node.readOnly)||!await key.evaluate(node=>node.required))throw Error('explicit provider field bindings changed');
 await selectValue(page,provider,'custom');
 const state=await page.locator('[data-api-connection]').evaluate(form=>({endpoint:form.querySelector('[name=endpoint]').value,required:form.querySelector('[name=api_key]').required,readOnly:form.querySelector('[name=endpoint]').readOnly,open:form.querySelector('.model-connection-advanced').open}));
 if(state.endpoint!==expected||state.required||state.readOnly||!state.open)throw Error('selected owner field policy not applied: '+JSON.stringify(state));
 await writeFile(evidence+'/model-plugin-policy.json',JSON.stringify(state,null,2)+'\n');
}

export async function modelPickerVersion(page,context,url,evidence){
 const expected=process.env.STEWARD_MODEL_PICKER_LABEL, pending=process.env.STEWARD_MODEL_PICKER_PENDING, busy=process.env.STEWARD_MODEL_PICKER_BUSY;
 if(!expected||!pending||!['true','false'].includes(busy))throw Error('independent picker expectation missing');
 let release,begin,changes=0;const held=new Promise(resolve=>release=resolve),started=new Promise(resolve=>begin=resolve);
 page.on('request',request=>{if(new URL(request.url()).pathname==='/model/choose')changes++});
 await page.route('**/api/models',async route=>{begin();await held;await route.continue().catch(()=>{})});
 await page.reload();await started;
 const picker=page.getByRole('combobox',{name:expected,exact:true});await picker.waitFor();
 if(await picker.getAttribute('aria-busy')!==busy)throw Error('actual picker ignored owning loading policy');
 await picker.click();await page.getByRole('status').filter({hasText:pending}).waitFor();
 if(await page.getByRole('option').count())throw Error('pending picker exposed incomplete choices');
 await page.screenshot({path:evidence+'/picker-owned-version.png'});
 release();await page.getByRole('option').first().waitFor();await page.keyboard.press('Escape');
 if(changes)throw Error('presentation change activated a model');
 await writeFile(evidence+'/picker-owned-version.json',JSON.stringify({label:expected,pending,busy,activation_requests:changes},null,2));
}

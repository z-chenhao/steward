import assert from 'node:assert/strict';
import {mkdir,writeFile} from 'node:fs/promises';
import {chromium} from '../../guest-browser/node_modules/playwright-core/index.mjs';
import {waitForUIState} from '../../guest-browser/ui-state.mjs';
const[url,out,phase,ref='',caption='Original form']=process.argv.slice(2);await mkdir(out,{recursive:true});
const browser=await chromium.launch({headless:true,...(process.env.STEWARD_BROWSER_EXECUTABLE?{executablePath:process.env.STEWARD_BROWSER_EXECUTABLE}:{})});let page;
try{
 const context=await browser.newContext({locale:'en-US',viewport:{width:1400,height:980},reducedMotion:'reduce'});page=await context.newPage();page.setDefaultTimeout(12000);await page.goto(url);await page.getByLabel('Workspace access token',{exact:true}).fill(process.env.STEWARD_WEB_TEST_TOKEN);await page.getByRole('button',{name:'Open workspace'}).click();
 const preview=phase.endsWith('preview'),variant=caption.startsWith('Form v124:'),notes=variant?'Saved new':'Saved old';
 const scope=preview?page.locator('[data-ui-preview]'):page.locator('[data-ui-spec*='+JSON.stringify('"view_ref":"'+ref+'"')+']');
 // SSR fallback has field.notes; wait for the actual managed native driver.
 const form=scope.locator('.native-input-form:has(textarea[name="notes"])');await form.getByRole('textbox',{name:'Notes',exact:true}).waitFor();await form.getByRole('heading',{name:caption,exact:true}).waitFor();const detectedAt=Date.now();
 if(preview){
  assert.equal(await form.evaluate(node=>Boolean(node.closest('[inert]'))),true);assert.equal(await form.getByRole('textbox',{name:'Notes',exact:true}).isDisabled(),true);assert.equal(await form.getByRole('checkbox',{name:'Confirm',exact:true}).isChecked(),variant);assert.equal(await form.evaluate(node=>node.closest('[data-form-id]')?.getAttribute('data-form-id')??''),'');await form.evaluate(node=>node.requestSubmit());
  await form.screenshot({path:out+'/preview.png'});await writeFile(out+'/report.json',JSON.stringify({phase,detectedAt,actualApp:true,inert:true,variant,owningInitialConfirmation:variant,noFormIdentity:true},null,2));
 }else{
  const reply=await context.request.get(url+'/ui/form-view?'+new URLSearchParams({ref}));assert.equal(reply.status(),200);const display=await reply.json();assert.equal(display.props.title,caption);assert.equal(display.defaults.confirm,String(variant));
  if(phase.startsWith('formal')){
   assert.equal(await form.getByRole('checkbox',{name:'Confirm',exact:true}).isChecked(),variant,'new Run uses real owning initial value');await form.getByRole('textbox',{name:'Notes',exact:true}).fill(notes);await form.getByRole('checkbox',{name:'Confirm',exact:true}).setChecked(!variant);
   await waitForUIState(page,ref,state=>state.state.values?.notes===notes&&state.state.values?.confirm===String(!variant)&&state.model_context.title===caption);
  }
  const state=await waitForUIState(page,ref,state=>state.state.values?.notes===notes&&state.state.values?.confirm===String(!variant)&&state.model_context.title===caption);
  await form.getByRole('textbox',{name:'Notes',exact:true}).evaluate((node,value)=>new Promise((resolve,reject)=>{const deadline=performance.now()+5000;const check=()=>node.value===value?resolve():performance.now()>deadline?reject(Error('first restored original draft unavailable')):setTimeout(check,25);check()}),notes);assert.equal(await form.getByRole('checkbox',{name:'Confirm',exact:true}).isChecked(),!variant);
  const formID=await form.evaluate(node=>node.closest('[data-form-id]')?.getAttribute('data-form-id'));assert.match(formID,/^[a-f0-9]{64}$/);
  const nativeContext=await browser.newContext({javaScriptEnabled:false,storageState:await context.storageState(),viewport:{width:1400,height:980}});
  try{
   const nativePage=await nativeContext.newPage();await nativePage.goto(url);
   const nativeForm=nativePage.locator('form[data-form-id="'+formID+'"]');
   await nativeForm.getByRole('heading',{name:caption,exact:true}).waitFor();
   assert.equal(await nativeForm.locator('select[name="field.confirm"]').inputValue(),String(variant),'SSR uses owning initial value');
   assert.equal(await nativeForm.locator('input[name="form_id"]').inputValue(),formID);
   assert.ok(await nativeForm.locator('input[name="message_id"]').inputValue());
   await nativeForm.screenshot({path:out+'/form-no-javascript.png'});
  }finally{await nativeContext.close()}
  for(const width of[1400,390])for(const theme of['light','dark']){await page.setViewportSize({width,height:980});await page.evaluate(theme=>document.documentElement.classList.toggle('dark',theme==='dark'),theme);await form.scrollIntoViewIfNeeded();await form.screenshot({path:out+'/form-'+width+'-'+theme+'.png'})}
  await writeFile(out+'/report.json',JSON.stringify({phase,detectedAt,actualApp:true,variant,states:state,formID,originalOrdinal:1,owningInitialConfirmation:variant,userDraftOverridesInitial:true,serverRenderedOwningDisplay:true},null,2));
 }
 await context.close();
}catch(error){if(page){await page.screenshot({path:out+'/failure.png'});await writeFile(out+'/failure.txt',await page.locator('body').innerText())}throw error}finally{await browser.close()}

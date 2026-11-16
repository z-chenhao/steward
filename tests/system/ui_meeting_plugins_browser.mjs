import assert from 'node:assert/strict';
import {mkdir,writeFile} from 'node:fs/promises';
import {chromium} from '../../guest-browser/node_modules/playwright-core/index.mjs';
const[url,out,phase,ref='',caption='Review']=process.argv.slice(2);await mkdir(out,{recursive:true});
const browser=await chromium.launch({headless:true,...(process.env.STEWARD_BROWSER_EXECUTABLE?{executablePath:process.env.STEWARD_BROWSER_EXECUTABLE}:{})});let page;
try{
 const context=await browser.newContext({viewport:{width:1400,height:980},reducedMotion:'reduce'});page=await context.newPage();page.setDefaultTimeout(12000);await page.goto(url);await page.getByLabel('Workspace access token',{exact:true}).fill(process.env.STEWARD_WEB_TEST_TOKEN);await page.getByRole('button',{name:'Open workspace'}).click();
 const preview=phase.endsWith('preview'),variant=caption.startsWith('Meeting v126:'),scope=preview?page.locator('[data-ui-preview]'):page.locator('[data-ui-spec*='+JSON.stringify('"widget_ref":"'+ref+'"')+']');
 const drawing=scope.locator('.native-time-picker');await drawing.waitFor();const detectedAt=Date.now();
 if(preview){
  await drawing.getByRole('heading',{name:caption,exact:true}).waitFor();assert.equal(await drawing.locator('button').count(),3);assert.equal(await drawing.locator('button:enabled').count(),0);assert.equal(await drawing.evaluate(node=>Boolean(node.closest('[inert]'))),true);assert.equal(await drawing.evaluate(node=>node.closest('[data-form-id]')?.getAttribute('data-form-id')??''),'');
  await drawing.screenshot({path:out+'/preview.png'});await writeFile(out+'/report.json',JSON.stringify({phase,detectedAt,actualApp:true,variant,inert:true,noIngressIdentity:true},null,2));
 }else{
  const spec=await scope.evaluate(node=>JSON.parse(node.dataset.uiSpec)),component=spec.components.find(c=>c.widget_ref===ref);assert.ok(component?.meeting_view);const formID=component.form_id;assert.match(formID,/^[a-f0-9]{64}$/);const view=spec.meeting_views[formID];assert.equal(view.title,caption);assert.equal(view.decline_label,variant?'Try other times':'None of these work');assert.deepEqual(view.slots.map(s=>s.choice),[0,1]);
  const nativeContext=await browser.newContext({javaScriptEnabled:false,storageState:await context.storageState()});try{const native=await nativeContext.newPage();await native.goto(url);const form=native.locator('form[data-form-id="'+formID+'"]');assert.equal(await form.locator('p').first().innerText(),caption);assert.equal(await form.locator('input[name="form_id"]').inputValue(),formID);assert.equal(await form.locator('input[name="message_id"]').inputValue(),component.message_id);assert.equal(await form.locator('button[name="field.choice"]').first().getAttribute('value'),'0')}finally{await nativeContext.close()}
  if(phase==='formal'||phase==='upgraded'){
   await drawing.getByRole('heading',{name:caption,exact:true}).waitFor();await drawing.getByRole('button',{name:view.decline_label,exact:true}).waitFor();
   for(const width of[1400,390])for(const theme of['light','dark']){await page.setViewportSize({width,height:980});await page.evaluate(theme=>document.documentElement.classList.toggle('dark',theme==='dark'),theme);await drawing.screenshot({path:out+'/meeting-'+width+'-'+theme+'.png'})}
   await drawing.locator('.native-time-option').nth(variant?1:0).click();
  }else{assert.equal(component.response,variant?'1':'0');assert.equal(component.submitted,true)}
  await drawing.getByRole('heading',{name:view.selected_title,exact:true}).waitFor();assert.ok((await drawing.innerText()).includes(variant?'2026-11-18 at 14:00':'2026-11-17 at 09:15'));assert.equal(await drawing.locator('button').count(),0);
  await writeFile(out+'/report.json',JSON.stringify({phase,detectedAt,actualApp:true,variant,formID,messageID:component.message_id,view,originalOrdinal:1,choice:variant?'1':'0',noJavaScript:true},null,2));
 }
 await context.close();
}catch(error){if(page){await page.screenshot({path:out+'/failure.png'});await writeFile(out+'/failure.txt',await page.locator('body').innerText());await writeFile(out+'/failure-navigation.json',JSON.stringify({requested:url,current:page.url()},null,2))}throw error}finally{await browser.close()}

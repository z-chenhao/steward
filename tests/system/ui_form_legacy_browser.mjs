import assert from 'node:assert/strict';
import {mkdir,writeFile} from 'node:fs/promises';
import {chromium} from '../../guest-browser/node_modules/playwright-core/index.mjs';
import {waitForUIState} from '../../guest-browser/ui-state.mjs';
const[url,out,phase,ref]=process.argv.slice(2);await mkdir(out,{recursive:true});
const browser=await chromium.launch({headless:true,...(process.env.STEWARD_BROWSER_EXECUTABLE?{executablePath:process.env.STEWARD_BROWSER_EXECUTABLE}:{})});let page;
try{
 const context=await browser.newContext({viewport:{width:1400,height:980},reducedMotion:'reduce'});page=await context.newPage();page.setDefaultTimeout(12000);await page.goto(url);await page.getByLabel('Workspace access token',{exact:true}).fill(process.env.STEWARD_WEB_TEST_TOKEN);await page.getByRole('button',{name:'Open workspace'}).click();
 const scope=page.locator('[data-ui-spec*='+JSON.stringify('"widget_ref":"'+ref+'"')+']');const form=scope.locator('.native-input-form:has(textarea[name="notes"])');await form.getByRole('textbox',{name:'Notes',exact:true}).waitFor();
 if(phase==='before'){await form.getByRole('textbox',{name:'Notes',exact:true}).fill('Retained draft 中文');await form.getByRole('checkbox',{name:'Confirm',exact:true}).check();await waitForUIState(page,ref,state=>state.state.values?.notes==='Retained draft 中文'&&state.state.values?.confirm==='true');await page.reload();await form.getByRole('textbox',{name:'Notes',exact:true}).waitFor()}
 await form.getByRole('textbox',{name:'Notes',exact:true}).evaluate(node=>new Promise((resolve,reject)=>{const deadline=performance.now()+5000;const check=()=>node.value==='Retained draft 中文'?resolve():performance.now()>deadline?reject(Error('first legacy draft recovery failed')):setTimeout(check,25);check()}));assert.equal(await form.getByRole('checkbox',{name:'Confirm',exact:true}).isChecked(),true);
 const states=await waitForUIState(page,ref,state=>state.state.values?.notes==='Retained draft 中文'&&state.state.values?.confirm==='true');const fields=await form.locator('input,textarea').evaluateAll(nodes=>nodes.map(node=>({tag:node.tagName,type:node.type,name:node.name,id:node.id,value:node.value,checked:node.checked??null,required:node.required})));
 await form.screenshot({path:out+'/form.png'});await writeFile(out+'/report.json',JSON.stringify({phase,fields,states,native:true},null,2));await context.close();
}catch(error){if(page){await page.screenshot({path:out+'/failure.png'});await writeFile(out+'/failure.txt',await page.locator('body').innerText())}throw error}finally{await browser.close()}

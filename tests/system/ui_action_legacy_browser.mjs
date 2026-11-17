import assert from 'node:assert/strict';
import {mkdir,writeFile} from 'node:fs/promises';
import {chromium} from '../../guest-browser/node_modules/playwright-core/index.mjs';
import {waitForUIState} from '../../guest-browser/ui-state.mjs';
const[url,out,phase,ref,variantText='false']=process.argv.slice(2);const variant=variantText==='true';await mkdir(out,{recursive:true});
const browser=await chromium.launch({headless:true,...(process.env.STEWARD_BROWSER_EXECUTABLE?{executablePath:process.env.STEWARD_BROWSER_EXECUTABLE}:{})});let page;
try{
 const context=await browser.newContext({viewport:{width:1400,height:980},reducedMotion:'reduce'});page=await context.newPage();page.setDefaultTimeout(12000);const posts=[];page.on('request',req=>{if(req.method()==='POST'&&new URL(req.url()).pathname==='/api/ui/action')posts.push(JSON.parse(req.postData()))});
 await page.goto(url);await page.getByLabel('Workspace access token',{exact:true}).fill(process.env.STEWARD_WEB_TEST_TOKEN);await page.getByRole('button',{name:'Open workspace'}).click();
 const scope=page.locator('[data-ui-spec*='+JSON.stringify('"widget_ref":"'+ref+'"')+']'),button=scope.getByRole('button',{name:'Refresh catalog',exact:true});await button.waitFor();
 const spec=await scope.evaluate(node=>JSON.parse(node.dataset.uiSpec)),c=spec.components.find(c=>c.widget_ref===ref);assert.equal(c.action_view,undefined);assert.equal(spec.action_views?.[ref],undefined);const view=null;
 const pendingRef=ref.replace(/#1$/, '#2'),pending=page.locator('[data-ui-spec*='+JSON.stringify('\"widget_ref\":\"'+pendingRef+'\"')+']').getByRole('button',{name:'Pending action',exact:true});await pending.waitFor();assert.equal(await pending.isEnabled(),true);
 const ready=()=>button.evaluate(node=>new Promise((resolve,reject)=>{const deadline=performance.now()+5000;const check=()=>!node.disabled?resolve():performance.now()>deadline?reject(Error('original action driver not ready')):setTimeout(check,20);check()}));await ready();
 if(phase==='before'){
  await button.evaluate(node=>node.dispatchEvent(new MouseEvent('click',{bubbles:true})));assert.equal(posts.length,0);
  for(const width of[1400,390])for(const theme of['light','dark']){await page.setViewportSize({width,height:980});await page.evaluate(theme=>document.documentElement.classList.toggle('dark',theme==='dark'),theme);await button.screenshot({path:out+'/action-'+width+'-'+theme+'.png'})}
  await button.click();
 }
 const state=await waitForUIState(page,ref,s=>typeof s.state.source==='string'&&s.state.source.length>=16);const source=state.state.source;
 const result=await page.evaluate(async({ref,source})=>{const deadline=performance.now()+10000;for(;;){const r=await fetch('/api/ui/action?'+new URLSearchParams({ref,source}));if(!r.ok)throw Error('original action unavailable');const value=await r.json();if(value.status==='succeeded')return value;if(!['pending','waiting'].includes(value.status)||performance.now()>deadline)throw Error('actual original action did not succeed: '+value.status);await new Promise(resolve=>setTimeout(resolve,40))}},{ref,source});
 await ready();if(!variant)await button.locator('svg').waitFor();assert.equal(await button.locator('svg').count(),variant?0:1);assert.equal(await scope.locator('[role=alert]').count(),0);assert.equal(posts.length,(phase==='before')?1:0,'read/recovery unexpectedly posted an action');
 if(posts.length){assert.equal(posts[0].ref,ref);assert.equal(posts[0].source_id,source);assert.deepEqual(Object.keys(posts[0]).sort(),['ref','source_id'])}
 await writeFile(out+'/report.json',JSON.stringify({phase,actualApp:true,variant,ref,source,state,result,view,posts:posts.length,historicalDriver:true,pendingText:await pending.innerText()},null,2));await context.close();
}catch(error){if(page){await page.screenshot({path:out+'/failure.png'});await writeFile(out+'/failure.txt',await page.locator('body').innerText())}throw error}finally{await browser.close()}

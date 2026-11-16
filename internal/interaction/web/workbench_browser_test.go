package web

import (
	"context"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/z-chenhao/assistant/internal/plugin/webworkbench/contract"
)

func TestWorkbenchBrowserOriginalStylesAndPinnedUpdates(t *testing.T) {
	if os.Getenv("STEWARD_TEST_APP_BROWSER") != "1" {
		t.Skip("run by browser gate")
	}
	original := originalWorkbenchPresentation(t)
	ref := "sha256:" + strings.Repeat("a", 64)
	legacy := languageServer()
	legacy.Tasks = workspaceReader{}
	legacy.Memory = &ownerMemoryReader{}
	managed := languageServer()
	managed.Tasks = workspaceReader{}
	managed.Memory = &ownerMemoryReader{}
	managed.WebWorkbench = func(_ context.Context, reference string) (contract.Presentation, string, bool, error) {
		if reference != "" && reference != ref {
			return contract.Presentation{}, "", true, errWorkbenchUnavailable
		}
		return original, ref, true, nil
	}
	a, b := httptest.NewServer(legacy), httptest.NewServer(managed)
	defer a.Close()
	defer b.Close()
	evidence := os.Getenv("STEWARD_APP_BROWSER_EVIDENCE")
	if evidence == "" {
		evidence = t.TempDir()
	}
	evidence = filepath.Join(evidence, "web-workbench")
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	script := `import {chromium} from '../../../guest-browser/node_modules/playwright-core/index.mjs';
import {mkdir,writeFile} from 'node:fs/promises';
import {assertDataList} from '../../../tests/system/channels_controls.mjs';
const [legacy,managed,evidence,revision]=process.argv.slice(1);await mkdir(evidence,{recursive:true});
const browser=await chromium.launch({headless:true,...(process.env.STEWARD_BROWSER_EXECUTABLE?{executablePath:process.env.STEWARD_BROWSER_EXECUTABLE}:{})});
const results=[];
try{
 for(const mobile of [false,true])for(const theme of ['light','dark']){
  const context=await browser.newContext({extraHTTPHeaders:{Authorization:"Bearer private-token-at-least-24-bytes"},viewport:mobile?{width:390,height:844}:{width:1440,height:1000},colorScheme:theme});
  const page=await context.newPage(),errors=[];page.on('pageerror',error=>errors.push(error.message));
  for(const path of ['/tasks','/tasks/task:visible','/tasks/executions/owned-root','/']){
   const samples=[];
   for(const [index,url] of [legacy,managed].entries()){
    await page.goto(url+path);await page.evaluate(()=>document.fonts.ready);
    const main=page.locator('main');await main.waitFor();
    if(await page.locator('.task-table').count()===0&&path==='/tasks')throw Error('nonempty table unavailable '+url+' '+await page.locator('h1').textContent());
    const sample=await main.evaluate(main=>{
     const properties=['display','position','fontFamily','fontSize','lineHeight','fontWeight','color','backgroundColor','paddingTop','paddingRight','paddingBottom','paddingLeft','marginTop','marginBottom','borderTopWidth','borderRadius','gridTemplateColumns','gap'];
     return [...main.querySelectorAll('h1,h2,.task-rule-name,.task-table th,.task-table td,.task-objective,.task-rule-facts,.execution-heading,.execution-summary,.execution-main,.welcome,.welcome p,.composer,.composer textarea')].map(node=>{
      const style=getComputedStyle(node);return {tag:node.tagName,cls:node.className,styles:Object.fromEntries(properties.map(key=>[key,style[key]]))};
     });
    });
    if(sample.length===0)throw Error('empty computed style evidence '+path);
    samples.push(sample);
    if(index===1){
     const config=await page.locator('#web-workbench-config').textContent();if(JSON.parse(config).revision!==revision)throw Error('managed config lost graph');
     const sheet=page.locator('link[href*="/api/workbench/style?"]');if(await sheet.count()!==1)throw Error('managed stylesheet missing');
     const css=await context.request.get(new URL(await sheet.getAttribute('href'),managed).href);if(!css.ok()||css.headers()['x-steward-workbench-reader']!==revision)throw Error('stylesheet lost graph');
     await page.screenshot({path:evidence+'/'+theme+'-'+(mobile?'mobile':'desktop')+'-'+(path==='/'?'chat':path.includes('executions')?'execution':path==='/tasks'?'list':'task')+'.png',fullPage:true});
    }
   }
   if(JSON.stringify(samples[0])!==JSON.stringify(samples[1]))throw Error('computed styles differ '+theme+' '+mobile+' '+path+'\n'+JSON.stringify(samples.map(sample=>sample.filter((row,i)=>JSON.stringify(row)!==JSON.stringify(samples[0][i])))));
  }

  // Retained native Memory has an old 12px toolbar override. Current shared
  // controls must preserve search/filter alignment without rewriting its CSS.
  await page.goto(managed+'/developer/memory');
  await page.getByRole('button',{name:'Filters',exact:true}).waitFor();
  if(await page.locator('tr[data-memory-ref]').count()!==1)throw Error('empty compatibility list fixture');
  await page.setViewportSize({width:320,height:844});await assertDataList(page);
  if(!await page.locator('link[href="/style-base.css"]').count())throw Error('current Memory still uses frozen shared controls');
  await page.screenshot({path:evidence+'/'+theme+'-'+(mobile?'mobile':'desktop')+'-memory-compat-320.png',fullPage:true});
  await page.goto(managed+'/tasks');
  const update=page.waitForResponse(response=>response.url().includes('/api/updates?'));
  const response=await update;
  if(!response.ok()||response.request().headers()['x-steward-workbench-reader']!==revision||response.headers()['x-steward-workbench-reader']!==revision)throw Error('live update lost original reader');
  await page.locator('[data-execution-open]').first().click();
  await page.locator('.control-sheet #execution-detail').waitFor();
  if(!page.url().includes('/tasks/executions/owned-root'))throw Error('sheet history missing');
  await page.goBack();await page.locator('.control-sheet #execution-detail').waitFor({state:'hidden'});
  await page.goForward();await page.locator('.control-sheet #execution-detail').waitFor();
  if(errors.length)throw Error(errors.join('\n'));
  results.push({mobile,theme,nonempty_style_views:4,pinned_updates:true,sheet_back_forward:true});await context.close();
 }
 await writeFile(evidence+'/result.json',JSON.stringify(results,null,2));
}finally{await browser.close()}`
	cmd := exec.CommandContext(ctx, "node", "--input-type=module", "-e", script, a.URL, b.URL, evidence, ref)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatal(string(out), err)
	}
}

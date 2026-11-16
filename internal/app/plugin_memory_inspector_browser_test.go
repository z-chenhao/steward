package app

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

// Actual App, SQLite, sealed plugin process, authenticated routes and original
// shared controls. Managed readiness requires the frozen opaque Worker handshake.
func checkMemoryInspectorManagedBrowser(t *testing.T, handler http.Handler, token, revision, ref, styleDigest, chineseTitle, margin, scenario string) {
	t.Helper()
	server := httptest.NewServer(handler)
	defer server.Close()
	evidence := os.Getenv("STEWARD_MEMORY_MANAGED_BROWSER_EVIDENCE")
	if evidence == "" {
		evidence = t.TempDir()
	}
	if scenario != "baseline" {
		evidence = filepath.Join(evidence, scenario)
	}
	englishTitle := "Memory details"
	if scenario == "upgrade" {
		englishTitle = "Independent memory details"
	}
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()
	script := `import {chromium} from '../../guest-browser/node_modules/playwright-core/index.mjs';
import {mkdir,writeFile} from 'node:fs/promises';import {createHash} from 'node:crypto';
const [url,evidence,token,revision,ref,expectedStyle,chineseTitle,margin,englishTitle]=process.argv.slice(1);await mkdir(evidence,{recursive:true});
const browser=await chromium.launch({headless:true,...(process.env.STEWARD_BROWSER_EXECUTABLE?{executablePath:process.env.STEWARD_BROWSER_EXECUTABLE}:{})});
try{
 const context=await browser.newContext({viewport:{width:1440,height:1000}});await context.request.post(url+'/session',{form:{token,next:'/developer/memory'}});const page=await context.newPage();
 const program=page.waitForResponse(r=>new URL(r.url()).pathname==='/api/developer/memory/program');
 const styles=page.waitForResponse(r=>new URL(r.url()).pathname==='/api/developer/memory/style');await page.goto(url+'/developer/memory');await page.locator('#memory-page[data-memory-ready]').waitFor();
 const programResponse=await program;if(programResponse.status()!==200||new URL(programResponse.url()).searchParams.get('inspector_revision')!==revision||!programResponse.headers()['content-security-policy']?.includes('sandbox allow-scripts;')||programResponse.headers()['content-security-policy']?.includes('allow-same-origin'))throw Error('Managed client did not load exact opaque program');
 if(await page.locator('iframe[title="Memory isolated presentation"][sandbox="allow-scripts"]').count()!==1)throw Error('Memory program relay missing or duplicated');
 const response=await styles;const source=await response.text();if(response.status()!==200||new URL(response.url()).searchParams.get('inspector_revision')!==revision||'sha256:'+createHash('sha256').update(source).digest('hex')!==expectedStyle)throw Error('Actual stylesheet not from exact original graph');
 if(await page.locator('link[href="/memory/legacy-v1.css"]').count())throw Error('Managed page loaded legacy stylesheet');
 await page.setViewportSize({width:390,height:844});await page.waitForFunction(()=>!document.querySelector('#memory-filter-rail').open);
 await page.setViewportSize({width:780,height:1000});await page.waitForFunction(expected=>document.querySelector('#memory-filter-rail').open===expected,margin==='24px');
 await page.setViewportSize({width:1440,height:1000});await page.waitForFunction(()=>document.querySelector('#memory-filter-rail').open);
 await page.locator('a.memory-open',{hasText:'Original managed inspector note'}).click();const sheet=page.getByRole('dialog',{name:englishTitle,exact:true});await sheet.waitFor();await sheet.locator('[data-memory-content]').waitFor();
 const detail=page.url();if(new URL(detail).searchParams.get('inspector_revision')!==revision||new URL(detail).searchParams.get('ref')!==ref)throw Error('Sheet navigation lost exact selected version/identity');
 if(await sheet.locator('[data-memory-id]').getAttribute('data-memory-id')!==ref||!await sheet.getByRole('button',{name:'Previous memory',exact:true}).isDisabled()||!await sheet.getByRole('button',{name:'Next memory',exact:true}).isDisabled())throw Error('Original singleton navigation or identity changed');
 const results=[];
 for(const [name,width,height,dark] of [['desktop-light',1440,1000,false],['desktop-dark',1440,1000,true],['mobile-dark',390,844,true],['mobile-light',390,844,false]]){
  await page.setViewportSize({width,height});await page.evaluate(dark=>document.documentElement.classList.toggle('dark',dark),dark);await page.evaluate(()=>document.fonts.ready);await page.evaluate(()=>new Promise(r=>requestAnimationFrame(()=>requestAnimationFrame(r))));
  const values=await sheet.locator('[data-memory-content]').evaluate(n=>({text:n.textContent,font:getComputedStyle(n).fontFamily,size:getComputedStyle(n).fontSize,lineHeight:getComputedStyle(n).lineHeight,color:getComputedStyle(n).color,margin:getComputedStyle(n.closest('.memory-content-section')).marginBottom,width:document.documentElement.scrollWidth,viewport:innerWidth}));
  if(values.text!=='Original authorized private content'||!values.font.includes('Inter')||values.size!==(width<760?'14px':'13px')||values.margin!==margin||values.width>values.viewport)throw Error('Original computed controls/text/layout changed: '+JSON.stringify(values));
  results.push({name,...values});await page.screenshot({path:evidence+'/managed-memory-'+name+'.png'});
 }
 await sheet.getByRole('button',{name:'Close details',exact:true}).click();await sheet.waitFor({state:'detached'});await page.waitForURL(u=>!u.searchParams.has('ref'));
 // Hold the first real authorized response, select again, then release the old
 // response. A marker on the committed view proves it was not replaced later.
 const concurrent=await context.newPage();await concurrent.goto(url+'/developer/memory');await concurrent.locator('#memory-page[data-memory-ready]').waitFor();
 let releaseFirst,firstSeen;const held=new Promise(r=>releaseFirst=r),seen=new Promise(r=>firstSeen=r);let reads=0;
 await concurrent.route('**/developer/memory?**',async route=>{
  if(!new URL(route.request().url()).searchParams.has('ref'))return route.continue();
  const response=await route.fetch();if(++reads===1){firstSeen();await held}
  try{await route.fulfill({response})}catch(error){if(reads<2)throw error}
 });
 const originalLink=concurrent.locator('a.memory-open',{hasText:'Original managed inspector note'});
 await originalLink.click();await seen;await originalLink.click();
 const concurrentSheet=concurrent.getByRole('dialog',{name:englishTitle,exact:true});await concurrentSheet.locator('[data-memory-content]').waitFor();
 await concurrentSheet.locator('#memory-workbench').evaluate(n=>n.dataset.concurrentAccepted='second');releaseFirst();
 await concurrent.waitForTimeout(150);
 if(reads!==2||await concurrentSheet.locator('#memory-workbench').getAttribute('data-concurrent-accepted')!=='second'||await concurrent.evaluate(()=>history.state?.memorySheetDepth)!==1||new URL(concurrent.url()).searchParams.get('ref')!==ref)throw Error('Late response replaced current selection or duplicated history');
 // Exercise the browser lifecycle signals without claiming this forces a
 // browser-managed BFCache admission: terminate, then reconnect original graph.
 await concurrent.evaluate(()=>dispatchEvent(new PageTransitionEvent('pagehide',{persisted:true})));
 if(await concurrent.locator('iframe[title="Memory isolated presentation"]').count()||await concurrent.getByRole('dialog').count())throw Error('pagehide retained Worker or sheet');
 await concurrent.evaluate(()=>dispatchEvent(new PageTransitionEvent('pageshow',{persisted:true})));
 await concurrent.locator('#memory-page[data-memory-ready]').waitFor();await concurrentSheet.locator('[data-memory-content]').waitFor();
 if(await concurrentSheet.locator('[data-memory-id]').getAttribute('data-memory-id')!==ref||await concurrent.evaluate(()=>history.state?.memorySheetDepth)!==1||new URL(concurrent.url()).searchParams.get('inspector_revision')!==revision)throw Error('Lifecycle recovery lost original selection or frozen version');
 await concurrent.close();
 // An unavailable managed program leaves original frozen links usable. It must
 // never initialize the explicit pre-service client or open its side sheet.
 const unavailable=await context.newPage();await unavailable.route('**/api/developer/memory/program?**',route=>route.fulfill({status:503,body:'unavailable'}));
 await unavailable.goto(url+'/developer/memory');await unavailable.locator('#memory-page[data-memory-unavailable]').waitFor({timeout:20000});
 if(await unavailable.locator('#memory-page[data-memory-ready]').count())throw Error('Managed failure used legacy client');
 await unavailable.locator('a.memory-open',{hasText:'Original managed inspector note'}).click();await unavailable.waitForURL(u=>u.searchParams.get('ref')===ref);
 if(new URL(unavailable.url()).searchParams.get('inspector_revision')!==revision||await unavailable.getByRole('dialog').count()||await unavailable.locator('[data-memory-content]').innerText()!=='Original authorized private content')throw Error('Failed program lost frozen native navigation');
 await unavailable.close();
 const native=await browser.newContext({javaScriptEnabled:false,viewport:{width:1440,height:1000}});await native.addCookies(await context.cookies());const p=await native.newPage();const nativeStyle=p.waitForResponse(r=>new URL(r.url()).pathname==='/api/developer/memory/style');await p.goto(detail);
 if((await nativeStyle).status()!==200||await p.locator('[data-memory-id]').getAttribute('data-memory-id')!==ref||await p.locator('[data-memory-content]').innerText()!=='Original authorized private content')throw Error('Native owned detail lost original facts/style');
 await p.screenshot({path:evidence+'/managed-memory-native.png',fullPage:true});await writeFile(evidence+'/managed-memory-computed.json',JSON.stringify({revision,ref,stylesheet:expectedStyle,englishTitle,results,native:true},null,2));await native.close();
 const localized=await browser.newContext({viewport:{width:1440,height:1000}});await localized.addCookies(await context.cookies());await localized.addCookies([{name:'steward-language',value:'zh-CN',url}]);const zh=await localized.newPage();await zh.goto(url+'/developer/memory');await zh.locator('#memory-page[data-memory-ready]').waitFor();if(await zh.locator('html').getAttribute('lang')!=='zh-CN')throw Error('Chinese locale fixture not selected');
 await zh.locator('a.memory-open',{hasText:'Original managed inspector note'}).click();const zhSheet=zh.getByRole('dialog',{name:chineseTitle,exact:true});await zhSheet.waitFor();if(await zhSheet.locator('[data-memory-content]').innerText()!=='Original authorized private content'||new URL(zh.url()).searchParams.get('inspector_revision')!==revision)throw Error('Chinese owned copy changed facts/version');
 await zh.screenshot({path:evidence+'/managed-memory-chinese.png'});await localized.close();await context.close();
}finally{await browser.close()}`
	if out, err := exec.CommandContext(ctx, "node", "--input-type=module", "-e", script, server.URL, evidence, token, revision, ref, styleDigest, chineseTitle, margin, englishTitle).CombinedOutput(); err != nil {
		t.Fatalf("managed Memory browser: %v\n%s", err, out)
	}
}

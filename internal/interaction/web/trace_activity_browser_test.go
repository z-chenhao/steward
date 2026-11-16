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
)

// Hold actual Worker replies at the browser MessagePort, without changing the
// production transport or program, to prove coalescing/cancellation/disposal.
func TestTraceActivityActualDelayedRepliesCoalesceAndCancel(t *testing.T) {
	if os.Getenv("STEWARD_TEST_APP_BROWSER") != "1" {
		t.Skip("run by browser gate")
	}
	source, err := os.ReadFile("../../plugin/traceinspector/view/trace-worker.js")
	if err != nil {
		t.Fatal(err)
	}
	s := languageServer()
	s.Inspector = &inspectionReader{}
	s.Traces = &managedBrowserTraceReader{selectedTraceWorkbenchReader: &selectedTraceWorkbenchReader{revision: "sha256:" + strings.Repeat("a", 64)}, source: string(source)}
	server := httptest.NewServer(s)
	defer server.Close()
	evidence := os.Getenv("STEWARD_TRACE_ACTIVITY_BROWSER_EVIDENCE")
	if evidence == "" && os.Getenv("STEWARD_APP_BROWSER_EVIDENCE") != "" {
		evidence = filepath.Join(os.Getenv("STEWARD_APP_BROWSER_EVIDENCE"), "trace-activity-lifecycle")
	}
	if evidence == "" {
		evidence = t.TempDir()
	}
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	script := `
import assert from 'node:assert/strict';import {mkdir} from 'node:fs/promises';import {chromium} from '../../../guest-browser/node_modules/playwright-core/index.mjs';
const [url,evidence]=process.argv.slice(1);await mkdir(evidence,{recursive:true});const browser=await chromium.launch({headless:true,...(process.env.STEWARD_BROWSER_EXECUTABLE?{executablePath:process.env.STEWARD_BROWSER_EXECUTABLE}:{})});
try{const context=await browser.newContext({viewport:{width:1440,height:1000}});await context.request.post(url+'/session',{form:{token:'private-token-at-least-24-bytes'},maxRedirects:0});const page=await context.newPage();
await page.addInitScript(()=>{
 const Native=MessageChannel,set=Object.getOwnPropertyDescriptor(MessagePort.prototype,'onmessage').set;
 window.__heldActivity=[];window.__activityRequests=[];window.__holdKind=null;
 window.MessageChannel=function(){const channel=new Native(),kinds=new Map();for(const port of [channel.port1,channel.port2]){
  const send=port.postMessage;port.postMessage=function(value,...rest){if(value?.type==='activity'){kinds.set(value.seq,value.input.event.kind);window.__activityRequests.push(value)}return send.call(this,value,...rest)};
  let handler;Object.defineProperty(port,'onmessage',{configurable:true,get:()=>handler,set(value){handler=value;set.call(port,event=>{if(event.data?.type==='activity'&&kinds.get(event.data.seq)===window.__holdKind){window.__heldActivity.push(()=>handler.call(port,event));return}handler?.call(port,event)})}});
 }return channel};window.MessageChannel.prototype=Native.prototype;
});
await page.goto(url+'/developer/observability?search=keep');await page.waitForFunction(()=>document.querySelector('[data-activity-max]')?.textContent==='1');
const original=page.url(),root=page.locator('[data-trace-activity]'),chart=root.locator('.obs-activity-chart'),brush=root.locator('.obs-activity-brush'),box=await chart.boundingBox(),y=box.y+box.height/2;
await page.mouse.move(box.x+box.width*.9,y);await page.getByRole('tooltip').waitFor();await page.evaluate(()=>{window.__activityRequests=[];window.__holdKind='down'});await page.mouse.down();await page.waitForFunction(()=>window.__heldActivity.length===1);
await page.mouse.move(box.x+box.width*.6,y);await page.mouse.move(box.x+box.width*.1,y);
assert.equal(await page.evaluate(()=>window.__activityRequests.length),1);
await page.evaluate(()=>{window.__holdKind=null;window.__heldActivity.shift()()});await page.waitForFunction(()=>document.querySelector('.obs-activity-brush').style.width==='100%');
assert.equal(await page.evaluate(()=>window.__activityRequests.filter(request=>request.input.event.kind==='move').length),1);
await page.screenshot({path:evidence+'/coalesced-activity.png',animations:'disabled'});await page.keyboard.press('Escape');await page.mouse.up();await brush.waitFor({state:'hidden'});
const holdUp=async()=>{await page.mouse.move(box.x+box.width*.1,y);await page.mouse.down();await brush.waitFor();await page.evaluate(()=>{window.__holdKind='up'});await page.mouse.up();await page.waitForFunction(()=>window.__heldActivity.length===1)};
await holdUp();await page.keyboard.press('Escape');await page.evaluate(async()=>{window.__holdKind=null;window.__heldActivity.shift()();await new Promise(requestAnimationFrame)});await brush.waitFor({state:'hidden'});assert.equal(page.url(),original);
await holdUp();await page.evaluate(async()=>{dispatchEvent(new PageTransitionEvent('pagehide'));window.__holdKind=null;window.__heldActivity.shift()();await new Promise(requestAnimationFrame)});
assert.equal(page.url(),original);assert.equal(await page.locator('iframe[src*="/api/developer/traces/program"]').count(),0);assert.equal(await page.locator('.control-chart-tooltip').count(),0);
await context.close();}finally{await browser.close()}
`
	if output, err := exec.CommandContext(ctx, "node", "--input-type=module", "-e", script, server.URL, evidence).CombinedOutput(); err != nil {
		t.Fatalf("trace activity lifecycle: %v\n%s", err, output)
	}
}

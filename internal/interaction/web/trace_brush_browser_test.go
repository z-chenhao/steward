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
func TestTraceBrushActualDelayedRepliesCoalesceAndCancel(t *testing.T) {
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
	evidence := os.Getenv("STEWARD_TRACE_BRUSH_BROWSER_EVIDENCE")
	if evidence == "" && os.Getenv("STEWARD_APP_BROWSER_EVIDENCE") != "" {
		evidence = filepath.Join(os.Getenv("STEWARD_APP_BROWSER_EVIDENCE"), "trace-brush-lifecycle")
	}
	if evidence == "" {
		evidence = t.TempDir()
	}
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	script := `import assert from 'node:assert/strict';import {mkdir} from 'node:fs/promises';import {chromium} from '../../../guest-browser/node_modules/playwright-core/index.mjs';
const [url,evidence]=process.argv.slice(1);await mkdir(evidence,{recursive:true});const browser=await chromium.launch({headless:true,...(process.env.STEWARD_BROWSER_EXECUTABLE?{executablePath:process.env.STEWARD_BROWSER_EXECUTABLE}:{})});
try{const context=await browser.newContext({viewport:{width:1440,height:1000}});await context.request.post(url+'/session',{form:{token:'private-token-at-least-24-bytes'},maxRedirects:0});const page=await context.newPage();
await page.addInitScript(()=>{
 const Native=MessageChannel,set=Object.getOwnPropertyDescriptor(MessagePort.prototype,'onmessage').set;
 window.__heldBrush=[];window.__brushRequests=[];window.__brushReplies=[];window.__holdBrush=true;
 window.MessageChannel=function(){const channel=new Native();for(const port of [channel.port1,channel.port2]){
  const send=port.postMessage;port.postMessage=function(value,...rest){if(value?.type==='timeline_brush')window.__brushRequests.push(value);return send.call(this,value,...rest)};
  let handler;Object.defineProperty(port,'onmessage',{configurable:true,get:()=>handler,set(value){handler=value;set.call(port,event=>{if(event.data?.type==='timeline_brush'){window.__brushReplies.push(event.data);if(window.__holdBrush){window.__heldBrush.push(()=>handler.call(port,event));return}}handler?.call(port,event)})}});
 }return channel};window.MessageChannel.prototype=Native.prototype;
});
await page.goto(url+'/developer/observability/root-public?obs=generation:g');await page.waitForFunction(()=>document.querySelector('[data-split-view]').hasAttribute('data-time-window-start'));
const root=page.locator('[data-split-view]'),axis=page.locator('.obs-timeline-axis'),box=await axis.boundingBox(),y=box.y+box.height/2;
const original=await root.evaluate(node=>({start:node.dataset.timeWindowStart,end:node.dataset.timeWindowEnd}));
const start=async()=>{await page.mouse.move(box.x+box.width*.2,y);await page.mouse.down()};
await start();await page.mouse.move(box.x+box.width*.4,y);await page.waitForFunction(()=>window.__heldBrush.length===1);
await page.mouse.move(box.x+box.width*.8,y);assert.equal(await page.evaluate(()=>window.__brushRequests.length),1);
await page.evaluate(()=>{window.__holdBrush=false;window.__heldBrush.shift()()});
await page.waitForFunction(()=>{const replies=window.__brushReplies,nodes=document.querySelectorAll('.obs-timeline-brush');return replies.length===2&&nodes.length===2&&Array.from(nodes).every(node=>Number(node.getAttribute('width'))===replies[1].result.width)});
const widths=await page.evaluate(()=>window.__brushReplies.map(reply=>reply.result.width));assert.ok(widths[1]>widths[0]);await page.screenshot({path:evidence+'/coalesced-range.png',animations:'disabled'});
await page.keyboard.press('Escape');await page.mouse.up();assert.equal(await page.locator('.obs-timeline-brush').count(),0);
// The real completed reply is held before Escape, then delivered afterward.
await page.evaluate(()=>{window.__holdBrush=true});await start();await page.mouse.move(box.x+box.width*.6,y);await page.waitForFunction(()=>window.__heldBrush.length===1);
await page.keyboard.press('Escape');await page.mouse.up();await page.evaluate(async()=>{window.__holdBrush=false;window.__heldBrush.shift()();await new Promise(requestAnimationFrame)});
assert.equal(await page.locator('.obs-timeline-brush').count(),0);assert.deepEqual(await root.evaluate(node=>({start:node.dataset.timeWindowStart,end:node.dataset.timeWindowEnd})),original);
// A disposed view must not regain a program or paint when its reply is released.
await page.evaluate(()=>{window.__holdBrush=true});await start();await page.mouse.move(box.x+box.width*.7,y);await page.waitForFunction(()=>window.__heldBrush.length===1);
await page.evaluate(async()=>{dispatchEvent(new PageTransitionEvent('pagehide'));window.__holdBrush=false;window.__heldBrush.shift()();await new Promise(requestAnimationFrame)});await page.mouse.up();
assert.equal(await page.locator('iframe[src*="/api/developer/traces/program"]').count(),0);assert.equal(await page.locator('.obs-timeline-brush').count(),0);assert.equal(await page.locator('.control-chart-tooltip').count(),0);assert.deepEqual(await root.evaluate(node=>({start:node.dataset.timeWindowStart,end:node.dataset.timeWindowEnd})),original);
await context.close();}finally{await browser.close()}`
	if output, err := exec.CommandContext(ctx, "node", "--input-type=module", "-e", script, server.URL, evidence).CombinedOutput(); err != nil {
		t.Fatalf("trace brush lifecycle: %v\n%s", err, output)
	}
}

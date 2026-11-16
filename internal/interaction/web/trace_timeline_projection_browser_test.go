package web

import (
	"context"
	"net/http/httptest"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"
)

func TestTraceTimelineMalformedProjectionKeepsOriginalSSR(t *testing.T) {
	if os.Getenv("STEWARD_TEST_APP_BROWSER") != "1" {
		t.Skip("run by browser gate")
	}
	raw, err := os.ReadFile("../../plugin/traceinspector/view/trace-worker.js")
	if err != nil {
		t.Fatal(err)
	}
	// The candidate has a valid envelope but tries to supply an invalid lane. It
	// must not cause a partially committed window, erase original SVG, or fallback.
	source := string(raw) + `;const send=self.postMessage.bind(self);self.postMessage=value=>{if(value.type==='timeline')value.result.projection.rows[0].bar.x=-1;send(value)};`
	s := languageServer()
	s.Inspector = &inspectionReader{}
	s.Traces = &managedBrowserTraceReader{selectedTraceWorkbenchReader: &selectedTraceWorkbenchReader{revision: "sha256:" + strings.Repeat("a", 64)}, source: source}
	server := httptest.NewServer(s)
	defer server.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	script := `import assert from 'node:assert/strict';import {chromium} from '../../../guest-browser/node_modules/playwright-core/index.mjs';
const url=process.argv[1],browser=await chromium.launch({headless:true,...(process.env.STEWARD_BROWSER_EXECUTABLE?{executablePath:process.env.STEWARD_BROWSER_EXECUTABLE}:{})});
try{const context=await browser.newContext();await context.request.post(url+'/session',{form:{token:'private-token-at-least-24-bytes'},maxRedirects:0});const page=await context.newPage();
const program=page.waitForResponse(r=>r.url().includes('/api/developer/traces/program'));await page.goto(url+'/developer/observability/root-public?obs=generation:g');assert.equal((await program).status(),200);
await page.waitForFunction(()=>document.querySelectorAll('iframe[src*="/api/developer/traces/program"]').length===0);
assert.equal(await page.locator('[data-split-view]').getAttribute('data-time-window-start'),null);assert.equal(await page.locator('.obs-track').count(),2);assert.equal(await page.locator('.obs-timeline-grid').count(),0);assert.equal(await page.locator('.observation-node').count(),2);
for(const button of await page.locator('[data-timeline-action]').all())assert.equal(await button.isDisabled(),true);assert.ok((await page.locator('body').innerText()).includes('Accepted <script>unsafe()</script>'));assert.equal(await page.locator('a.tree-heading').count(),2);
await context.close();}finally{await browser.close()}`
	if output, err := exec.CommandContext(ctx, "node", "--input-type=module", "-e", script, server.URL).CombinedOutput(); err != nil {
		t.Fatalf("invalid timeline projection: %v\n%s", err, output)
	}
}

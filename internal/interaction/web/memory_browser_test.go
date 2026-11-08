package web

import (
	"context"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

// The existing browser gate supplies the browser; the server and shared sheet
// are real, while only this read-only domain projection contains fixture data.
func TestMemoryMaintenanceBrowser(t *testing.T) {
	if os.Getenv("STEWARD_TEST_APP_BROWSER") != "1" {
		t.Skip("run by make test-browser")
	}
	s := languageServer()
	s.Memory = maintenanceMemoryReader{}
	server := httptest.NewServer(s)
	defer server.Close()
	evidence := os.Getenv("STEWARD_MEMORY_BROWSER_EVIDENCE")
	if evidence == "" && os.Getenv("STEWARD_APP_BROWSER_EVIDENCE") != "" {
		evidence = filepath.Join(os.Getenv("STEWARD_APP_BROWSER_EVIDENCE"), "memory-maintenance")
	}
	if evidence == "" {
		evidence = t.TempDir()
	}
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	script := `import {chromium} from '../../../guest-browser/node_modules/playwright-core/index.mjs';
import {mkdir} from 'node:fs/promises';
import {maintenanceInspection} from '../../../tests/system/channels_memory.mjs';
const [url,evidence]=process.argv.slice(1);await mkdir(evidence,{recursive:true});
const browser=await chromium.launch({headless:true,...(process.env.STEWARD_BROWSER_EXECUTABLE?{executablePath:process.env.STEWARD_BROWSER_EXECUTABLE}:{})});
try{const context=await browser.newContext({viewport:{width:1440,height:1000}});await context.request.post(url+'/session',{form:{token:'private-token-at-least-24-bytes',next:'/developer/memory'}});const page=await context.newPage();await maintenanceInspection({page,url,evidence,priced:true});const native=await browser.newContext({javaScriptEnabled:false});await native.addCookies(await context.cookies());const p=await native.newPage();await p.goto(url+'/developer/memory?view=maintenance&ref=memory:maintenance-priced');if(!await p.locator('[data-memory-id]').count()||!(await p.locator('[data-memory-id]').innerText()).includes('0.125000'))throw Error('Native maintenance detail missing');await native.close();await context.close()}finally{await browser.close()}`
	if out, e := exec.CommandContext(ctx, "node", "--input-type=module", "-e", script, server.URL, evidence).CombinedOutput(); e != nil {
		t.Fatalf("memory browser: %v\n%s", e, out)
	}
}

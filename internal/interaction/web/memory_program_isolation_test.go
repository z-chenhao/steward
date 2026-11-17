package web

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/z-chenhao/assistant/internal/memory"
)

type memoryIsolationReader struct {
	memoryStyleOnlyReader
	programs map[string]string
}

func (r *memoryIsolationReader) MemoryInspectorProgram(_ context.Context, reference string) (memory.InspectionProgram, error) {
	source, ok := r.programs[reference]
	if !ok {
		return memory.InspectionProgram{}, memory.ErrInspectionUnavailable
	}
	return memory.InspectionProgram{ReaderRevision: reference, Source: source}, nil
}

// Real protected Memory route and TS transport with injected hostile programs.
// Package sealing/lifecycle is proved by the separate actual App regressions.
func TestMemoryProgramActualOpaqueWorkerDeniesEffectsAndTerminatesFailures(t *testing.T) {
	if os.Getenv("STEWARD_TEST_APP_BROWSER") != "1" {
		t.Skip("run by make test-browser")
	}
	var network atomic.Int64
	peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		network.Add(1)
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Write([]byte("destination"))
	}))
	defer peer.Close()
	raw, err := os.ReadFile("../../plugin/memoryinspector/view/memory-worker.js")
	if err != nil {
		t.Fatal(err)
	}
	target, _ := json.Marshal(peer.URL)
	attack := `;const previous=self.onmessage;self.onmessage=e=>{if(e.data.type==='init'){if(typeof document!=='undefined'||typeof parent!=='undefined')throw Error('DOM authority');try{fetch(` + string(target) + `).catch(()=>{})}catch{}try{importScripts(` + string(target) + `)}catch{}try{indexedDB.open('private');throw Error('storage authority')}catch(error){if(error.name!=='SecurityError')throw error}globalThis.MEMORY_PROGRAM_MARKER=true}previous(e)};`
	references := []string{}
	for _, digit := range []string{"1", "2", "3", "4"} {
		references = append(references, "sha256:"+strings.Repeat(digit, 64))
	}
	reader := &memoryIsolationReader{programs: map[string]string{references[0]: string(raw) + attack, references[1]: `self.onmessage=e=>postMessage({seq:999999,type:'ready'})`, references[2]: `self.onmessage=()=>{while(true){}}`, references[3]: `self.onmessage=e=>postMessage({seq:e.data.seq,type:'ready',padding:'x'.repeat(80000)})`}}
	command := exec.Command("node", "--input-type=module", "-e", `import {build} from 'esbuild';const r=await build({stdin:{contents:"export {connectMemoryBrowser} from './src/memory-browser'",resolveDir:process.cwd(),loader:'ts'},bundle:true,write:false,format:'iife',globalName:'MemoryBrowserFixture'});process.stdout.write(r.outputFiles[0].text)`)
	command.Dir = "../../../web"
	bundle, err := command.CombinedOutput()
	if err != nil {
		t.Fatal(err, string(bundle))
	}
	s := languageServer()
	s.Memory = reader
	host := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/fixture":
			w.Header().Set("Content-Security-Policy", "default-src 'none'; script-src 'self'; frame-src 'self'; connect-src 'self'")
			w.Write([]byte(`<!doctype html><title>Memory transport</title><script defer src="/fixture.js"></script><body></body>`))
		case "/fixture.js":
			w.Header().Set("Content-Type", "text/javascript")
			w.Write(bundle)
			w.Write([]byte(`;window.fixture=MemoryBrowserFixture.connectMemoryBrowser(new URL(location.href).searchParams.get('ref'),{rail_present:true,rail_open:true});window.fixture.catch(()=>{});`))
		default:
			s.ServeHTTP(w, r)
		}
	}))
	defer host.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	script := `import assert from 'node:assert/strict';import {chromium} from '../../../guest-browser/node_modules/playwright-core/index.mjs';
const [url,...refs]=process.argv.slice(1);const browser=await chromium.launch({headless:true,...(process.env.STEWARD_BROWSER_EXECUTABLE?{executablePath:process.env.STEWARD_BROWSER_EXECUTABLE}:{})});
try{const context=await browser.newContext();assert.equal((await context.request.get(url+'/api/developer/memory/program?inspector_revision='+encodeURIComponent(refs[0]))).status(),401);assert.equal((await context.request.post(url+'/session',{form:{token:'private-token-at-least-24-bytes'},maxRedirects:0})).status(),303);
const page=await context.newPage();await page.goto(url+'/fixture?ref='+encodeURIComponent(refs[0]));const fit=await page.evaluate(async()=>{const p=await window.fixture;return p.call({type:'fit',rail_present:true,rail_open:true,compact:true,wide_open:true})});assert.equal(fit.rail_open,false);
const frame=page.frames().find(f=>f.url().includes('/api/developer/memory/program'));assert.ok(frame);const isolation=await frame.evaluate(()=>({origin:self.origin,marker:globalThis.MEMORY_PROGRAM_MARKER,scripts:document.scripts.length}));assert.equal(isolation.origin,'null');assert.equal(isolation.marker,undefined);assert.equal(isolation.scripts,1);await page.evaluate(async()=>{(await window.fixture).close()});assert.equal(await page.locator('iframe').count(),0);
for(const ref of refs.slice(1)){await page.goto(url+'/fixture?ref='+encodeURIComponent(ref));assert.equal(await page.evaluate(()=>window.fixture.then(()=>false,()=>true)),true);assert.equal(await page.locator('iframe').count(),0)}
await page.goto(url+'/api/developer/memory/program?inspector_revision='+encodeURIComponent(refs[0]));assert.equal(await page.locator('body').innerText(),'Memory isolated presentation program');await context.close();console.log('Memory opaque Worker: effects denied; invalid sequence, loop and oversized reply terminated');
}finally{await browser.close()}`
	args := append([]string{"--input-type=module", "-e", script, host.URL}, references...)
	out, err := exec.CommandContext(ctx, "node", args...).CombinedOutput()
	if err != nil {
		t.Fatal(err, string(out))
	}
	if network.Load() != 0 {
		t.Fatal("Worker reached network", network.Load())
	}
	if reader.factReads != 0 {
		t.Fatal("program transport loaded Memory facts", reader.factReads)
	}
}

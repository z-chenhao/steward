package web

import (
	"context"
	"errors"
	"github.com/z-chenhao/assistant/internal/observability"
	"net/http/httptest"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"
)

type traceVersionBrowserReader struct {
	traceReader
	source, style string
}

func (r *traceVersionBrowserReader) ObservationBody(_ context.Context, q observability.BodyQuery) (observability.Body, error) {
	return browserWireBody(q)
}

func traceVersionRef(letter string) string { return "sha256:" + strings.Repeat(letter, 64) }
func validTraceVersionRef(ref string) bool {
	return ref == traceVersionRef("a") || ref == traceVersionRef("b") || ref == traceVersionRef("c")
}
func (r *traceVersionBrowserReader) TraceWorkbenchSelection(_ context.Context, ref string) (observability.TraceWorkbench, error) {
	if ref == "" {
		ref = traceVersionRef("a")
	}
	if !validTraceVersionRef(ref) {
		return observability.TraceWorkbench{}, errors.New("unknown fixture reader")
	}
	source, err := os.ReadFile("../../plugin/traceinspector/view/trace.html")
	if err != nil {
		return observability.TraceWorkbench{}, err
	}
	view := string(source)
	if ref == traceVersionRef("b") {
		view = strings.Replace(view, `{{traceTr "Tracing"}}`, `{{traceTr "Versioned trace directory"}}`, 1)
		view = strings.Replace(view, `{{else if eq . "root"}}{{traceTr "Root Worker"}}`, `{{else if eq . "root"}}{{traceTr "Worker v2"}}`, 1)
		view = strings.ReplaceAll(view, `fraction .Count $activityMax 100`, `fraction .Count $activityMax 90`)
		view = strings.ReplaceAll(view, "trace-role-label", "trace-private-role-v2")
		view = strings.Replace(view, `{{else if eq . "metadata"}}{{traceTr "Details"}}`, `{{else if eq . "metadata"}}{{traceTr "Diagnostics v2"}}`, 1)
	}
	return observability.TraceWorkbench{Copy: traceFixtureCopy(), ReaderRevision: ref, Managed: ref != traceVersionRef("c"), ViewSource: view}, nil
}
func (r *traceVersionBrowserReader) TraceWorkbenchProgram(_ context.Context, ref string) (observability.TraceProgram, error) {
	if !validTraceVersionRef(ref) || ref == traceVersionRef("c") {
		return observability.TraceProgram{}, errors.New("legacy fixture reader")
	}
	style := r.style
	source := r.source
	if ref == traceVersionRef("b") {
		source = strings.ReplaceAll(source, `sections:['system','messages','tools','parameters']`, `sections:['parameters','system','messages','tools']`)
		source = strings.ReplaceAll(source, `expanded:{system:false,messages:'last'`, `expanded:{system:true,messages:'all'`)
		source = strings.ReplaceAll(source, "keep_filter:input.same_trace", "keep_filter:false")
		style = strings.ReplaceAll(style, "height:88px", "height:96px")
		style = strings.ReplaceAll(style, "--timeline-width:44%", "--timeline-width:48%")
		style = strings.ReplaceAll(style, ".obs-tree-guides>span{width:12px", ".obs-tree-guides>span{width:16px")
		style = strings.ReplaceAll(style, ".obs-wire-reader{display:grid;gap:20px}", ".obs-wire-reader{display:grid;gap:24px}")
	}
	return observability.TraceProgram{ReaderRevision: ref, Source: source, Stylesheet: style}, nil
}
func (r *traceVersionBrowserReader) Traces(ctx context.Context, q observability.TraceQuery) (observability.Traces, error) {
	if !validTraceVersionRef(q.ReaderRevision) {
		return observability.Traces{}, errors.New("lost frozen reader")
	}
	out, err := r.traceReader.Traces(ctx, q)
	return browserActivityFacts(out), err
}
func TestTracePageBrowserKeepsOneFrozenAssetVersion(t *testing.T) {
	if os.Getenv("STEWARD_TEST_APP_BROWSER") != "1" {
		t.Skip("run by browser gate")
	}
	source, err := os.ReadFile("../../plugin/traceinspector/view/trace-worker.js")
	if err != nil {
		t.Fatal(err)
	}
	style, err := os.ReadFile("../../plugin/traceinspector/view/trace-activity.css")
	if err != nil {
		t.Fatal(err)
	}
	s := languageServer()
	s.Inspector = &inspectionReader{}
	s.Traces = &traceVersionBrowserReader{source: string(source), style: string(style)}
	server := httptest.NewServer(s)
	defer server.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	script := `import assert from 'node:assert/strict';import {chromium} from '../../../guest-browser/node_modules/playwright-core/index.mjs';
const [base]=process.argv.slice(1),ref=letter=>'sha256:'+letter.repeat(64),list=letter=>base+'/developer/observability?reader_revision='+encodeURIComponent(ref(letter));
const browser=await chromium.launch({headless:true,...(process.env.STEWARD_BROWSER_EXECUTABLE?{executablePath:process.env.STEWARD_BROWSER_EXECUTABLE}:{})});
try{const context=await browser.newContext({viewport:{width:1440,height:1000}});await context.addInitScript(()=>{window.__documentEpoch=crypto.randomUUID()});await context.request.post(base+'/session',{form:{token:'private-token-at-least-24-bytes'},maxRedirects:0});const page=await context.newPage();
const ready=async height=>{assert.equal(await page.getByRole('heading',{level:1}).textContent(),height===96?'Versioned trace directory':'Tracing');assert.equal(await page.locator('tbody tr[data-role="root"] [data-kind="agent"]').textContent(),height===96?'Worker v2':'Root Worker');assert.equal(await page.locator('.obs-activity-bars rect').first().getAttribute('height'),height===96?'90':'100');assert.equal(await page.locator('.obs-activity-bars rect').first().getAttribute('y'),height===96?'10':'0');await page.locator('[data-trace-column]:not([disabled])').first().waitFor({state:'attached'});await page.waitForFunction(()=>document.querySelector('[data-activity-max]')?.textContent==='1');assert.equal(await page.locator('.obs-activity-chart').evaluate(node=>node.getBoundingClientRect().height),height)};
await page.goto(list('a'));await ready(88);const original=await page.evaluate(()=>window.__documentEpoch);
// Existing link represents a published reference to another accepted reader.
await page.locator('a.trace-open').evaluate((node,value)=>{const url=new URL(node.href);url.searchParams.set('reader_revision',value);node.href=url.href},ref('b'));await page.locator('a.trace-open').click();await page.waitForURL(url=>url.searchParams.get('reader_revision')===ref('b'));
await page.waitForFunction(value=>document.querySelector('#observability-page')?.dataset.traceReaderRevision===value,ref('b'));assert.notEqual(await page.evaluate(()=>window.__documentEpoch),original);assert.equal(await page.getByRole('dialog').count(),0);assert.equal(await page.locator('link[href*="/traces/style?"]').evaluate(node=>new URL(node.href).searchParams.get('reader_revision')),ref('b'));
await page.goto(list('b'));await ready(96);const same=await page.evaluate(()=>window.__documentEpoch);await page.locator('a.trace-open').click();const peek=page.getByRole('dialog',{name:'Trace details',exact:true});await peek.waitFor();assert.equal(await peek.locator('.obs-tree-guides>span').first().evaluate(node=>node.getBoundingClientRect().width),16);assert.equal(await peek.locator('.obs-tree-pane').evaluate(node=>getComputedStyle(node).getPropertyValue('--timeline-width').trim()),'48%');await peek.locator('[data-json-mode]:not([disabled])').first().waitFor({state:'attached'});assert.equal(await page.evaluate(()=>window.__documentEpoch),same);assert.equal(await peek.locator('.obs-detail-tabs a').nth(1).textContent(),'Diagnostics v2');await peek.getByRole('searchbox',{name:'Search observations'}).fill('Model');await peek.locator('[data-observation-id="generation:g"] a.tree-heading').click();await page.waitForURL(url=>url.searchParams.get('obs')==='generation:g');await page.waitForFunction(()=>document.querySelector('.control-sheet .obs-tree-search input')?.value==='');assert.equal(await page.evaluate(()=>window.__documentEpoch),same);
await peek.locator('a.tree-heading').first().evaluate((node,value)=>{const url=new URL(node.href);url.searchParams.set('reader_revision',value);node.href=url.href},ref('a'));await peek.locator('a.tree-heading').first().click();await page.waitForURL(url=>url.searchParams.get('reader_revision')===ref('a'));await page.waitForFunction(value=>document.querySelector('#observability-page')?.dataset.traceReaderRevision===value,ref('a'));assert.notEqual(await page.evaluate(()=>window.__documentEpoch),same);
// An unversioned published link resolves through a complete current-page read.
const beforeUnversioned=await page.evaluate(()=>window.__documentEpoch);await page.locator('[data-observation-id="generation:g"] a.tree-heading').evaluate(node=>{const url=new URL(node.href);url.searchParams.delete('reader_revision');node.href=url.href});await page.locator('[data-observation-id="generation:g"] a.tree-heading').click();await page.waitForFunction(previous=>window.__documentEpoch!==previous,beforeUnversioned);assert.equal(await page.locator('#observability-page').getAttribute('data-trace-reader-revision'),ref('a'));
await page.locator('[data-json-mode]:not([disabled])').first().waitFor({state:'attached'});
// A same-URL response carrying another reader is rejected before DOM adoption.
const beforeMismatch=await page.evaluate(()=>{sessionStorage.removeItem('mixed-reader');new MutationObserver(()=>{if([...document.querySelectorAll('[data-trace-program-revision]')].some(node=>node.dataset.traceProgramRevision==='sha256:'+'b'.repeat(64)))sessionStorage.setItem('mixed-reader','yes')}).observe(document,{subtree:true,childList:true});return window.__documentEpoch});
let corrupted=false;await page.route('**/developer/observability/root-public?**',async route=>{if(!corrupted&&route.request().resourceType()==='fetch'){corrupted=true;const response=await route.fetch();await route.fulfill({response,body:(await response.text()).replaceAll(ref('a'),ref('b'))})}else await route.continue()});
await page.locator('[data-observation-id="generation:g"] a.tree-heading').click();await page.waitForFunction(previous=>window.__documentEpoch!==previous,beforeMismatch);assert.equal(corrupted,true);assert.equal(await page.evaluate(()=>sessionStorage.getItem('mixed-reader')),null);await page.unrouteAll();
// Explicit historical/direct reader has a document identity despite no Worker.
await page.goto(list('c'));await ready(88);assert.equal(await page.locator('iframe[src*="/traces/program"]').count(),0);const legacy=await page.evaluate(()=>window.__documentEpoch);await page.locator('a.trace-open').click();await page.getByRole('dialog',{name:'Trace details',exact:true}).waitFor();assert.equal(await page.evaluate(()=>window.__documentEpoch),legacy);assert.equal(await page.locator('.obs-tree-pane').first().evaluate(node=>getComputedStyle(node).getPropertyValue('--timeline-width').trim()),'44%');assert.equal(await page.locator('#observability-page').getAttribute('data-trace-reader-revision'),ref('c'));await page.goto(base+'/developer/observability/root-public?obs=generation%3Ag&tab=wire&reader_revision='+encodeURIComponent(ref('b')));await page.locator('.obs-wire-reader').waitFor();assert.equal(await page.locator('.obs-wire-reader').evaluate(node=>getComputedStyle(node).gap),'24px');assert.equal(await page.locator('.obs-wire-attempt>.obs-message>summary').first().textContent(),'Parameters');assert.equal(await page.locator('.obs-wire-attempt>.obs-message').nth(1).evaluate(node=>node.open),true);await context.close();
}finally{await browser.close()}`
	if out, err := exec.CommandContext(ctx, "node", "--input-type=module", "-e", script, server.URL).CombinedOutput(); err != nil {
		t.Fatalf("frozen page browser: %v\n%s", err, out)
	}
}

func (r *traceVersionBrowserReader) ProjectedObservations(ctx context.Context, q observability.ObservationQuery) (observability.ObservationTree, *observability.TracePresentation, error) {
	tree, err := r.Observations(ctx, q)
	if err != nil {
		return tree, nil, err
	}
	if q.ReaderRevision == traceVersionRef("c") {
		return tree, nil, nil
	}
	return tree, owningTraceFixtureProjection(tree, q.ReaderRevision), nil
}

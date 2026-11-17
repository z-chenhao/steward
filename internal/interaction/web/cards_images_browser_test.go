package web

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/z-chenhao/assistant/internal/agent"
	"github.com/z-chenhao/assistant/internal/delivery/ui"
)

type cardsImagesBrowserCore struct {
	*cardsControllerCore
	image      []byte
	imageReads int
	denied     bool
}

func (c *cardsImagesBrowserCore) OpenWebCardsImage(_ context.Context, ref string, item int) (io.ReadCloser, string, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if ref != "ui:original#1" || item != 0 {
		c.forbidden++
		return nil, "", agent.ErrNotFound
	}
	c.imageReads++
	if c.denied {
		return nil, "", agent.ErrNotFound
	}
	return io.NopCloser(bytes.NewReader(c.image)), "image/png", nil
}

// Real React, HTTP and Blob delivery. Domain image authorization is a fixture;
// the owning Store's original-item/Forget regression is maintained separately.
func TestUICardsImagesBrowserOriginalBlobAndBoundRequests(t *testing.T) {
	if os.Getenv("STEWARD_TEST_APP_BROWSER") != "1" {
		t.Skip("run by make test-browser")
	}
	read := func(path string) []byte {
		t.Helper()
		raw, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		return raw
	}
	var original struct {
		HTML  string          `json:"initial_html"`
		Props json.RawMessage `json:"props"`
	}
	if err := json.Unmarshal(read("../../plugin/intelligentui/testdata/original-cards.json"), &original); err != nil {
		t.Fatal(err)
	}
	var props map[string]json.RawMessage
	if err := json.Unmarshal(original.Props, &props); err != nil {
		t.Fatal(err)
	}
	var items []map[string]json.RawMessage
	if err := json.Unmarshal(props["items"], &items); err != nil {
		t.Fatal(err)
	}
	items[0]["image_ref"] = json.RawMessage(`"artifact:declared_image"`)
	props["items"], _ = json.Marshal(items)
	propsJSON, _ := json.Marshal(props)
	anchor := `<button type="button" class="native-card-title" aria-pressed="false">Alpha`
	if strings.Count(original.HTML, anchor) != 1 {
		t.Fatal("original image fixture anchor changed")
	}
	markup := strings.Replace(original.HTML, anchor, `<img loading="lazy" data-card-image="0" alt="">`+anchor, 1)
	picture, err := base64.StdEncoding.DecodeString("iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mP8/x8AAwMCAO+jRZkAAAAASUVORK5CYII=")
	if err != nil {
		t.Fatal(err)
	}
	core := &cardsImagesBrowserCore{cardsControllerCore: &cardsControllerCore{cardsCore: cardsCore{view: ui.CardsView{HTML: markup, CSS: string(read("../../plugin/intelligentui/view/cards.css")), Worker: string(read("../../plugin/intelligentui/view/cards-worker.js")), Styles: read("assets/widget.css"), CompactMaxWidth: 760, Contexts: []ui.CardsContext{{Title: "Card choices", VisibleItem: "Alpha"}, {Title: "Card choices", VisibleItem: "Beta"}}}}, states: map[string]ui.ViewState{}}, image: picture}
	webRoot, err := filepath.Abs("../../../web")
	if err != nil {
		t.Fatal(err)
	}
	build := `import{build}from'../../../web/node_modules/esbuild/lib/main.js';const result=await build({stdin:{contents:"import{createElement}from'react';import{createRoot}from'react-dom/client';import{FrozenCardsView}from'./src/intelligent-ui/cards-view';import{InteractionGroup}from'./src/intelligent-ui/group';createRoot(document.getElementById('root')).render(createElement(InteractionGroup,{reference:'ui:original#0',actions:[]},createElement(FrozenCardsView,{reference:'ui:original#1',props:JSON.parse(document.getElementById('props').textContent),fallback:'Image fallback'})));",resolveDir:process.argv[1],sourcefile:'cards-images.tsx',loader:'tsx'},bundle:true,jsx:'automatic',write:false,format:'iife',define:{'process.env.NODE_ENV':'\"production\"'}});process.stdout.write(result.outputFiles[0].contents);`
	parent, err := exec.Command("node", "--input-type=module", "-e", build, webRoot).Output()
	if err != nil {
		t.Fatal(err)
	}
	s := &Server{Core: core, Config: Config{TokenRef: "credential:test"}, Secrets: fakeSecrets{}}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/fixture.js":
			w.Header().Set("Content-Type", "text/javascript")
			_, _ = w.Write(parent)
			return
		case "/fixture":
			w.Header().Set("Content-Type", "text/html")
			_, _ = w.Write([]byte(`<!doctype html><link rel="stylesheet" href="/style.css"><style>#root{max-width:672px;margin:24px}</style><div id="root"></div><script type="application/json" id="props">` + string(propsJSON) + `</script><script src="/fixture.js"></script>`))
			return
		case "/deny-fixture":
			core.mu.Lock()
			core.denied = true
			core.mu.Unlock()
			w.WriteHeader(204)
			return
		}
		s.ServeHTTP(w, r)
	}))
	defer server.Close()
	evidence := t.TempDir()
	if directory := os.Getenv("STEWARD_APP_BROWSER_EVIDENCE"); directory != "" {
		evidence = filepath.Join(directory, "cards-images")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Second)
	defer cancel()
	script := `import{waitForUIState}from'../../../guest-browser/ui-state.mjs';import{chromium}from'../../../guest-browser/node_modules/playwright-core/index.mjs';import{mkdir,writeFile}from'node:fs/promises';const[url,out]=process.argv.slice(1);await mkdir(out,{recursive:true});const browser=await chromium.launch({headless:true,...(process.env.STEWARD_BROWSER_EXECUTABLE?{executablePath:process.env.STEWARD_BROWSER_EXECUTABLE}:{})});try{const context=await browser.newContext();await context.request.post(url+'/session',{form:{token:'private-token-at-least-24-bytes'},maxRedirects:0});const page=await context.newPage(),resources=[];page.on('response',response=>{if(response.url().includes('/ui/cards-image?'))resources.push({url:response.url(),status:response.status()})});await page.setViewportSize({width:1400,height:900});await page.goto(url+'/fixture');const frame=page.frameLocator('iframe[title="Cards view"]'),image=frame.locator('img');await image.waitFor();await image.evaluate(img=>new Promise((resolve,reject)=>{if(img.complete&&img.naturalWidth)resolve();else{img.addEventListener('load',resolve,{once:true});img.addEventListener('error',()=>reject(Error('image decoding failed')),{once:true})}}));if(!await image.evaluate(img=>img.naturalWidth===1&&img.naturalHeight===1&&img.loading==='eager'&&img.src.startsWith('blob:')&&self.origin==='null'))throw Error('authorized Blob did not render in opaque frame');if((await frame.locator('body').innerHTML()).includes('artifact:'))throw Error('raw Artifact identity entered iframe');await frame.locator('body').evaluate(()=>{for(const value of[{type:'image-request',item:0,artifact_ref:'artifact:private'},{type:'image-request',item:0,ref:'ui:foreign#5'},{type:'image-request',item:1},{type:'image-request',item:999},{type:'image-request',item:'0'}])parent.postMessage({channel:'steward-cards-view',...value},'*')});await frame.locator('body').evaluate(()=>{parent.postMessage({channel:'steward-cards-view',type:'view-state',state:{index:0},model_context:{title:'PRIVATE_IMAGE_PIXELS',visible_item:'PRIVATE_IMAGE_PIXELS',pixels:'PRIVATE_IMAGE_PIXELS'}},'*');parent.postMessage({channel:'steward-cards-view',type:'view-state',state:{index:0,pixels:'PRIVATE_IMAGE_PIXELS'},model_context:{visible_item:'PRIVATE_IMAGE_PIXELS'}},'*')});await waitForUIState(page,'ui:original#1',s=>s.state.index===0&&s.model_context.visible_item==='Alpha');const saved=await page.evaluate(async()=>(await(await fetch('/api/ui/state?ref=ui:original%231')).json()));if(JSON.stringify(saved).includes('PRIVATE_IMAGE_PIXELS')||saved.model_context.title!=='Card choices'||Object.keys(saved.state).join()!=='index')throw Error('image-bearing frame authored model context or raw state '+JSON.stringify(saved));const blocked=await frame.locator('body').evaluate(async()=>{try{await fetch('/ui/cards-image?ref=ui:original%231&item=0');return false}catch{return true}});if(!blocked)throw Error('candidate acquired authenticated image transport');for(const width of[1400,390]){await page.setViewportSize({width,height:900});for(const theme of['light','dark']){await page.evaluate(theme=>document.documentElement.classList.toggle('dark',theme==='dark'),theme);await frame.locator('body').evaluate(()=>new Promise(resolve=>requestAnimationFrame(()=>requestAnimationFrame(resolve))));await page.screenshot({path:out+'/images-'+theme+'-'+width+'.png'})}}if(resources.length!==1||resources[0].status!==200)throw Error('image requests were replayed or unavailable '+JSON.stringify(resources));const target=new URL(resources[0].url);if(target.searchParams.get('ref')!=='ui:original#1'||target.searchParams.get('item')!=='0'||[...target.searchParams].length!==2)throw Error('candidate chose image source');await context.request.get(url+'/deny-fixture');await page.reload();await frame.locator('img[data-image-unavailable=true]').waitFor();if(await frame.locator('img').evaluate(img=>img.naturalWidth!==0||img.hasAttribute('src')))throw Error('denied image was revived');await writeFile(out+'/report.json',JSON.stringify({scope:'actual Cards React/HTTP/Blob path; domain image grant is fixture, separate Store regression proves original item and Forget',opaqueBlobDecoded:true,originalComponentRequest:true,rawArtifactIdentityAbsent:true,frozenGoContextOnly:true,pixelContextAndExtraStateRejected:true,repeatedAndForgedRequestsIgnored:true,networkDenied:true,newReadDeniedAfterRevocation:true,resources},null,2));await context.close()}finally{await browser.close()}`
	if output, err := exec.CommandContext(ctx, "node", "--input-type=module", "-e", script, server.URL, evidence).CombinedOutput(); err != nil {
		t.Fatalf("Cards images: %v\n%s", err, output)
	}
	core.mu.Lock()
	defer core.mu.Unlock()
	if core.forbidden != 0 || core.imageReads != 2 {
		t.Fatal("image grant widened or repeated", core.forbidden, core.imageReads)
	}
}

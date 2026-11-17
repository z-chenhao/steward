package web

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	_ "embed"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/z-chenhao/assistant/internal/delivery/ui"
	htmlnode "golang.org/x/net/html"
)

type cardsViewReader interface {
	WebCardsView(context.Context, string) (ui.CardsView, error)
}

//go:embed view/cards-gestures.js
var cardsGesturesDriver string

//go:embed view/cards-driver.js
var cardsDOMDriver string

//go:embed view/cards-text.js
var cardsTextRestore string

// This archived owning policy only interprets the reviewed historical Runtime.
//
//go:embed legacy-ui/cards-v1/worker.js
var historicalCardsWorker string

func (s *Server) cardsView(w http.ResponseWriter, r *http.Request) {
	reader, ok := s.Core.(cardsViewReader)
	if !ok {
		fail(w, 503)
		return
	}
	view, err := reader.WebCardsView(r.Context(), r.URL.Query().Get("ref"))
	if err != nil {
		fail(w, 404)
		return
	}
	if r.URL.Query().Get("format") == "context" {
		if len(view.Contexts) < 1 || len(view.Contexts) > 16 {
			fail(w, 503)
			return
		}
		jsonResult(w, view.Contexts)
		return
	}
	renderCardsView(w, r, view)
}

func renderCardsView(w http.ResponseWriter, r *http.Request, view ui.CardsView) {
	program := view.Worker
	if view.Runtime != "" {
		if program != "" || fmt.Sprintf("%x", sha256.Sum256([]byte(view.Runtime))) != "4b7947e8673b2b87a280d8daffb6bb7e002158b106e6407da250ae9500a26c59" {
			fail(w, 503)
			return
		}
		program = historicalCardsWorker
	}
	encodedProgram, _ := json.Marshal(program)
	if view.HTML == "" || view.CSS == "" || program == "" || len(view.Styles) == 0 || view.CompactMaxWidth < 1 || view.CompactMaxWidth > 4096 {
		fail(w, 503)
		return
	}
	markup := cardsMarkup(view.HTML)
	nonceBytes := make([]byte, 24)
	if _, err := rand.Read(nonceBytes); err != nil {
		fail(w, 500)
		return
	}
	nonce := base64.RawStdEncoding.EncodeToString(nonceBytes)
	w.Header().Set("Content-Security-Policy", "sandbox allow-scripts; default-src 'none'; script-src 'nonce-"+nonce+"'; worker-src blob:; style-src 'unsafe-inline'; font-src data:; img-src data: blob:; connect-src 'none'; frame-src 'none'; form-action 'none'; base-uri 'none'; frame-ancestors 'self'")
	w.Header().Set("Permissions-Policy", "camera=(), microphone=(), geolocation=(), clipboard-read=(), clipboard-write=()")
	w.Header().Set("Origin-Agent-Cluster", "?1")
	w.Header().Set("Referrer-Policy", "no-referrer")
	if r.Header.Get("Sec-Fetch-Dest") == "document" {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		root, _ := htmlnode.Parse(strings.NewReader(markup))
		var parts []string
		var text func(*htmlnode.Node)
		text = func(n *htmlnode.Node) {
			if n.Type == htmlnode.TextNode && strings.TrimSpace(n.Data) != "" {
				parts = append(parts, n.Data)
			}
			for c := n.FirstChild; c != nil; c = c.NextSibling {
				text(c)
			}
		}
		if root != nil {
			text(root)
		}
		_, _ = fmt.Fprint(w, strings.Join(parts, "\n"))
		return
	}
	theme := "light"
	if r.URL.Query().Get("theme") == "dark" {
		theme = "dark"
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = fmt.Fprint(w, `<!doctype html><html class="ogui-theme" data-widget-theme="`+theme+`" style="color-scheme:`+theme+`"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1"><title>Cards view</title><script nonce="`+nonce+`">
(()=>{addEventListener('message',event=>{const value=event.data;if(event.source!==parent||value?.channel!=='steward-cards-view'||value.type!=='theme')return;if(value.theme==='light'||value.theme==='dark'){document.documentElement.style.colorScheme=value.theme;document.documentElement.dataset.widgetTheme=value.theme}if(Number.isFinite(value.viewportWidth)&&value.viewportWidth>0&&value.viewportWidth<=100000)document.documentElement.toggleAttribute('data-host-compact',value.viewportWidth<=`+strconv.Itoa(view.CompactMaxWidth)+`);for(const key of ['--card','--border','--foreground','--muted-foreground','--hover','--font-ui','--font-sans','--control-border']){const token=value.tokens?.[key];if(typeof token==='string'&&token.length<=256)document.documentElement.style.setProperty(key,token)}last=0;if(document.body)report()});let last=0;const report=()=>{const height=Math.ceil(document.body.getBoundingClientRect().height);if(height!==last){last=height;parent.postMessage({channel:'steward-cards-view',type:'height',height},'*')}};addEventListener('DOMContentLoaded',()=>{new ResizeObserver(report).observe(document.body);document.fonts.ready.then(report);report()})})();
</script><style>`+planStyleText(string(view.Styles))+`
:root{--card:var(--color-background-tertiary);--border:var(--color-border-tertiary);--foreground:var(--color-text-primary);--muted-foreground:var(--color-text-secondary);--hover:var(--color-background-secondary);--font-ui:var(--font-sans)}html,body{margin:0;min-height:0;height:auto}.ogui-theme body{font:14px/1.6 var(--font-ui);background:transparent}*{box-sizing:border-box}
</style><style>`+planStyleText(view.CSS)+`</style></head><body>`+markup+`<script nonce="`+nonce+`">`+cardsGesturesDriver+`</script><script nonce="`+nonce+`">(`+cardsDOMDriver+`)(`+string(encodedProgram)+`,(`+cardsTextRestore+`));</script></body></html>`)
}

package web

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"net/http"
	"regexp"
	"strconv"
	"strings"

	"github.com/z-chenhao/assistant/internal/delivery/ui"
	htmlnode "golang.org/x/net/html"
)

type planViewReader interface {
	WebPlanView(context.Context, string) (ui.PlanView, error)
}

var planStyleEnd = regexp.MustCompile(`(?i)</style`)

func planStyleText(css string) string {
	return planStyleEnd.ReplaceAllStringFunc(css, func(match string) string { return "<\\" + match[1:] })
}

func (s *Server) planView(w http.ResponseWriter, r *http.Request) {
	reader, ok := s.Core.(planViewReader)
	if !ok {
		fail(w, 503)
		return
	}
	view, err := reader.WebPlanView(r.Context(), r.URL.Query().Get("ref"))
	if err != nil {
		fail(w, 404)
		return
	}
	renderPlanView(w, r, view, false)
}

func renderPlanView(w http.ResponseWriter, r *http.Request, view ui.PlanView, inert bool) {
	renderReadonlyView(w, r, view, inert, "Plan view", previewMarkup(view.HTML))
}

// Shared fixed DOM driver for Plan and Cards read-only view consumers. Markup is
// sanitized by the owning route; candidate scripts never enter this shell.
func renderReadonlyView(w http.ResponseWriter, r *http.Request, view ui.PlanView, inert bool, title, markup string) {
	if view.HTML == "" || view.CSS == "" || len(view.Styles) == 0 {
		fail(w, 503)
		return
	}
	nonceBytes := make([]byte, 24)
	if _, err := rand.Read(nonceBytes); err != nil {
		fail(w, 500)
		return
	}
	nonce := base64.RawStdEncoding.EncodeToString(nonceBytes)
	w.Header().Set("Content-Security-Policy", "sandbox allow-scripts; default-src 'none'; script-src 'nonce-"+nonce+"'; style-src 'unsafe-inline'; font-src data:; img-src 'none'; connect-src 'none'; frame-src 'none'; form-action 'none'; base-uri 'none'; frame-ancestors 'self'")
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
	bodyAttribute := ""
	if inert {
		bodyAttribute = " inert"
	}
	_, _ = fmt.Fprint(w, `<!doctype html><html class="ogui-theme" data-widget-theme="`+theme+`" style="color-scheme:`+theme+`"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1"><title>`+title+`</title><script nonce="`+nonce+`">
(()=>{addEventListener('message',event=>{const value=event.data;if(event.source!==parent||value?.channel!=='steward-plan-view'||value.type!=='theme')return;if(value.theme==='light'||value.theme==='dark'){document.documentElement.style.colorScheme=value.theme;document.documentElement.dataset.widgetTheme=value.theme}if(Number.isFinite(value.viewportWidth)&&value.viewportWidth>0&&value.viewportWidth<=100000)document.documentElement.toggleAttribute('data-host-compact',value.viewportWidth<=`+strconv.Itoa(view.CompactMaxWidth)+`);for(const key of ['--card','--border','--foreground','--muted-foreground','--hover','--font-ui','--font-sans']){const token=value.tokens?.[key];if(typeof token==='string'&&token.length<=256)document.documentElement.style.setProperty(key,token)}last=0;if(document.body)report()});let last=0;const report=()=>{const height=Math.ceil(document.body.getBoundingClientRect().height);if(height!==last){last=height;parent.postMessage({channel:'steward-plan-view',type:'height',height},'*')}};addEventListener('DOMContentLoaded',()=>{if(!(`+cardsTextRestore+`)()){parent.postMessage({channel:'steward-plan-view',type:'failed'},'*');return}new ResizeObserver(report).observe(document.body);document.fonts.ready.then(report);report()})})();
</script><style>`+planStyleText(string(view.Styles))+`
:root{--card:var(--color-background-tertiary);--border:var(--color-border-tertiary);--foreground:var(--color-text-primary);--muted-foreground:var(--color-text-secondary);--hover:var(--color-background-secondary);--font-ui:var(--font-sans)}html,body{margin:0;min-height:0;height:auto}.ogui-theme body{font:14px/1.6 var(--font-ui);background:transparent}*{box-sizing:border-box}
</style><style>`+planStyleText(view.CSS)+`</style></head><body`+bodyAttribute+`>`+markup+`</body></html>`)
}

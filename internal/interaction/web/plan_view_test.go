package web

import (
	"context"
	"errors"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/z-chenhao/assistant/internal/delivery/ui"
	htmlnode "golang.org/x/net/html"
)

type planCore struct {
	identityCore
	view ui.PlanView
	err  error
}

func (c planCore) WebPlanView(context.Context, string) (ui.PlanView, error) { return c.view, c.err }

func TestUIPlanViewUsesNonceOnlyScriptAndReadOnlySanitization(t *testing.T) {
	core := planCore{view: ui.PlanView{HTML: `<details class="native-plan"><summary onclick="EVIL()">Plan: Go</summary><p>Original plan</p><script>EVIL()</script><form action="/api/message"><input name="text"></form><img src="https://bad.test/leak"></details>`, CSS: `p{color:red}</StYlE><script>CSS_EVIL()</script>`, Styles: []byte(`:root{--font-sans:Inter}`)}}
	server := &Server{Core: core}
	for _, destination := range []string{"iframe", "document"} {
		request := httptest.NewRequest("GET", "/ui/plan?ref=ui:original%230&theme=dark", nil)
		request.Header.Set("Sec-Fetch-Dest", destination)
		response := httptest.NewRecorder()
		server.planView(response, request)
		if response.Code != 200 {
			t.Fatal(response.Code, response.Body.String())
		}
		policy := response.Header().Get("Content-Security-Policy")
		for _, part := range []string{"sandbox allow-scripts", "script-src 'nonce-", "connect-src 'none'", "frame-src 'none'", "form-action 'none'", "base-uri 'none'"} {
			if !strings.Contains(policy, part) {
				t.Fatal("missing isolation", part, policy)
			}
		}
		if strings.Contains(policy, "script-src 'unsafe-inline'") {
			t.Fatal("candidate scripts authorized")
		}
		if destination == "document" {
			if response.Header().Get("Content-Type") != "text/plain; charset=utf-8" || !strings.Contains(response.Body.String(), "Original plan") || strings.Contains(response.Body.String(), "EVIL") {
				t.Fatal("direct navigation executed or exposed markup", response.Body.String())
			}
			continue
		}
		document, err := htmlnode.Parse(strings.NewReader(response.Body.String()))
		if err != nil {
			t.Fatal(err)
		}
		scripts, details, forms := 0, 0, 0
		var walk func(*htmlnode.Node)
		walk = func(n *htmlnode.Node) {
			if n.Type == htmlnode.ElementNode {
				if n.Data == "script" {
					scripts++
					nonce := ""
					for _, a := range n.Attr {
						if a.Key == "nonce" {
							nonce = a.Val
						}
					}
					if nonce == "" || !strings.Contains(policy, "'nonce-"+nonce+"'") {
						t.Fatal("script not uniquely nonce-authorized")
					}
				}
				if n.Data == "details" {
					details++
				}
				if n.Data == "form" {
					forms++
				}
				for _, a := range n.Attr {
					if strings.HasPrefix(a.Key, "on") || a.Key == "src" || a.Key == "action" {
						t.Fatal("candidate active attribute retained", a)
					}
				}
			}
			for c := n.FirstChild; c != nil; c = c.NextSibling {
				walk(c)
			}
		}
		walk(document)
		if scripts != 1 || details != 1 || forms != 0 || !strings.Contains(response.Body.String(), `data-widget-theme="dark"`) {
			t.Fatal("read-only view boundary changed", scripts, details, forms)
		}
	}
}

func TestUIPlanViewMissingSourceOrStylesNeverUsesLegacyRenderer(t *testing.T) {
	for _, core := range []planCore{{err: errors.New("unavailable")}, {view: ui.PlanView{HTML: "<details>Original</details>", CSS: "details{}"}}} {
		response := httptest.NewRecorder()
		(&Server{Core: core}).planView(response, httptest.NewRequest("GET", "/ui/plan?ref=ui:original%230", nil))
		if response.Code != 404 && response.Code != 503 {
			t.Fatal("missing managed view succeeded", response.Code)
		}
		if strings.Contains(response.Body.String(), "Websandbox") || strings.Contains(response.Body.String(), "native-plan") {
			t.Fatal("native fallback used")
		}
	}
}

type revokedPlanCore struct{ planCore }

func (revokedPlanCore) CheckChannel(context.Context, string, string, string) error {
	return errors.New("revoked")
}

func TestUIPlanViewRouteRetainsAuthenticationBindingAndNoStore(t *testing.T) {
	view := planCore{view: ui.PlanView{HTML: "<details><summary>Plan</summary></details>", CSS: "details{}", Styles: []byte("body{}")}}
	for _, tc := range []struct {
		token   bool
		revoked bool
		status  int
	}{{false, false, 401}, {true, true, 403}, {true, false, 200}} {
		s := &Server{Core: view, Config: Config{TokenRef: "credential:test"}, Secrets: fakeSecrets{}}
		if tc.revoked {
			s.Core = revokedPlanCore{view}
		}
		request := httptest.NewRequest("GET", "http://127.0.0.1:8080/ui/plan?ref=ui:original%230", nil)
		if tc.token {
			request.Header.Set("Authorization", "Bearer private-token-at-least-24-bytes")
		}
		response := httptest.NewRecorder()
		s.ServeHTTP(response, request)
		if response.Code != tc.status || response.Header().Get("Cache-Control") != "no-store" {
			t.Fatal("plan route bypassed normal guards", tc, response.Code, response.Header())
		}
	}
}

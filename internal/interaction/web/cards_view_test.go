package web

import (
	"context"
	"encoding/json"
	"errors"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/z-chenhao/assistant/internal/delivery/ui"
	"golang.org/x/net/html"
)

type cardsCore struct {
	identityCore
	view ui.CardsView
	err  error
}

func (c cardsCore) WebCardsView(context.Context, string) (ui.CardsView, error) { return c.view, c.err }

func TestUICardsViewSeparatesAcceptedRuntimeFromMarkupAndNetwork(t *testing.T) {
	core := cardsCore{view: ui.CardsView{HTML: `<section class="native-carousel"><button onclick="MARKUP_EVIL()">Original card</button><a href="https://outside.invalid">Details</a><img src="https://outside.invalid/leak" data-card-image="0"><script>MARKUP_EVIL()</script><form action="/api/message"><input name="text"></form></section>`, CSS: `p{color:red}</StYlE><script>CSS_ESCAPE()</script>`, Worker: `globalThis.fixture="</sCrIpT><script>RUNTIME_ESCAPE()</script>";`, Styles: []byte(`:root{--font-sans:Inter}`), CompactMaxWidth: 760}}
	for _, destination := range []string{"iframe", "document"} {
		r := httptest.NewRequest("GET", "/ui/cards?ref=ui:original%231&theme=dark", nil)
		r.Header.Set("Sec-Fetch-Dest", destination)
		w := httptest.NewRecorder()
		(&Server{Core: core}).cardsView(w, r)
		if w.Code != 200 {
			t.Fatal(w.Code)
		}
		policy := w.Header().Get("Content-Security-Policy")
		for _, part := range []string{"sandbox allow-scripts", "script-src 'nonce-", "connect-src 'none'", "img-src data:", "frame-src 'none'", "form-action 'none'", "base-uri 'none'"} {
			if !strings.Contains(policy, part) {
				t.Fatal("missing Cards isolation", part, policy)
			}
		}
		if strings.Contains(policy, "script-src 'unsafe-inline'") || strings.Contains(policy, "allow-same-origin") || strings.Contains(policy, "allow-popups") {
			t.Fatal("candidate gained ambient authority", policy)
		}
		if destination == "document" {
			if w.Header().Get("Content-Type") != "text/plain; charset=utf-8" || !strings.Contains(w.Body.String(), "Original card") || strings.Contains(w.Body.String(), "ESCAPE") || strings.Contains(w.Body.String(), "EVIL") {
				t.Fatal("direct navigation exposed executable content", w.Body.String())
			}
			continue
		}
		doc, err := html.Parse(strings.NewReader(w.Body.String()))
		if err != nil {
			t.Fatal(err)
		}
		scripts, buttons := 0, 0
		var walk func(*html.Node)
		walk = func(n *html.Node) {
			if n.Type == html.ElementNode {
				if n.Data == "script" {
					scripts++
					nonce := ""
					for _, a := range n.Attr {
						if a.Key == "nonce" {
							nonce = a.Val
						}
					}
					if nonce == "" || !strings.Contains(policy, "'nonce-"+nonce+"'") {
						t.Fatal("unaccepted script escaped CSP")
					}
				}
				if n.Data == "button" {
					buttons++
				}
				if n.Data == "form" || n.Data == "input" {
					t.Fatal("markup created a submission channel")
				}
				for _, a := range n.Attr {
					if strings.HasPrefix(a.Key, "on") || a.Key == "src" || a.Key == "href" || a.Key == "action" {
						t.Fatal("markup retained ambient effect", a)
					}
				}
			}
			for c := n.FirstChild; c != nil; c = c.NextSibling {
				walk(c)
			}
		}
		walk(doc)
		if scripts != 3 || buttons != 1 {
			t.Fatal("expected fixed controller, trusted gesture driver and fixed Worker driver only", scripts, buttons)
		}
	}
}

type revokedCardsCore struct{ cardsCore }

func TestUICardsHistoricalRuntimeUsesOnlyReviewedWorkerAdapter(t *testing.T) {
	raw, err := os.ReadFile("../../rsi/builtin/ui-cards-original.json")
	if err != nil {
		t.Fatal(err)
	}
	var oracle struct {
		Expected struct {
			Views []struct {
				Runtime string `json:"runtime"`
			} `json:"views"`
		} `json:"expected"`
	}
	if err = json.Unmarshal(raw, &oracle); err != nil {
		t.Fatal(err)
	}
	for _, accepted := range []bool{true, false} {
		runtime := oracle.Expected.Views[0].Runtime
		if !accepted {
			runtime += "\nlocation.href='https://outside.invalid';"
		}
		w := httptest.NewRecorder()
		renderCardsView(w, httptest.NewRequest("GET", "/ui/cards?ref=ui:old%230", nil), ui.CardsView{HTML: `<section>Historical</section>`, CSS: `section{}`, Runtime: runtime, Styles: []byte(`body{}`), CompactMaxWidth: 760})
		if accepted {
			if w.Code != 200 || !strings.Contains(w.Body.String(), "new Worker(") || strings.Contains(w.Body.String(), runtime) {
				t.Fatal("historical DOM source executed or compatibility missing", w.Code)
			}
		} else if w.Code != 503 {
			t.Fatal("unknown historical Runtime accepted", w.Code)
		}
	}
}

func (revokedCardsCore) CheckChannel(context.Context, string, string, string) error {
	return errors.New("revoked")
}

func TestUICardsViewRouteRetainsAuthenticationBindingAndNoStore(t *testing.T) {
	core := cardsCore{view: ui.CardsView{HTML: `<section>Cards</section>`, CSS: `section{}`, Worker: `void 0`, Styles: []byte(`body{}`), CompactMaxWidth: 760}}
	for _, tc := range []struct {
		token, revoked bool
		status         int
	}{{false, false, 401}, {true, true, 403}, {true, false, 200}} {
		s := &Server{Core: core, Config: Config{TokenRef: "credential:test"}, Secrets: fakeSecrets{}}
		if tc.revoked {
			s.Core = revokedCardsCore{core}
		}
		r := httptest.NewRequest("GET", "http://127.0.0.1:8080/ui/cards?ref=ui:original%231", nil)
		if tc.token {
			r.Header.Set("Authorization", "Bearer private-token-at-least-24-bytes")
		}
		w := httptest.NewRecorder()
		s.ServeHTTP(w, r)
		if w.Code != tc.status || w.Header().Get("Cache-Control") != "no-store" {
			t.Fatal("Cards route bypassed normal guards", tc, w.Code, w.Header())
		}
	}
}

func TestUICardsViewMissingAcceptedBytesNeverFallsBack(t *testing.T) {
	for _, core := range []cardsCore{{err: errors.New("unavailable")}, {view: ui.CardsView{HTML: `<section>Cards</section>`, CSS: `section{}`, Styles: []byte(`body{}`), CompactMaxWidth: 760}}} {
		w := httptest.NewRecorder()
		(&Server{Core: core}).cardsView(w, httptest.NewRequest("GET", "/ui/cards?ref=ui:original%231", nil))
		if w.Code != 404 && w.Code != 503 {
			t.Fatal("missing managed Cards bytes rendered", w.Code)
		}
	}
}

func TestUICardsContextFormatReturnsOnlyFrozenDescriptions(t *testing.T) {
	core := cardsCore{view: ui.CardsView{HTML: `<section>PRIVATE_HTML</section>`, Runtime: `PRIVATE_RUNTIME()`, CSS: `PRIVATE_CSS{}`, Contexts: []ui.CardsContext{{Title: "Choices", VisibleItem: "Original Alpha"}}}}
	w := httptest.NewRecorder()
	(&Server{Core: core}).cardsView(w, httptest.NewRequest("GET", "/ui/cards?ref=ui:original%231&format=context", nil))
	if w.Code != 200 || !strings.HasPrefix(w.Header().Get("Content-Type"), "application/json") || strings.Contains(w.Body.String(), "PRIVATE_") || !strings.Contains(w.Body.String(), "Original Alpha") {
		t.Fatal("context format mixed presentation authority", w.Code, w.Body.String())
	}
	core.view.Contexts = nil
	w = httptest.NewRecorder()
	(&Server{Core: core}).cardsView(w, httptest.NewRequest("GET", "/ui/cards?ref=ui:original%231&format=context", nil))
	if w.Code != 503 {
		t.Fatal("missing frozen description accepted", w.Code)
	}
}

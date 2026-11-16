package web

import (
	"context"
	"errors"
	"net/http/httptest"
	"net/url"
	"reflect"
	"strings"
	"testing"

	"github.com/z-chenhao/assistant/internal/agent"
	"github.com/z-chenhao/assistant/internal/delivery/ui"
	"golang.org/x/net/html"
)

type tableCore struct {
	identityCore
	view ui.TableView
	err  error
}

func (c tableCore) WebTableView(_ context.Context, ref string) (ui.TableView, error) {
	if ref != "ui:original#1" {
		return ui.TableView{}, agent.ErrNotFound
	}
	return c.view, c.err
}

type revokedTableCore struct{ tableCore }

func (revokedTableCore) CheckChannel(context.Context, string, string, string) error {
	return errors.New("revoked")
}

func TestUITableViewUsesOnlyHostScriptAndInertMarkup(t *testing.T) {
	core := tableCore{view: ui.TableView{HTML: `<section class="answer-table" data-table-facts="e30="><table><tr><td><button onclick="MARKUP_EVIL()">Original cell</button></td></tr></table><a href="https://outside.invalid">link</a><meta http-equiv="refresh" content="0;url=https://outside.invalid"><img src="https://outside.invalid"><script>MARKUP_EVIL()</script><form action="/api/message"><input></form></section>`, CSS: `p{color:red}</StYlE><script>CSS_ESCAPE()</script>`, Worker: `globalThis.workerText="</sCrIpT><script>WORKER_ESCAPE()</script>";`, Styles: []byte(`body{}`), CompactMaxWidth: 760}}
	for _, destination := range []string{"iframe", "document"} {
		r := httptest.NewRequest("GET", "/ui/table?ref=ui:original%231", nil)
		r.Header.Set("Sec-Fetch-Dest", destination)
		w := httptest.NewRecorder()
		(&Server{Core: core}).tableView(w, r)
		if w.Code != 200 {
			t.Fatal(w.Code, w.Body.String())
		}
		policy := w.Header().Get("Content-Security-Policy")
		for _, part := range []string{"sandbox allow-scripts", "script-src 'nonce-", "worker-src blob:", "connect-src 'none'", "img-src 'none'", "frame-src 'none'", "form-action 'none'", "base-uri 'none'"} {
			if !strings.Contains(policy, part) {
				t.Fatal("missing Table boundary", part, policy)
			}
		}
		if strings.Contains(policy, "allow-same-origin") || strings.Contains(policy, "allow-popups") || strings.Contains(policy, "'unsafe-eval'") {
			t.Fatal("ambient authority allowed", policy)
		}
		if destination == "document" {
			if w.Header().Get("Content-Type") != "text/plain; charset=utf-8" || strings.Contains(w.Body.String(), "ESCAPE") || strings.Contains(w.Body.String(), "EVIL") || !strings.Contains(w.Body.String(), "Original cell") {
				t.Fatal("navigation exposed executable content", w.Body.String())
			}
			continue
		}
		doc, err := html.Parse(strings.NewReader(w.Body.String()))
		if err != nil {
			t.Fatal(err)
		}
		scripts := 0
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
						t.Fatal("script lacked exact Host nonce")
					}
				}
				if strings.Contains(" form input a img iframe ", " "+n.Data+" ") {
					t.Fatal("ambient tag survived", n.Data)
				}
				if n.Data == "meta" {
					for _, a := range n.Attr {
						if a.Key == "http-equiv" {
							t.Fatal("navigation meta survived")
						}
					}
				}
				for _, a := range n.Attr {
					if strings.HasPrefix(a.Key, "on") || a.Key == "href" || a.Key == "src" || a.Key == "action" {
						t.Fatal("ambient attribute survived", a)
					}
				}
			}
			for c := n.FirstChild; c != nil; c = c.NextSibling {
				walk(c)
			}
		}
		walk(doc)
		if scripts != 1 {
			t.Fatal("candidate source created a DOM script", scripts)
		}
	}
}

func TestUITableViewRouteRetainsAuthChannelAndExactQuery(t *testing.T) {
	core := tableCore{view: ui.TableView{HTML: `<section>Table</section>`, CSS: `section{}`, Worker: `void 0`, Styles: []byte(`body{}`), CompactMaxWidth: 760}}
	for _, c := range []struct {
		token, revoked bool
		status         int
	}{{false, false, 401}, {true, true, 403}, {true, false, 200}} {
		s := &Server{Core: core, Config: Config{TokenRef: "credential:test"}, Secrets: fakeSecrets{}}
		if c.revoked {
			s.Core = revokedTableCore{core}
		}
		r := httptest.NewRequest("GET", "http://127.0.0.1:8080/ui/table?ref=ui:original%231", nil)
		if c.token {
			r.Header.Set("Authorization", "Bearer private-token-at-least-24-bytes")
		}
		w := httptest.NewRecorder()
		s.ServeHTTP(w, r)
		if w.Code != c.status || w.Header().Get("Cache-Control") != "no-store" {
			t.Fatal("ordinary guards bypassed", c, w.Code, w.Header())
		}
	}
	for _, query := range []string{"ref=ui:original%231&ref=other", "ref=ui:original%231&url=https://outside.invalid", "ref=ui:original%231&format=worker", "ref=ui:original%231&format=context&format=context", "ref=ui:original%231&theme=dark&theme=light", "ref=%xx"} {
		w := httptest.NewRecorder()
		(&Server{Core: core}).tableView(w, httptest.NewRequest("GET", "/ui/table?"+query, nil))
		if w.Code != 400 {
			t.Fatal("ambiguous query accepted", query, w.Code)
		}
	}
}

func TestUITableContextDoesNotCarryWorkerOrMarkupAndMissingViewRejects(t *testing.T) {
	contexts := []ui.TableSortContext{}
	for column := -1; column < 1; column++ {
		for _, direction := range []int{1, -1} {
			var label *string
			if column >= 0 {
				name := "N"
				label = &name
			}
			contexts = append(contexts, ui.TableSortContext{Sort: column, Direction: direction, Description: ui.TableDescription{Title: "Frozen", Sort: label, Direction: direction}})
		}
	}
	core := tableCore{view: ui.TableView{HTML: "PRIVATE_HTML", Worker: "PRIVATE_WORKER", CSS: "PRIVATE_CSS", Contexts: contexts}}
	w := httptest.NewRecorder()
	(&Server{Core: core}).tableView(w, httptest.NewRequest("GET", "/ui/table?ref=ui:original%231&format=context", nil))
	if w.Code != 200 || strings.Contains(w.Body.String(), "PRIVATE_") || !strings.Contains(w.Body.String(), "Frozen") {
		t.Fatal("context mixed execution authority", w.Code, w.Body.String())
	}
	for _, c := range []tableCore{{err: errors.New("unavailable")}, {view: ui.TableView{HTML: "Table", CSS: "table{}", Styles: []byte(`body{}`), CompactMaxWidth: 760}}} {
		w := httptest.NewRecorder()
		(&Server{Core: c}).tableView(w, httptest.NewRequest("GET", "/ui/table?ref=ui:original%231", nil))
		if w.Code != 404 && w.Code != 503 {
			t.Fatal("missing accepted Worker rendered", w.Code)
		}
	}
}

func TestUITableExportAcceptsOnlyCanonicalOriginalRowPositions(t *testing.T) {
	for _, query := range []string{"rows=0,2,1", "rows="} {
		values, _ := url.ParseQuery(query)
		rows, ok := tableExportIndices(values)
		if !ok || rows == nil {
			t.Fatal(query, rows, ok)
		}
		if query == "rows=0,2,1" && !reflect.DeepEqual(rows, []int{0, 2, 1}) {
			t.Fatal(rows)
		}
	}
	for _, query := range []string{"", "rows=0&rows=1", "rows=00", "rows=-1", "rows=200", "rows=0,0", "rows=0,", "rows=%2B1", "rows=1.0"} {
		values, _ := url.ParseQuery(query)
		if rows, ok := tableExportIndices(values); ok {
			t.Fatal("invalid export accepted", query, rows)
		}
	}
}

func (c tableCore) WebTableExport(ctx context.Context, ref string, rows []int) (ui.TableExport, error) {
	if _, err := c.WebTableView(ctx, ref); err != nil {
		return ui.TableExport{}, err
	}
	result := ui.TableExport{Title: "Original", Columns: []string{"Cell"}, Rows: [][]string{}}
	for _, row := range rows {
		if row != 0 {
			return ui.TableExport{}, agent.ErrNotFound
		}
		result.Rows = append(result.Rows, []string{"=original-private-value"})
	}
	return result, nil
}

func TestUITableExportRouteRetainsAuthorizationAndOriginalDataBoundary(t *testing.T) {
	for _, scenario := range []struct {
		name                      string
		token, revoked, forgotten bool
		query                     string
		status                    int
	}{
		{name: "unauthenticated", query: "&rows=0", status: 401},
		{name: "revoked channel", token: true, revoked: true, query: "&rows=0", status: 403},
		{name: "forgotten", token: true, forgotten: true, query: "&rows=0", status: 404},
		{name: "original", token: true, query: "&rows=0", status: 200},
		{name: "empty", token: true, query: "&rows=", status: 200},
		{name: "missing rows", token: true, status: 400},
		{name: "candidate bytes", token: true, query: "&rows=0&bytes=invented", status: 400},
		{name: "candidate destination", token: true, query: "&rows=0&url=https://outside.invalid", status: 400},
		{name: "foreign row", token: true, query: "&rows=1", status: 404},
		{name: "duplicate row", token: true, query: "&rows=0,0", status: 400},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			core := tableCore{}
			if scenario.forgotten {
				core.err = agent.ErrNotFound
			}
			server := &Server{Core: core, Config: Config{TokenRef: "credential:test"}, Secrets: fakeSecrets{}}
			if scenario.revoked {
				server.Core = revokedTableCore{core}
			}
			request := httptest.NewRequest("GET", "http://127.0.0.1:8080/ui/table?ref=ui:original%231&format=export"+scenario.query, nil)
			if scenario.token {
				request.Header.Set("Authorization", "Bearer private-token-at-least-24-bytes")
			}
			response := httptest.NewRecorder()
			server.ServeHTTP(response, request)
			if response.Code != scenario.status || response.Header().Get("Cache-Control") != "no-store" {
				t.Fatal(response.Code, response.Header(), response.Body.String())
			}
			if scenario.status == 200 {
				if !strings.Contains(response.Body.String(), `"title":"Original"`) || strings.Contains(response.Body.String(), "worker") {
					t.Fatal(response.Body.String())
				}
			} else if strings.Contains(response.Body.String(), "original-private-value") {
				t.Fatal("denied request leaked data")
			}
		})
	}
}

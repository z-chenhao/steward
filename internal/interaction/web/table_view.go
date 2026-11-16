package web

import (
	"context"
	"crypto/rand"
	_ "embed"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/z-chenhao/assistant/internal/delivery/ui"
	htmlnode "golang.org/x/net/html"
)

type tableExportReader interface {
	WebTableExport(context.Context, string, []int) (ui.TableExport, error)
}

type tableViewReader interface {
	WebTableView(context.Context, string) (ui.TableView, error)
}

//go:embed view/table-driver.js
var tableDOMDriver string

func (s *Server) tableView(w http.ResponseWriter, r *http.Request) {
	query, err := url.ParseQuery(r.URL.RawQuery)
	if err != nil || len(query["ref"]) != 1 || len(query["format"]) > 1 || len(query["theme"]) > 1 || len(query["rows"]) > 1 {
		fail(w, 400)
		return
	}
	for key := range query {
		if key != "ref" && key != "format" && key != "theme" && key != "rows" {
			fail(w, 400)
			return
		}
	}
	if format := query.Get("format"); format != "" && format != "context" && format != "export" {
		fail(w, 400)
		return
	}

	if query.Get("format") == "export" {
		rows, valid := tableExportIndices(query)
		if !valid {
			fail(w, 400)
			return
		}
		reader, ok := s.Core.(tableExportReader)
		if !ok {
			fail(w, 503)
			return
		}
		data, err := reader.WebTableExport(r.Context(), query.Get("ref"), rows)
		if err != nil {
			fail(w, 404)
			return
		}
		jsonResult(w, data)
		return
	}
	if _, present := query["rows"]; present {
		fail(w, 400)
		return
	}
	reader, ok := s.Core.(tableViewReader)
	if !ok {
		fail(w, 503)
		return
	}
	view, err := reader.WebTableView(r.Context(), query.Get("ref"))
	if err != nil {
		fail(w, 404)
		return
	}
	if query.Get("format") == "context" {
		if len(view.Contexts) < 4 || len(view.Contexts) > 26 {
			fail(w, 503)
			return
		}
		jsonResult(w, view.Contexts)
		return
	}
	renderTableView(w, r, view)
}

// Empty rows= is a valid zero-row export; omitted/aliased/duplicate IDs are not.
func tableExportIndices(query url.Values) ([]int, bool) {
	if len(query["rows"]) != 1 || len(query.Get("rows")) > 799 {
		return nil, false
	}
	rows := []int{}
	if query.Get("rows") == "" {
		return rows, true
	}
	seen := map[int]bool{}
	for _, text := range strings.Split(query.Get("rows"), ",") {
		row, err := strconv.Atoi(text)
		if err != nil || row < 0 || row >= 200 || strconv.Itoa(row) != text || seen[row] || len(rows) >= 200 {
			return nil, false
		}
		seen[row] = true
		rows = append(rows, row)
	}
	return rows, true
}

func renderTableView(w http.ResponseWriter, r *http.Request, view ui.TableView) {
	renderTableFrame(w, r, view, false)
}

func renderTableFrame(w http.ResponseWriter, r *http.Request, view ui.TableView, readonly bool) {
	if view.HTML == "" || view.CSS == "" || view.Worker == "" || len(view.Styles) == 0 || view.CompactMaxWidth < 1 || view.CompactMaxWidth > 4096 {
		fail(w, 503)
		return
	}
	markup := tableMarkup(view.HTML)
	nonceBytes := make([]byte, 24)
	if _, err := rand.Read(nonceBytes); err != nil {
		fail(w, 500)
		return
	}
	nonce := base64.RawStdEncoding.EncodeToString(nonceBytes)
	w.Header().Set("Content-Security-Policy", "sandbox allow-scripts; default-src 'none'; script-src 'nonce-"+nonce+"'; worker-src blob:; style-src 'unsafe-inline'; font-src data:; img-src 'none'; connect-src 'none'; frame-src 'none'; form-action 'none'; base-uri 'none'; frame-ancestors 'self'")
	w.Header().Set("Permissions-Policy", "camera=(), microphone=(), geolocation=(), clipboard-read=(), clipboard-write=()")
	w.Header().Set("Origin-Agent-Cluster", "?1")
	w.Header().Set("Referrer-Policy", "no-referrer")
	if r.Header.Get("Sec-Fetch-Dest") == "document" {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		root, _ := htmlnode.Parse(strings.NewReader(markup))
		var parts []string
		var text func(*htmlnode.Node)
		text = func(n *htmlnode.Node) {
			if n.Type == htmlnode.ElementNode && n.Data == "template" {
				return
			}
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
	bodyAttribute := ""
	title := "Table view"
	if readonly {
		bodyAttribute = " inert"
		title = "Table preview"
	}
	theme := "light"
	if r.URL.Query().Get("theme") == "dark" {
		theme = "dark"
	}
	// json.Marshal escapes <, >, &, U+2028/2029. Candidate source remains a
	// serialized Blob payload, never a script tag executed in the DOM realm.
	program, err := json.Marshal(view.Worker)
	if err != nil {
		fail(w, 500)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = fmt.Fprint(w, `<!doctype html><html class="ogui-theme" lang="en" data-compact-max-width="`+strconv.Itoa(view.CompactMaxWidth)+`" data-widget-theme="`+theme+`" style="color-scheme:`+theme+`"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1"><title>`+title+`</title><style>`+planStyleText(string(view.Styles))+`
:root{--card:var(--color-background-tertiary);--border:var(--color-border-tertiary);--foreground:var(--color-text-primary);--muted-foreground:var(--color-text-secondary);--hover:var(--color-background-secondary);--font-ui:var(--font-sans)}html,body{margin:0;min-height:0;height:auto}.ogui-theme body{font:14px/1.6 var(--font-ui);background:transparent}*{box-sizing:border-box}
</style><style>`+planStyleText(view.CSS)+`</style></head><body`+bodyAttribute+`>`+markup+`<script nonce="`+nonce+`">(`+tableDOMDriver+`)(`+string(program)+`,`+strconv.FormatBool(readonly)+`);</script></body></html>`)
}

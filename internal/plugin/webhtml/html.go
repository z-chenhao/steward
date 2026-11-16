// Package webhtml owns the replaceable, repository-private simple HTML recipe.
// The existing Readability/PDF runtime is a separate, unmigrated boundary.
package webhtml

import (
	"golang.org/x/net/html"
	"strings"
	"unicode/utf8"
)

func parseHTML(raw string) (string, string) {
	doc, e := html.Parse(strings.NewReader(raw))
	if e != nil {
		return "", ""
	}
	var title, body strings.Builder
	var visit func(*html.Node, bool)
	visit = func(n *html.Node, inTitle bool) {
		if n.Type == html.ElementNode {
			switch n.Data {
			case "script", "style", "noscript", "svg", "nav", "footer", "form", "iframe", "template", "head":
				if n.Data == "head" {
					for c := n.FirstChild; c != nil; c = c.NextSibling {
						if c.Data == "title" {
							visit(c, true)
						}
					}
				}
				return
			}
			if n.Data == "title" {
				inTitle = true
			}
		}
		if n.Type == html.TextNode {
			if inTitle {
				title.WriteString(n.Data)
			} else {
				body.WriteString(n.Data)
				body.WriteByte(' ')
			}
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			visit(c, inTitle)
		}
	}
	visit(doc, false)
	return preview(strings.Join(strings.Fields(title.String()), " "), 1024), strings.Join(strings.Fields(body.String()), " ")
}

func preview(s string, n int) string {
	if len(s) <= n {
		return s
	}
	for n > 0 && !utf8.ValidString(s[:n]) {
		n--
	}
	return s[:n]
}

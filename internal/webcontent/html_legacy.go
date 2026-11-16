package webcontent

import (
	"context"
	"strings"

	"golang.org/x/net/html"
)

type htmlParserKey struct{}
type HTMLParser func(context.Context, []byte) (string, string, error)

// WithHTMLParser binds only the original fetch operation's HTML decoder. It
// grants no URL, filesystem, model or database operation to the decoder.
func WithHTMLParser(ctx context.Context, parse HTMLParser) context.Context {
	return context.WithValue(ctx, htmlParserKey{}, parse)
}

// ParseHTML uses a frozen managed decoder when attached by the original owner.
// The remaining native entry is restricted to pre-parser Runs/unbundled carriers.
// Errors from a managed decoder never fall back to the legacy recipe.
func ParseHTML(ctx context.Context, data []byte) (string, string, error) {
	if parse, ok := ctx.Value(htmlParserKey{}).(HTMLParser); ok {
		return parse(ctx, data)
	}
	title, text := parseHTMLLegacyV1(string(data))
	return title, text, nil
}

// Frozen pre-parser V1 implementation. Its exact consumers are retained Runs
// without steward.web-html/1 and explicitly unbundled or previously selected nineteen-package carriers.
// Managed HTML always takes the bound decoder above, including on failure.
// Remove this compatibility implementation once those consumers are gone.
func parseHTMLLegacyV1(raw string) (string, string) {
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
	return Preview(strings.Join(strings.Fields(title.String()), " "), 1024), strings.Join(strings.Fields(body.String()), " ")
}

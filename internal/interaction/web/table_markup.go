package web

import (
	"bytes"
	"strings"

	"golang.org/x/net/html"
	"golang.org/x/net/html/atom"
)

// Table markup is inert. Candidate policy runs in a Worker; markup never
// supplies scripts, navigation, resources, forms or Host DOM capabilities.
func tableMarkup(source string) string {
	nodes, err := html.ParseFragment(strings.NewReader(source), &html.Node{Type: html.ElementNode, Data: "body", DataAtom: atom.Body})
	if err != nil {
		return ""
	}
	root := &html.Node{Type: html.ElementNode, Data: "div", DataAtom: atom.Div}
	for _, node := range nodes {
		root.AppendChild(node)
	}
	var clean func(*html.Node)
	clean = func(parent *html.Node) {
		for node := parent.FirstChild; node != nil; {
			next := node.NextSibling
			if node.Type == html.ElementNode {
				if !strings.Contains(" section div p button table caption thead tbody tr th td svg path template ", " "+node.Data+" ") {
					parent.RemoveChild(node)
					node = next
					continue
				}
				attrs := node.Attr[:0]
				for _, attr := range node.Attr {
					key := strings.ToLower(attr.Key)
					if strings.HasPrefix(key, "aria-") || strings.Contains(" class type disabled role tabindex scope data-numeric data-table-facts data-table-sort-icon viewbox width height fill stroke stroke-width stroke-linecap stroke-linejoin d ", " "+key+" ") {
						attrs = append(attrs, attr)
					}
				}
				node.Attr = attrs
				clean(node)
			} else if node.Type != html.TextNode {
				parent.RemoveChild(node)
			}
			node = next
		}
	}
	clean(root)
	var out bytes.Buffer
	for node := root.FirstChild; node != nil; node = node.NextSibling {
		if err := html.Render(&out, node); err != nil {
			return ""
		}
	}
	return out.String()
}

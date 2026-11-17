package web

import (
	"bytes"
	"strings"

	"golang.org/x/net/html"
	"golang.org/x/net/html/atom"
)

// Cards may execute the accepted package runtime, never scripts or event
// handlers smuggled through markup. Image and link targets are Host-mediated;
// this fragment cannot navigate or choose authenticated resource URLs.
func cardsMarkup(source string) string {
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
				if !strings.Contains(" section article div h3 span p button a img svg path ", " "+node.Data+" ") {
					parent.RemoveChild(node)
					node = next
					continue
				}
				attrs := node.Attr[:0]
				for _, attr := range node.Attr {
					key := strings.ToLower(attr.Key)
					if strings.HasPrefix(key, "aria-") || strings.Contains(" class type disabled loading alt data-slot data-selected data-card-image data-card-text viewbox width height fill stroke stroke-width stroke-linecap stroke-linejoin d ", " "+key+" ") {
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

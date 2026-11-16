package web

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"html/template"
	"net/url"
	"strings"

	"github.com/z-chenhao/assistant/internal/connectedapps"
	xhtml "golang.org/x/net/html"
	"golang.org/x/net/html/atom"
)

func compileAppDirectoryMarkup(source string) (*template.Template, error) {
	t, err := parseAppDirectoryMarkup(source)
	if err != nil {
		return nil, err
	}
	var seed [16]byte
	if _, err = rand.Read(seed[:]); err != nil {
		return nil, err
	}
	n := hex.EncodeToString(seed[:])
	d := appDirectoryMarkupData{AppViews: "views_" + n, Discover: "discover_" + n, YourApps: "connected_" + n, AllApps: "all_" + n, BackToApps: "back_" + n, CategoryLabel: "category_" + n, AllCategories: "categories_" + n, Filter: "filter_" + n, MoreApps: "more_" + n, Search: "search &" + n, Category: "category_" + n, NextCursor: "cursor /&" + n, OptionID: "option_" + n, OptionName: "option_name_" + n}
	d.Options = template.HTML(`<option value="first_` + n + `" data-probe="first_` + n + `">Original first</option><option value="second_` + n + `" data-probe="second_` + n + `" selected>Original selected</option>`)
	for flags := 0; flags < 4; flags++ {
		d.ConnectedCurrent = flags&1 != 0
		d.DirectoryCurrent = flags&2 != 0
		d.DiscoverCurrent = flags == 0
		for _, present := range []bool{false, true} {
			d.CollectionPresent = present
			d.CategoriesPresent = present
			d.PaginationPresent = present
			d.OptionSelected = present
			for _, part := range []string{"tabs", "collection-back", "categories", "pagination", "option"} {
				var out bytes.Buffer
				if err := t.ExecuteTemplate(&out, "app-directory-view-"+part, d); err != nil || !validAppDirectoryMarkup(out.String(), part, d) {
					return nil, connectedapps.ErrUnavailable
				}
			}
		}
	}
	return t, nil
}
func validAppDirectoryMarkup(raw, part string, d appDirectoryMarkupData) bool {
	if part == "collection-back" && !d.CollectionPresent || part == "categories" && !d.CategoriesPresent || part == "pagination" && !d.PaginationPresent {
		return strings.TrimSpace(raw) == ""
	}
	// Parse the fallback with scripting disabled too: noscript is an actual
	// original form path, so its button must be checked as DOM, not raw text.
	nodes, err := xhtml.ParseFragmentWithOptions(strings.NewReader(raw), &xhtml.Node{Type: xhtml.ElementNode, Data: "div", DataAtom: atom.Div}, xhtml.ParseOptionEnableScripting(false))
	if err != nil {
		return false
	}
	root := &xhtml.Node{Type: xhtml.ElementNode, Data: "div"}
	for _, n := range nodes {
		root.AppendChild(n)
	}
	approved := map[*xhtml.Node]bool{}
	protected := map[*xhtml.Node]bool{}
	exact := func(n *xhtml.Node, attrs map[string]string) bool {
		if n == nil || !appSettingsAttributes(n, attrs) {
			return false
		}
		approved[n] = true
		return true
	}
	link := func(href, label, classes string, current bool) *xhtml.Node {
		n := markupOne(root, func(n *xhtml.Node) bool { return n.Data == "a" && markupAttr(n, "href") == href })
		attrs := map[string]string{"href": href}
		if classes != "" {
			attrs["class"] = classes
		}
		if current {
			attrs["aria-current"] = "page"
		}
		if !exact(n, attrs) || markupText(n, nil) != label {
			return nil
		}
		return n
	}
	switch part {
	case "tabs":
		nav := markupOne(root, func(n *xhtml.Node) bool { return n.Data == "nav" })
		if !exact(nav, map[string]string{"class": "apps-tabs", "aria-label": d.AppViews}) {
			return false
		}
		for _, x := range []struct {
			href, label string
			current     bool
		}{{"/settings/apps", d.Discover, d.DiscoverCurrent}, {"/settings/apps?view=connected", d.YourApps, d.ConnectedCurrent}, {"/settings/apps?view=all", d.AllApps, d.DirectoryCurrent}} {
			n := link(x.href, x.label, "", x.current)
			if n == nil || !markupContains(nav, n) {
				return false
			}
		}
	case "collection-back":
		if link("/settings/apps", d.BackToApps, "back-link", false) == nil {
			return false
		}
	case "pagination":
		node := markupOne(root, func(n *xhtml.Node) bool { return n.Data == "a" })
		if node == nil {
			return false
		}
		href := markupAttr(node, "href")
		parsed, err := url.Parse(href)
		if err != nil || !strings.HasPrefix(href, "/settings/apps?") || parsed.Scheme != "" || parsed.Host != "" || parsed.Path != "/settings/apps" || parsed.RawPath != "" || parsed.Fragment != "" {
			return false
		}
		query, err := url.ParseQuery(parsed.RawQuery)
		if err != nil || len(query) != 4 {
			return false
		}
		for name, value := range map[string]string{"view": "all", "search": d.Search, "category": d.Category, "cursor": d.NextCursor} {
			if len(query[name]) != 1 || query.Get(name) != value {
				return false
			}
		}
		if link(href, d.MoreApps, "button secondary", false) == nil {
			return false
		}
	case "option":
		option := markupOne(root, func(n *xhtml.Node) bool { return n.Data == "option" })
		attrs := map[string]string{"value": d.OptionID}
		if d.OptionSelected {
			attrs["selected"] = ""
		}
		if !exact(option, attrs) || markupText(option, nil) != d.OptionName {
			return false
		}
	case "categories":
		form := markupOne(root, func(n *xhtml.Node) bool { return n.Data == "form" })
		if !exact(form, map[string]string{"method": "get", "action": "/settings/apps", "class": "apps-filter", "data-app-category-form": ""}) {
			return false
		}
		for name, value := range map[string]string{"view": "all", "search": d.Search} {
			input := markupOne(root, func(n *xhtml.Node) bool { return n.Data == "input" && markupAttr(n, "name") == name })
			if !exact(input, map[string]string{"type": "hidden", "name": name, "value": value}) || !markupContains(form, input) {
				return false
			}
		}
		label := markupOne(root, func(n *xhtml.Node) bool { return n.Data == "label" })
		if !exact(label, map[string]string{"for": "app-category", "class": "sr-only"}) || markupText(label, nil) != d.CategoryLabel || !markupContains(form, label) {
			return false
		}
		selectNode := markupOne(root, func(n *xhtml.Node) bool { return n.Data == "select" })
		if !exact(selectNode, map[string]string{"id": "app-category", "name": "category"}) || !markupContains(form, selectNode) {
			return false
		}
		defaultOption := markupOne(root, func(n *xhtml.Node) bool { return n.Data == "option" && markupAttr(n, "value") == "" })
		if !exact(defaultOption, map[string]string{"value": ""}) || defaultOption.Parent != selectNode || markupText(defaultOption, nil) != d.AllCategories {
			return false
		}
		expected, err := xhtml.ParseFragment(strings.NewReader(string(d.Options)), &xhtml.Node{Type: xhtml.ElementNode, Data: "select", DataAtom: atom.Select})
		if err != nil || len(expected) == 0 {
			return false
		}
		for _, node := range expected {
			canonical := markupSerialize(node)
			found := markupOne(root, func(n *xhtml.Node) bool { return n.Type == xhtml.ElementNode && markupSerialize(n) == canonical })
			if found == nil || found.Parent != selectNode || protected[found] {
				return false
			}
			protected[found] = true
		}
		fallback := markupOne(root, func(n *xhtml.Node) bool { return n.Data == "noscript" })
		if !exact(fallback, map[string]string{}) || !markupContains(form, fallback) {
			return false
		}
		button := markupOne(root, func(n *xhtml.Node) bool { return n.Data == "button" })
		if !exact(button, map[string]string{"class": "secondary"}) || !markupContains(fallback, button) || markupText(button, nil) != d.Filter {
			return false
		}
	default:
		return false
	}
	var walk func(*xhtml.Node) bool
	walk = func(n *xhtml.Node) bool {
		if protected[n] {
			return true
		}
		if n.Type == xhtml.TextNode {
			return true
		}
		if n.Type != xhtml.ElementNode {
			return false
		}
		if !approved[n] && !(len(n.Attr) == 0 && (n.Data == "div" || n.Data == "span")) {
			return false
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			if !walk(c) {
				return false
			}
		}
		return true
	}
	return walk(root)
}

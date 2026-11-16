package web

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"github.com/z-chenhao/assistant/internal/connectedapps"
	xhtml "golang.org/x/net/html"
	"golang.org/x/net/html/atom"
	"html/template"
	"strings"
)

// Fresh probes cannot be known by the pure compiler or appear as literal
// source. With no string conditionals/functions and closed flat fields, all
// allowed data flow is substitution;finite boolean cases cover every branch.
func preflightAppMarkup(m *appMarkup, cardProgram, sideProgram []string) error {
	seed := make([]byte, 16)
	if _, err := rand.Read(seed); err != nil {
		return err
	}
	nonce := hex.EncodeToString(seed)
	for flags := 0; flags < 8; flags++ {
		for img := 0; img < 2; img++ {
			for ref := 0; ref < 2; ref++ {
				d := appMarkupProbe(nonce, img != 0, ref != 0)
				d.ManageHidden = flags&1 != 0
				d.AddHidden = flags&2 != 0
				d.Unavailable = flags&4 != 0
				raw, err := renderAppMarkupAtoms(m.card, "app-card-atom-", cardProgram, d)
				if err != nil || !validAppMarkupDOM(string(raw), d, false, nonce) {
					return connectedapps.ErrUnavailable
				}
			}
		}
	}
	for pending := 0; pending < 2; pending++ {
		for img := 0; img < 2; img++ {
			d := appMarkupProbe(nonce, img != 0, false)
			d.Pending = pending != 0
			raw, err := renderAppMarkupAtoms(m.sidebar, "app-sidebar-atom-", sideProgram, d)
			if err != nil || !validAppMarkupDOM(string(raw), d, true, nonce) {
				return connectedapps.ErrUnavailable
			}
		}
	}
	return nil
}
func appMarkupProbe(n string, img, ref bool) appMarkupData {
	d := appMarkupData{Slug: "slug_" + n, URL: "/settings/apps?app=slug_" + n, Name: "name_" + n + `<&'"中文`, Description: "desc_" + n + `<&'"中文`, ManageLabel: "manage_" + n, AboutLabel: "about_" + n, ConnectLabel: "connect_" + n, PendingLabel: "pending_" + n}
	icon := `<span class="app-logo" data-probe-slot="` + n + `_icon"><span>initial_` + n + `</span>`
	if img {
		icon += `<img src="/apps/logo/` + n + `" alt="">`
	}
	d.Icon = template.HTML(icon + `</span>`)
	d.More = template.HTML(`<svg data-probe-slot="` + n + `_more"><circle cx="5"/></svg>`)
	d.Plus = template.HTML(`<svg data-probe-slot="` + n + `_plus"><path d="M1 2"/></svg>`)
	if ref {
		d.InterfaceReference = template.HTML(`<input type="hidden" name="interface_revision" value="graph_` + n + `" data-probe-slot="` + n + `_reference">`)
	}
	return d
}
func markupNodes(source string) ([]*xhtml.Node, error) {
	return xhtml.ParseFragment(strings.NewReader(source), &xhtml.Node{Type: xhtml.ElementNode, Data: "div", DataAtom: atom.Div})
}
func markupAttr(n *xhtml.Node, key string) string {
	for _, a := range n.Attr {
		if a.Key == key {
			return a.Val
		}
	}
	return ""
}
func markupHas(n *xhtml.Node, key string) bool {
	for _, a := range n.Attr {
		if a.Key == key {
			return true
		}
	}
	return false
}
func markupClass(n *xhtml.Node, class string) bool {
	for _, v := range strings.Fields(markupAttr(n, "class")) {
		if v == class {
			return true
		}
	}
	return false
}
func markupContains(parent, child *xhtml.Node) bool {
	for n := child; n != nil; n = n.Parent {
		if n == parent {
			return true
		}
	}
	return false
}
func markupAll(n *xhtml.Node, predicate func(*xhtml.Node) bool) []*xhtml.Node {
	var out []*xhtml.Node
	var walk func(*xhtml.Node)
	walk = func(n *xhtml.Node) {
		if n.Type == xhtml.ElementNode && predicate(n) {
			out = append(out, n)
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(n)
	return out
}
func markupOne(n *xhtml.Node, predicate func(*xhtml.Node) bool) *xhtml.Node {
	all := markupAll(n, predicate)
	if len(all) != 1 {
		return nil
	}
	return all[0]
}
func markupSerialize(n *xhtml.Node) string {
	var out bytes.Buffer
	_ = xhtml.Render(&out, n)
	return out.String()
}
func markupText(n *xhtml.Node, skip map[*xhtml.Node]bool) string {
	if skip[n] {
		return ""
	}
	if n.Type == xhtml.TextNode {
		return strings.TrimSpace(n.Data)
	}
	var out strings.Builder
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		out.WriteString(markupText(c, skip))
	}
	return out.String()
}
func markupUnhidden(n, root *xhtml.Node, ownHidden bool) bool {
	for p := n; p != nil; p = p.Parent {
		if markupHas(p, "hidden") && (p != n || !ownHidden) {
			return false
		}
		if markupHas(p, "aria-hidden") {
			return false
		}
		if p == root {
			break
		}
	}
	return markupHas(n, "hidden") == ownHidden
}

func validAppMarkupDOM(source string, d appMarkupData, sidebar bool, nonce string) bool {
	nodes, err := markupNodes(source)
	if err != nil {
		return false
	}
	var root *xhtml.Node
	for _, n := range nodes {
		if n.Type == xhtml.TextNode && strings.TrimSpace(n.Data) == "" {
			continue
		}
		if n.Type != xhtml.ElementNode || root != nil {
			return false
		}
		root = n
	}
	if root == nil {
		return false
	}
	slots := map[*xhtml.Node]bool{}
	controls := map[*xhtml.Node]bool{}
	slot := func(key string, value template.HTML) *xhtml.Node {
		if value == "" {
			if len(markupAll(root, func(n *xhtml.Node) bool { return markupAttr(n, "data-probe-slot") == nonce+"_"+key })) != 0 {
				return nil
			}
			return nil
		}
		node := markupOne(root, func(n *xhtml.Node) bool { return markupAttr(n, "data-probe-slot") == nonce+"_"+key })
		expected, e := markupNodes(string(value))
		if node == nil || e != nil || len(expected) != 1 || markupSerialize(node) != markupSerialize(expected[0]) {
			return nil
		}
		slots[node] = true
		return node
	}
	icon := slot("icon", d.Icon)
	if icon == nil {
		return false
	}
	allowedHidden := map[*xhtml.Node]bool{}
	if sidebar {
		if root.Data != "a" || markupAttr(root, "href") != d.URL || markupAttr(root, "data-app-open") != d.Slug || len(markupAll(root, func(n *xhtml.Node) bool { return n.Data == "a" })) != 1 {
			return false
		}
		pending := markupAll(root, func(n *xhtml.Node) bool { return markupClass(n, "app-pending-dot") })
		if len(pending) != map[bool]int{true: 1, false: 0}[d.Pending] {
			return false
		}
		for _, n := range pending {
			if n.Data != "span" || markupAttr(n, "aria-label") != d.PendingLabel || markupText(n, nil) != "" {
				return false
			}
		}
		if markupText(root, slots) != d.Name || !markupUnhidden(root, root, false) {
			return false
		}
	} else {
		if root.Data != "article" || !markupClass(root, "app-tile") || markupAttr(root, "data-app-tile") != d.Slug {
			return false
		}
		summary := markupOne(root, func(n *xhtml.Node) bool { return markupClass(n, "app-summary") })
		copy := markupOne(root, func(n *xhtml.Node) bool { return markupClass(n, "app-copy") })
		manage := markupOne(root, func(n *xhtml.Node) bool { return markupHas(n, "data-app-manage") })
		if summary == nil || copy == nil || manage == nil || (copy.Data != "span" && copy.Data != "div") || summary == copy || manage.Parent != root || summary.Data != "a" || manage.Data != "a" || !markupContains(summary, copy) || !markupContains(summary, icon) || markupAttr(summary, "href") != d.URL || markupAttr(summary, "data-app-open") != d.Slug || !markupUnhidden(summary, root, false) || !markupUnhidden(copy, root, false) {
			return false
		}
		copyText := markupText(copy, slots)
		if copyText != d.Name+d.Description && copyText != d.Description+d.Name {
			return false
		}
		if markupAttr(manage, "href") != d.URL || markupAttr(manage, "data-app-open") != d.Slug || markupAttr(manage, "aria-label") != d.ManageLabel+": "+d.Name || markupAttr(manage, "title") != d.ManageLabel || !markupUnhidden(manage, root, d.ManageHidden) || !markupClass(manage, "control-icon-button") {
			return false
		}
		allowedHidden[manage] = true
		controls[manage] = true
		more := slot("more", d.More)
		plus := slot("plus", d.Plus)
		if more == nil || plus == nil || !markupContains(manage, more) {
			return false
		}
		forms := markupAll(root, func(n *xhtml.Node) bool { return n.Data == "form" })
		adds := markupAll(root, func(n *xhtml.Node) bool { return markupHas(n, "data-app-add") })
		buttons := markupAll(root, func(n *xhtml.Node) bool { return n.Data == "button" })
		anchors := markupAll(root, func(n *xhtml.Node) bool { return n.Data == "a" })
		if d.Unavailable {
			if len(forms) != 0 || len(buttons) != 0 || len(adds) != 1 || len(anchors) != 3 {
				return false
			}
			add := adds[0]
			if add.Parent != root || add.Data != "a" || markupAttr(add, "href") != d.URL || markupAttr(add, "data-app-open") != d.Slug || markupAttr(add, "aria-label") != d.AboutLabel+": "+d.Name || !markupClass(add, "control-icon-button") || !markupContains(add, plus) || !markupUnhidden(add, root, d.AddHidden) {
				return false
			}
			allowedHidden[add] = true
			controls[add] = true
		} else {
			if len(forms) != 1 || len(buttons) != 1 || len(adds) != 0 || len(anchors) != 2 {
				return false
			}
			form := forms[0]
			button := buttons[0]
			if form.Parent != root || markupAttr(form, "method") != "post" || markupAttr(form, "action") != "/apps/configure" || !markupHas(form, "data-app-action") || !markupHas(form, "data-app-quick") || markupAttr(form, "data-app-toolkit") != d.Slug || !markupUnhidden(form, root, d.AddHidden) || !markupContains(form, button) || !markupContains(button, plus) || markupAttr(button, "type") != "submit" || markupAttr(button, "aria-label") != d.ConnectLabel+" "+d.Name || markupAttr(button, "title") != d.ConnectLabel || !markupClass(button, "control-icon-button") {
				return false
			}
			allowedHidden[form] = true
			controls[button] = true
			inputs := markupAll(root, func(n *xhtml.Node) bool { return n.Data == "input" })
			expected := map[string]string{"action": "connect", "toolkit": d.Slug}
			if d.InterfaceReference != "" {
				expected["interface_revision"] = "graph_" + nonce
				if slot("reference", d.InterfaceReference) == nil {
					return false
				}
			}
			if len(inputs) != len(expected) {
				return false
			}
			for _, input := range inputs {
				value, ok := expected[markupAttr(input, "name")]
				if !ok || markupAttr(input, "type") != "hidden" || markupAttr(input, "value") != value || !markupContains(form, input) {
					return false
				}
				delete(expected, markupAttr(input, "name"))
			}
		}
		if markupText(root, slots) != d.Name+d.Description && markupText(root, slots) != d.Description+d.Name {
			return false
		}
	}
	// Only the current roles and passive containers are available outside exact
	// trusted primitive slots. No arbitrary attributes,active content or CSS skin.
	var visit func(*xhtml.Node) bool
	visit = func(n *xhtml.Node) bool {
		if slots[n] {
			return true
		}
		if n.Type == xhtml.CommentNode {
			return false
		}
		if n.Type == xhtml.TextNode {
			return true
		}
		if n.Type != xhtml.ElementNode {
			return false
		}
		if !strings.Contains(" article a span div strong form input button ", " "+n.Data+" ") {
			return false
		}
		attrs := map[string]bool{}
		for _, a := range n.Attr {
			if a.Namespace != "" || attrs[a.Key] {
				return false
			}
			attrs[a.Key] = true
			if strings.HasPrefix(a.Key, "data-plugin-") {
				if strings.Contains(a.Val, nonce) {
					return false
				}
				continue
			}
			if !strings.Contains(" class href data-app-tile data-app-open data-app-manage data-app-add data-app-action data-app-toolkit data-app-quick aria-label title hidden method action type name value ", " "+a.Key+" ") {
				return false
			}
			if a.Key == "class" {
				for _, class := range strings.Fields(a.Val) {
					if !strings.Contains(" app-tile app-summary app-copy control-icon-button app-pending-dot ", " "+class+" ") {
						return false
					}
					if sidebar && class != "app-pending-dot" {
						return false
					}
				}
			}
			if a.Key == "hidden" && !allowedHidden[n] {
				return false
			}
			if a.Key == "data-app-open" && (n.Data != "a" || a.Val != d.Slug) {
				return false
			}
			if a.Key == "data-app-tile" && (sidebar || n != root || a.Val != d.Slug) {
				return false
			}
			if (a.Key == "data-app-action" || a.Key == "data-app-quick" || a.Key == "data-app-toolkit") && n.Data != "form" {
				return false
			}
			if a.Key == "class" {
				if markupClass(n, "app-tile") && n != root {
					return false
				}
				if markupClass(n, "control-icon-button") && !controls[n] {
					return false
				}
				if !sidebar && markupClass(n, "app-pending-dot") {
					return false
				}
			}

			if a.Key == "href" && a.Val != d.URL {
				return false
			}
			if a.Key == "title" && a.Val != d.Name && a.Val != d.ManageLabel && a.Val != d.ConnectLabel {
				return false
			}
			if a.Key == "aria-label" && n.Data != "a" && n.Data != "button" && !markupClass(n, "app-pending-dot") {
				return false
			}
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			if !visit(c) {
				return false
			}
		}
		return true
	}
	return visit(root)
}

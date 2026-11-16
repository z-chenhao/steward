package web

import (
	"crypto/rand"
	"encoding/hex"
	"html/template"
	"strings"

	"github.com/z-chenhao/assistant/internal/connectedapps"
	xhtml "golang.org/x/net/html"
)

func compileAppConnectMarkup(source string) (*template.Template, error) {
	t, err := parseAppConnectMarkup(source)
	if err != nil {
		return nil, err
	}
	var seed [16]byte
	if _, err := rand.Read(seed[:]); err != nil {
		return nil, err
	}
	n := hex.EncodeToString(seed[:])
	d := appConnectMarkupData{Slug: "toolkit_" + n, FirstLabel: "first_" + n, AddAccountLabel: "additional_" + n, ConnectLabel: "connect_" + n, AccountLabel: "alias_" + n, ConnectAccountLabel: "submit_" + n}
	d.Identity = template.HTML(`<input type="hidden" name="interface_revision" value="graph_` + n + `"><input type="hidden" name="action" value="connect"><input type="hidden" name="toolkit" value="` + d.Slug + `">`)
	d.Help = template.HTML(`<span data-probe="help_` + n + `">Original shared help</span>`)
	for _, hidden := range []bool{false, true} {
		d.FirstHidden, d.AdditionalHidden = hidden, hidden
		for _, part := range []string{"first", "additional", "unavailable"} {
			out, err := renderAppConnectMarkup(t, part, d)
			if err != nil || !validAppConnectMarkup(string(out), part, d) {
				return nil, connectedapps.ErrUnavailable
			}
		}
	}
	return t, nil
}

func validAppConnectMarkup(raw, part string, d appConnectMarkupData) bool {
	nodes, err := markupNodes(raw)
	if err != nil {
		return false
	}
	root := &xhtml.Node{Type: xhtml.ElementNode, Data: "div"}
	for _, n := range nodes {
		root.AppendChild(n)
	}
	protected := map[*xhtml.Node]bool{}
	slot := d.Identity
	if part == "unavailable" {
		slot = d.Help
	}
	expected, err := markupNodes(string(slot))
	if err != nil {
		return false
	}
	for _, n := range expected {
		if n.Type != xhtml.ElementNode {
			return false
		}
		canonical := markupSerialize(n)
		found := markupOne(root, func(n *xhtml.Node) bool { return markupSerialize(n) == canonical })
		if found == nil || protected[found] {
			return false
		}
		protected[found] = true
	}
	form := markupOne(root, func(n *xhtml.Node) bool { return n.Data == "form" })
	button := markupOne(root, func(n *xhtml.Node) bool { return n.Data == "button" })
	input := markupOne(root, func(n *xhtml.Node) bool { return n.Data == "input" && markupAttr(n, "name") == "alias" })
	details := markupOne(root, func(n *xhtml.Node) bool { return n.Data == "details" })
	summary := markupOne(root, func(n *xhtml.Node) bool { return n.Data == "summary" })
	if button == nil {
		return false
	}
	if part == "unavailable" {
		if form != nil || input != nil || details != nil || markupAttr(button, "type") != "button" || !markupHas(button, "disabled") || markupText(button, nil) != d.ConnectLabel {
			return false
		}
	} else {
		if form == nil || markupAttr(form, "method") != "post" || markupAttr(form, "action") != "/apps/configure" || !markupHas(form, "data-app-action") || markupAttr(form, "data-app-toolkit") != d.Slug || !markupContains(form, button) || markupAttr(button, "type") != "submit" || !allAccountSlotsWithin(protected, form) {
			return false
		}
		if part == "first" {
			if input != nil || details != nil || !markupHas(form, "data-app-first") || markupHas(form, "data-app-additional") || !markupUnhidden(form, root, d.FirstHidden) || markupText(button, nil) != d.FirstLabel {
				return false
			}
		} else if part == "additional" {
			if details == nil || summary == nil || input == nil || !markupHas(details, "data-app-add-account") || !markupClass(details, "app-add-account") || !markupContains(details, form) || summary.Parent != details || markupText(summary, nil) != d.AddAccountLabel || !markupUnhidden(details, root, d.AdditionalHidden) || markupHas(form, "hidden") || !markupHas(form, "data-app-additional") || markupHas(form, "data-app-first") || markupText(button, nil) != d.ConnectAccountLabel {
				return false
			}
			if !markupContains(form, input) || input.Parent == nil || input.Parent.Data != "label" || markupText(input.Parent, nil) != d.AccountLabel || markupAttr(input, "pattern") != "[A-Za-z0-9][A-Za-z0-9_-]{0,63}" || markupAttr(input, "maxlength") != "64" || !markupHas(input, "required") || (markupAttr(input, "type") != "" && markupAttr(input, "type") != "text") {
				return false
			}
			if value := markupAttr(input, "placeholder"); len(value) > 64 {
				return false
			}
		} else {
			return false
		}
	}
	var walk func(*xhtml.Node) bool
	walk = func(n *xhtml.Node) bool {
		if protected[n] {
			return true
		}
		if n.Type != xhtml.TextNode && n.Type != xhtml.ElementNode {
			return false
		}
		if n.Type == xhtml.ElementNode {
			if !strings.Contains(" div span section form button details summary label input ", " "+n.Data+" ") {
				return false
			}
			if n.Data == "form" && n != form || n.Data == "button" && n != button || n.Data == "input" && n != input || n.Data == "details" && n != details || n.Data == "summary" && n != summary {
				return false
			}
			for _, a := range n.Attr {
				if a.Namespace != "" {
					return false
				}
				switch a.Key {
				case "class":
					for _, c := range strings.Fields(a.Val) {
						if !strings.Contains(" app-unavailable-action app-add-account settings-actions secondary ", " "+c+" ") {
							return false
						}
					}
				case "hidden":
					if !(part == "first" && n == form && d.FirstHidden || part == "additional" && n == details && d.AdditionalHidden) {
						return false
					}
				case "method", "action", "data-app-action", "data-app-toolkit", "data-app-first", "data-app-additional":
					if n != form {
						return false
					}
				case "data-app-add-account":
					if n != details {
						return false
					}
				case "type":
					if n != button && n != input {
						return false
					}
				case "disabled":
					if part != "unavailable" || n != button {
						return false
					}
				case "name", "placeholder", "pattern", "maxlength", "required":
					if n != input {
						return false
					}
				default:
					return false
				}
			}
		}
		for child := n.FirstChild; child != nil; child = child.NextSibling {
			if !walk(child) {
				return false
			}
		}
		return true
	}
	if !walk(root) {
		return false
	}
	text := markupText(root, protected)
	labels := []string{d.FirstLabel}
	if part == "unavailable" {
		labels = []string{d.ConnectLabel}
	} else if part == "additional" {
		labels = []string{d.AddAccountLabel, d.AccountLabel, d.ConnectAccountLabel}
	}
	for _, label := range labels {
		if strings.Count(text, label) != 1 {
			return false
		}
	}
	return true
}

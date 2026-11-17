package web

import (
	"crypto/rand"
	"encoding/hex"
	"html/template"
	"strings"

	"github.com/z-chenhao/assistant/internal/connectedapps"
	xhtml "golang.org/x/net/html"
)

// The source has no calls, loops or string-dependent branches. Fresh literal
// probes therefore verify every permitted substitution without disclosing real
// account facts to the owning process. Identity/authorization slots are opaque.
func compileAppAccountMarkup(source string) (*template.Template, error) {
	t, err := parseAppAccountMarkup(source)
	if err != nil {
		return nil, err
	}
	var seed [16]byte
	if _, err := rand.Read(seed[:]); err != nil {
		return nil, err
	}
	n := hex.EncodeToString(seed[:])
	d := appAccountMarkupData{Name: "name_" + n, Alias: "alias_" + n, Status: "status_" + n, StatusLabel: "status_label_" + n, Confirm: "confirm_" + n, Description: "description_" + n, Action: "action_" + n, Label: "label_" + n}
	for _, populated := range []bool{false, true} {
		if populated {
			d.Warning = template.HTML(`<p data-probe="warning_` + n + `">Required disclosure</p>`)
			d.Authorization = template.HTML(`<div data-probe="authorization_` + n + `"><a href="https://connect.composio.dev/` + n + `">Authorize</a></div>`)
		}
		for _, part := range []string{"copy", "button", "actions"} {
			if part == "actions" {
				d.Identity = template.HTML(`<input type="hidden" name="connection" value="` + n + `"><input type="hidden" name="interface_revision" value="graph_` + n + `">`)
				d.Buttons = template.HTML(`<button name="action" value="check" data-probe="button_` + n + `">Check</button>`)
			}
			out, err := renderAppAccountMarkup(t, part, d)
			if err != nil || !validAppAccountMarkup(string(out), part, d) {
				return nil, connectedapps.ErrUnavailable
			}
		}
	}
	return t, nil
}

func validAppAccountMarkup(raw, part string, d appAccountMarkupData) bool {
	nodes, err := markupNodes(raw)
	if err != nil {
		return false
	}
	root := &xhtml.Node{Type: xhtml.ElementNode, Data: "div"}
	for _, node := range nodes {
		root.AppendChild(node)
	}
	protected := map[*xhtml.Node]bool{}
	slots := []template.HTML{}
	if part == "copy" {
		slots = []template.HTML{d.Warning, d.Authorization}
	} else if part == "actions" {
		slots = []template.HTML{d.Identity, d.Buttons}
	}
	for _, slot := range slots {
		expected, err := markupNodes(string(slot))
		if err != nil {
			return false
		}
		for _, node := range expected {
			if node.Type != xhtml.ElementNode {
				return false
			}
			canonical := markupSerialize(node)
			found := markupOne(root, func(n *xhtml.Node) bool { return markupSerialize(n) == canonical })
			if found == nil || protected[found] {
				return false
			}
			protected[found] = true
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
			if n.Data == "form" && part != "actions" || n.Data == "button" && part != "button" {
				return false
			}
			if !strings.Contains(" div span strong p small section form button ", " "+n.Data+" ") {
				return false
			}
			for _, a := range n.Attr {
				if a.Namespace != "" {
					return false
				}
				switch a.Key {
				case "class":
					for _, c := range strings.Fields(a.Val) {
						if !strings.Contains(" setting-copy app-account-actions secondary settings-actions field-help ", " "+c+" ") {
							return false
						}
					}
				case "data-connection-status":
					if part != "copy" || a.Val != d.Status {
						return false
					}
				case "method", "action", "data-app-action", "data-settings-confirm", "data-confirm-description":
					if part != "actions" || n.Data != "form" {
						return false
					}
				case "name", "value":
					if part != "button" || n.Data != "button" {
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
	count := func(value string) bool { return strings.Count(text, value) == 1 }
	switch part {
	case "copy":
		status := markupOne(root, func(n *xhtml.Node) bool { return markupHas(n, "data-connection-status") })
		return count(d.Name) && count(d.Alias) && count(d.StatusLabel) && status != nil && markupText(status, nil) == d.StatusLabel
	case "actions":
		form := markupOne(root, func(n *xhtml.Node) bool { return n.Data == "form" })
		return form != nil && markupAttr(form, "method") == "post" && markupAttr(form, "action") == "/apps/configure" && markupHas(form, "data-app-action") && markupAttr(form, "data-settings-confirm") == d.Confirm && markupAttr(form, "data-confirm-description") == d.Description && allAccountSlotsWithin(protected, form)
	case "button":
		button := markupOne(root, func(n *xhtml.Node) bool { return n.Data == "button" })
		return button != nil && markupAttr(button, "name") == "action" && markupAttr(button, "value") == d.Action && markupText(button, nil) == d.Label && count(d.Label)
	}
	return false
}

func allAccountSlotsWithin(slots map[*xhtml.Node]bool, parent *xhtml.Node) bool {
	for node := range slots {
		if !markupContains(parent, node) {
			return false
		}
	}
	return true
}

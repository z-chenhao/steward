package web

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"html/template"
	"strconv"
	"strings"

	"github.com/z-chenhao/assistant/internal/connectedapps"
	"github.com/z-chenhao/assistant/internal/plugin/appsinterface/contract"
	xhtml "golang.org/x/net/html"
)

func compileAppSettingsMarkup(source string) (*template.Template, error) {
	t, err := parseAppSettingsMarkup(source)
	if err != nil {
		return nil, err
	}
	var seed [16]byte
	if _, err = rand.Read(seed[:]); err != nil {
		return nil, err
	}
	n := hex.EncodeToString(seed[:])
	d := appSettingsMarkupData{SettingsCopy: contract.SettingsCopy{KeyRefLabel: "key_" + n, AppLabel: "app_" + n, ConfigLabel: "config_" + n, RemoveLabel: "remove_" + n, AddLabel: "add_" + n, SaveLabel: "save_" + n}, ProjectTitle: "project_" + n, AuthenticationTitle: "auth_" + n, KeyRef: "env:key_" + n, App: "app_value_" + n, Config: "config_value_" + n, Index: 37}
	d.Identity = template.HTML(`<input type="hidden" name="interface_revision" value="graph_` + n + `"><input type="hidden" name="revision" value="71">`)
	d.ProjectHelp = template.HTML(`<span data-probe="project_` + n + `">Original project help</span>`)
	d.KeyHelp = template.HTML(`<span data-probe="key_` + n + `">Original key reference help</span>`)
	d.Rows = template.HTML(`<fieldset data-probe="rows_` + n + `">Original projected rows</fieldset>`)
	for _, open := range []bool{false, true} {
		d.AdvancedOpen = open
		for _, part := range []string{"project", "row"} {
			var out bytes.Buffer
			if err := t.ExecuteTemplate(&out, "app-settings-view-"+part, d); err != nil || !validAppSettingsMarkup(out.String(), part, d) {
				return nil, connectedapps.ErrUnavailable
			}
		}
	}
	return t, nil
}

func validAppSettingsMarkup(raw, part string, d appSettingsMarkupData) bool {
	nodes, err := markupNodes(raw)
	if err != nil {
		return false
	}
	root := &xhtml.Node{Type: xhtml.ElementNode, Data: "div"}
	for _, n := range nodes {
		root.AppendChild(n)
	}
	protected := map[*xhtml.Node]bool{}
	var rows, projectHelp, keyHelp *xhtml.Node
	identities := map[*xhtml.Node]bool{}
	if part == "project" {
		for i, slot := range []template.HTML{d.Identity, d.ProjectHelp, d.KeyHelp, d.Rows} {
			expected, err := markupNodes(string(slot))
			if err != nil || len(expected) == 0 {
				return false
			}
			for _, node := range expected {
				canonical := markupSerialize(node)
				found := markupOne(root, func(n *xhtml.Node) bool { return n.Type == xhtml.ElementNode && markupSerialize(n) == canonical })
				if found == nil || protected[found] {
					return false
				}
				protected[found] = true
				if i == 0 {
					identities[found] = true
				}
				if i == 1 {
					projectHelp = found
				}
				if i == 2 {
					keyHelp = found
				}
				if i == 3 {
					rows = found
				}
			}
		}
	}
	// Every original control is unique and retains its complete functional
	// attributes. Ordinary wrappers and row order remain source presentation.
	input := func(name, value string, attrs map[string]string) *xhtml.Node {
		n := markupOne(root, func(n *xhtml.Node) bool { return !protected[n] && n.Data == "input" && markupAttr(n, "name") == name })
		if n == nil {
			return nil
		}
		want := map[string]string{"name": name, "value": value}
		for k, v := range attrs {
			want[k] = v
		}
		if !appSettingsAttributes(n, want) {
			return nil
		}
		return n
	}
	button := func(value, label string, save bool) *xhtml.Node {
		n := markupOne(root, func(n *xhtml.Node) bool { return n.Data == "button" && markupAttr(n, "value") == value })
		want := map[string]string{"class": "secondary setting-action"}
		if save {
			want["type"] = "submit"
		} else {
			want["name"] = "action"
			want["value"] = value
			want["formnovalidate"] = ""
		}
		if n == nil || !appSettingsAttributes(n, want) || markupText(n, nil) != label {
			return nil
		}
		return n
	}
	controls := map[*xhtml.Node]bool{}
	var form, details, fieldset *xhtml.Node
	if part == "project" {
		form = markupOne(root, func(n *xhtml.Node) bool { return n.Data == "form" })
		details = markupOne(root, func(n *xhtml.Node) bool { return n.Data == "details" })
		summary := markupOne(root, func(n *xhtml.Node) bool { return n.Data == "summary" })
		if form == nil || !appSettingsAttributes(form, map[string]string{"method": "post", "action": "/apps/service/configure"}) || len(identities) == 0 || rows == nil || !allAccountSlotsWithin(identities, form) || !markupContains(form, rows) || details == nil || summary == nil || summary.Parent != details || !markupContains(details, rows) || !markupContains(form, details) || markupText(summary, nil) != d.AuthenticationTitle {
			return false
		}
		want := map[string]string{"class": "service-advanced"}
		if d.AdvancedOpen {
			want["open"] = ""
		}
		if !appSettingsAttributes(details, want) {
			return false
		}
		key := input("key_ref", d.KeyRef, map[string]string{"id": "composio-key-ref", "placeholder": "env:COMPOSIO_API_KEY", "required": "", "autocomplete": "off", "aria-label": d.KeyRefLabel})
		editor := input("editor", "fields", map[string]string{"type": "hidden"})
		add, save := button("add-override", d.AddLabel, false), button("", d.SaveLabel, true)
		if key == nil || editor == nil || add == nil || save == nil {
			return false
		}
		for _, n := range []*xhtml.Node{key, editor, add, save} {
			if !markupContains(form, n) {
				return false
			}
			controls[n] = true
		}
		if !markupContains(details, add) {
			return false
		}
		label := markupOne(root, func(n *xhtml.Node) bool { return n.Data == "label" && markupAttr(n, "for") == "composio-key-ref" })
		if label == nil || keyHelp == nil || !markupContains(label, keyHelp) || markupText(label, protected) != d.KeyRefLabel {
			return false
		}
		heading := markupOne(root, func(n *xhtml.Node) bool { return n.Data == "h2" })
		if heading == nil || projectHelp == nil || !markupContains(heading, projectHelp) || markupText(heading, protected) != d.ProjectTitle {
			return false
		}
	} else if part == "row" {
		fieldset = markupOne(root, func(n *xhtml.Node) bool { return n.Data == "fieldset" })
		if fieldset == nil {
			return false
		}
		app := input("app_slug", d.App, map[string]string{"maxlength": "128", "spellcheck": "false", "autocomplete": "off", "placeholder": "gmail"})
		config := input("auth_config", d.Config, map[string]string{"maxlength": "128", "spellcheck": "false", "autocomplete": "off"})
		remove := button("remove-override-"+strconv.Itoa(d.Index), d.RemoveLabel, false)
		if app == nil || config == nil || remove == nil {
			return false
		}
		for _, n := range []*xhtml.Node{app, config, remove} {
			if !markupContains(fieldset, n) {
				return false
			}
			controls[n] = true
		}
		if app.Parent.Data != "label" || markupText(app.Parent, nil) != d.AppLabel || config.Parent.Data != "label" || markupText(config.Parent, nil) != d.ConfigLabel {
			return false
		}
		legend := markupOne(fieldset, func(n *xhtml.Node) bool { return n.Data == "legend" })
		if legend == nil || !markupClass(legend, "sr-only") || markupText(legend, nil) != d.AuthenticationTitle {
			return false
		}
	} else {
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
		if !strings.Contains(" div span section h2 form label input details summary fieldset legend button ", " "+n.Data+" ") {
			return false
		}
		if n.Data == "input" || n.Data == "button" {
			if !controls[n] {
				return false
			}
		} else {
			if n.Data == "form" && n != form || n.Data == "details" && n != details || n.Data == "fieldset" && n != fieldset {
				return false
			}
			for _, a := range n.Attr {
				if a.Namespace != "" {
					return false
				}
				switch a.Key {
				case "class":
					for _, c := range strings.Fields(a.Val) {
						if !strings.Contains(" service-configuration settings-section-title setting-row control-field-label setting-controls service-advanced settings-actions sr-only ", " "+c+" ") {
							return false
						}
					}
				case "method", "action":
					if n != form {
						return false
					}
				case "open":
					if n != details || !d.AdvancedOpen {
						return false
					}
				case "for":
					if n.Data != "label" || a.Val != "composio-key-ref" {
						return false
					}
				case "aria-label":
					if n.Data != "h2" || a.Val != d.ProjectTitle {
						return false
					}
				default:
					return false
				}
			}
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
func appSettingsAttributes(n *xhtml.Node, want map[string]string) bool {
	if len(n.Attr) != len(want) {
		return false
	}
	seen := map[string]bool{}
	for _, a := range n.Attr {
		v, ok := want[a.Key]
		if a.Namespace != "" || !ok || a.Val != v || seen[a.Key] {
			return false
		}
		seen[a.Key] = true
	}
	return true
}

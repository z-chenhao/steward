package web

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"html/template"
	"strconv"
	"strings"

	"github.com/z-chenhao/assistant/internal/connectedapps"
	xhtml "golang.org/x/net/html"
)

func compileAppPagesMarkup(source string) (*template.Template, error) {
	t, err := parseAppPagesMarkup(source)
	if err != nil {
		return nil, err
	}
	var seed [16]byte
	if _, err = rand.Read(seed[:]); err != nil {
		return nil, err
	}
	n := hex.EncodeToString(seed[:])
	value := func(name string) string { return name + ` <&" ` + n }
	slot := func(name string) template.HTML {
		return template.HTML(`<span data-page-probe="` + name + n + `">Original child</span>`)
	}
	d := appPagesMarkupData{AppsTitle: value("title"), AppSettings: value("settings"), SearchApps: value("searchlabel"), Search: value("searchbutton"), RefreshApps: value("refresh"), Category: value("category"), SearchValue: value("query"), DirectoryUnavailable: value("unavailable"), BackToApps: value("back"), Close: value("close"), Name: value("name"), Description: value("description"), YourConnections: value("connections"), NoConnections: value("empty"), ServiceTitle: value("service"), Revision: "revision_" + n, InterfaceMode: "mode_" + n, InterfaceRevision: value("graph"), InterfaceProfile: value("profile"), ConnectedApps: value("connected"), Slug: value("slug"), Title: value("group"), URL: "/settings/apps?collection=" + n, ViewAll: value("all"), NoApps: value("noapps"), ID: value("account"), Toolkit: value("toolkit"), Status: "status_" + n, Body: slot("body"), Entries: slot("entries"), Groups: slot("groups"), Cards: slot("cards"), SettingsIcon: slot("settingsicon"), Inventory: slot("inventory"), Detail: slot("detail"), Directory: slot("directory"), Icon: slot("icon"), Accounts: slot("accounts")}
	// All actual conditions in a definition vary independently. No candidate can
	// know the fresh display/child identities used by this Host validation.
	for flags := 0; flags < 16; flags++ {
		d.Selected = flags&1 != 0
		d.Localized = flags&1 != 0
		d.ManagedPresent = flags&1 != 0
		d.GroupsPresent = flags&1 != 0
		d.Unavailable = flags&1 != 0
		d.SearchVisible = flags&1 != 0
		d.EmptyHidden = flags&2 != 0
		d.HasInterface = flags&2 != 0
		d.HasURL = flags&2 != 0
		d.Public = flags&2 != 0
		d.NoAuth = flags&4 != 0
		d.Suggested = flags&8 != 0
		for _, part := range []string{"page", "service", "sidebar", "detail", "accounts", "account-row", "groups", "group", "page-heading", "page-unavailable", "page-feedback", "page-inventory", "page-body", "page-dialog", "detail-heading", "detail-feedback", "detail-accounts", "service-heading", "service-back"} {
			var out bytes.Buffer
			if err := t.ExecuteTemplate(&out, "app-page-view-"+part, d); err != nil || !validAppPagesMarkup(out.String(), part, d) {
				return nil, connectedapps.ErrUnavailable
			}
		}
	}
	return t, nil
}

// Only original control attributes and opaque child identities are authoritative.
// Passive source owns ordinary wrappers/order and graphic geometry.
func validAppPagesMarkup(raw, part string, d appPagesMarkupData) bool {
	if (part == "detail" || part == "detail-heading" || part == "detail-feedback" || part == "detail-accounts") && !d.Selected || part == "sidebar" && !d.ManagedPresent || part == "page-unavailable" && !d.Unavailable {
		return strings.TrimSpace(raw) == ""
	}
	nodes, err := markupNodes(raw)
	if err != nil {
		return false
	}
	root := &xhtml.Node{Type: xhtml.ElementNode, Data: "div"}
	for _, n := range nodes {
		root.AppendChild(n)
	}
	approved := map[*xhtml.Node]bool{root: true}
	protected := map[*xhtml.Node]bool{}
	valid := true
	node := func(tag string, attrs map[string]string, text string) *xhtml.Node {
		n := markupOne(root, func(n *xhtml.Node) bool {
			return n.Type == xhtml.ElementNode && n.Data == tag && appSettingsAttributes(n, attrs)
		})
		if n == nil || approved[n] || text != "" && markupText(n, nil) != text {
			valid = false
			return nil
		}
		approved[n] = true
		return n
	}
	within := func(parent, child *xhtml.Node) {
		if parent == nil || child == nil || !markupContains(parent, child) {
			valid = false
		}
	}
	child := func(source template.HTML, parent *xhtml.Node) {
		expected, e := markupNodes(string(source))
		if e != nil || len(expected) != 1 {
			valid = false
			return
		}
		canonical := markupSerialize(expected[0])
		found := markupOne(root, func(n *xhtml.Node) bool { return n.Type == xhtml.ElementNode && markupSerialize(n) == canonical })
		if found == nil || protected[found] {
			valid = false
			return
		}
		protected[found] = true
		within(parent, found)
	}
	link := func(href, label, classes string) *xhtml.Node {
		return node("a", map[string]string{"href": href, "class": classes}, label)
	}
	switch part {
	case "page":
		a := map[string]string{"class": "workspace-page settings-page apps-page", "data-apps": "", "data-app-revision": d.Revision, "data-app-interface-mode": d.InterfaceMode, "data-app-interface-revision": d.InterfaceRevision, "data-app-interface-profile": d.InterfaceProfile}
		if d.Localized {
			a["data-app-interface-localized"] = "true"
		}
		child(d.Body, node("section", a, ""))
	case "service":
		child(d.Body, node("section", map[string]string{"class": "workspace-page settings-page"}, ""))
	case "sidebar":
		node("p", map[string]string{"class": "settings-nav-heading apps-sidebar-heading"}, d.ConnectedApps)
		child(d.Entries, node("nav", map[string]string{"class": "section-navigation apps-connections", "aria-label": d.ConnectedApps}, ""))
	case "detail":
		a := map[string]string{"class": "app-detail", "data-app-detail": d.Slug}
		if d.HasInterface {
			a["data-app-no-auth"] = strconv.FormatBool(d.NoAuth)
			a["data-app-suggested"] = strconv.FormatBool(d.Suggested)
		}
		child(d.Body, node("div", a, ""))
	case "accounts":
		child(d.Accounts, root)
	case "account-row":
		child(d.Body, node("div", map[string]string{"class": "app-account", "data-app-account": d.ID, "data-app-slug": d.Toolkit, "data-app-state": d.Status, "data-app-public": strconv.FormatBool(d.Public)}, ""))
	case "groups":
		if d.GroupsPresent {
			child(d.Groups, root)
		} else {
			node("p", map[string]string{"class": "apps-empty"}, d.NoApps)
		}
	case "group":
		section := node("section", map[string]string{"class": "apps-group"}, "")
		heading := node("div", map[string]string{"class": "apps-group-heading"}, "")
		within(section, heading)
		within(heading, node("h2", map[string]string{}, d.Title))
		if d.HasURL {
			a := node("a", map[string]string{"class": "text-link", "href": d.URL, "aria-label": d.ViewAll + ": " + d.Title}, d.ViewAll)
			within(heading, a)
		}
		grid := node("div", map[string]string{"class": "apps-grid"}, "")
		within(section, grid)
		child(d.Cards, grid)
	case "page-heading":
		heading := node("div", map[string]string{"class": "page-heading apps-heading"}, "")
		within(heading, node("h1", map[string]string{}, d.AppsTitle))
		toolbar := node("div", map[string]string{"class": "apps-toolbar"}, "")
		within(heading, toolbar)
		settings := node("a", map[string]string{"class": "control-icon-button", "href": "/settings/apps/service", "aria-label": d.AppSettings, "title": d.AppSettings}, "")
		within(toolbar, settings)
		child(d.SettingsIcon, settings)
		refresh := node("a", map[string]string{"class": "control-icon-button", "href": "/settings/apps", "aria-label": d.RefreshApps, "title": d.RefreshApps}, "")
		within(toolbar, refresh)
		if d.SearchVisible {
			form := node("form", map[string]string{"method": "get", "action": "/settings/apps", "class": "apps-search control-search", "role": "search"}, "")
			within(toolbar, form)
			within(form, node("input", map[string]string{"type": "hidden", "name": "view", "value": "all"}, ""))
			within(form, node("input", map[string]string{"type": "hidden", "name": "category", "value": d.Category}, ""))
			within(form, node("label", map[string]string{"for": "app-search", "class": "sr-only"}, d.SearchApps))
			within(form, node("input", map[string]string{"type": "search", "id": "app-search", "name": "search", "value": d.SearchValue, "maxlength": "200", "placeholder": d.SearchApps}, ""))
			within(form, node("button", map[string]string{"class": "sr-only", "type": "submit"}, d.Search))
		}
	case "page-unavailable":
		node("p", map[string]string{"class": "apps-notice", "role": "alert"}, d.DirectoryUnavailable)
	case "page-feedback":
		node("p", map[string]string{"class": "feedback", "role": "status", "aria-live": "polite", "data-app-feedback": "", "hidden": ""}, "")
	case "page-inventory":
		child(d.Inventory, node("div", map[string]string{"id": "app-accounts", "hidden": ""}, ""))
	case "page-body":
		if d.Selected {
			link("/settings/apps", d.BackToApps, "back-link")
			child(d.Detail, node("section", map[string]string{"class": "app-detail-page"}, ""))
		} else {
			child(d.Directory, root)
		}
	case "page-dialog":
		dialog := node("dialog", map[string]string{"class": "control-dialog apps-dialog", "data-app-dialog": "", "aria-labelledby": "app-dialog-title"}, "")
		within(dialog, node("button", map[string]string{"type": "button", "class": "control-icon-button apps-dialog-close", "data-app-close": "", "aria-label": d.Close}, ""))
		within(dialog, node("div", map[string]string{"data-app-dialog-body": ""}, ""))
	case "detail-heading":
		header := node("header", map[string]string{}, "")
		child(d.Icon, header)
		within(header, node("h2", map[string]string{}, d.Name))
		within(header, node("p", map[string]string{}, d.Description))
	case "detail-feedback":
		node("p", map[string]string{"class": "feedback", "data-app-detail-feedback": "", "role": "status", "aria-live": "polite", "hidden": ""}, "")
	case "detail-accounts":
		node("h3", map[string]string{}, d.YourConnections)
		child(d.Accounts, node("div", map[string]string{"data-app-detail-accounts": ""}, ""))
		a := map[string]string{"class": "muted", "data-app-no-accounts": ""}
		if d.EmptyHidden {
			a["hidden"] = ""
		}
		node("p", a, d.NoConnections)
	case "service-heading":
		within(node("div", map[string]string{"class": "page-heading"}, ""), node("h1", map[string]string{}, d.ServiceTitle))
	case "service-back":
		link("/settings/apps", d.BackToApps, "back-link")
	default:
		return false
	}
	if !valid {
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
		if !approved[n] {
			if n.Data == "svg" {
				// Reuse the existing passive symbol guard at the original compact sizes.
				w, e := strconv.Atoi(markupAttr(n, "width"))
				h, e2 := strconv.Atoi(markupAttr(n, "height"))
				if e != nil || e2 != nil || w < 12 || w > 20 || h != w {
					return false
				}
				clone := graphicRoot(markupSerialize(n))
				if clone == nil {
					return false
				}
				for i := range clone.Attr {
					if clone.Attr[i].Key == "width" || clone.Attr[i].Key == "height" {
						clone.Attr[i].Val = "20"
					}
				}
				return validAppSymbol(markupSerialize(clone)) && (part == "group" || part == "page-heading" || part == "page-dialog")
			}
			if len(n.Attr) != 0 || (n.Data != "div" && n.Data != "span") {
				return false
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

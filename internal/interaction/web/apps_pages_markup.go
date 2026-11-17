package web

import (
	"bytes"
	"encoding/json"
	"github.com/z-chenhao/assistant/internal/connectedapps"
	"github.com/z-chenhao/assistant/internal/plugin/appsinterface/contract"
	"github.com/z-chenhao/assistant/pkg/rsiplugin"
	"html/template"
	"strconv"
)

// appPagesMarkupData contains display values and already-authorized child
// fragments only. It cannot reach an Apps State, account object or effect API.
// The complete page path is developed together before activating markup /7.
type appPagesMarkupData struct {
	AppsTitle, AppSettings, SearchApps, Search, RefreshApps                           string
	Category, SearchValue, DirectoryUnavailable, BackToApps                           string
	Close, Name, Description, YourConnections, NoConnections, ServiceTitle            string
	Selected, SearchVisible, Unavailable, EmptyHidden                                 bool
	Localized, ManagedPresent, HasInterface, GroupsPresent, HasURL, NoAuth, Suggested bool
	Revision, InterfaceMode, InterfaceRevision, InterfaceProfile, ConnectedApps       string
	Slug, Title, URL, ViewAll, NoApps, ID, Toolkit, Status                            string
	Public                                                                            bool
	Body, Entries, Groups, Cards                                                      template.HTML
	SettingsIcon, Inventory, Detail, Directory, Icon, Accounts                        template.HTML
}

// parseAppPagesMarkup checks syntax only. Current-version activation must also
// validate the resulting DOM and all original control/identity slots.
func parseAppPagesMarkup(source string) (*template.Template, error) {
	return parsePassiveAppAtomsLimit(source, "app-page-view-", map[string]string{
		"page-heading":     "AppsTitle AppSettings SettingsIcon SearchVisible Category SearchApps SearchValue Search RefreshApps",
		"page-unavailable": "Unavailable DirectoryUnavailable",
		"page-feedback":    "",
		"page-inventory":   "Inventory",
		"page-body":        "Selected BackToApps Detail Directory",
		"page-dialog":      "Close",
		"detail-heading":   "Selected Icon Name Description",
		"detail-feedback":  "Selected",
		"detail-accounts":  "Selected YourConnections Accounts EmptyHidden NoConnections",
		"service-heading":  "ServiceTitle",
		"service-back":     "BackToApps",
		"page":             "Revision InterfaceMode InterfaceRevision InterfaceProfile Localized Body",
		"service":          "Body",
		"sidebar":          "ManagedPresent ConnectedApps Entries",
		"detail":           "Selected Slug HasInterface NoAuth Suggested Body",
		"accounts":         "Accounts",
		"account-row":      "ID Toolkit Status Public Body",
		"groups":           "GroupsPresent Groups NoApps",
		"group":            "Title HasURL URL ViewAll Cards",
	}, 8192)
}

// renderAppPagesPart binds existing authorized children, not arbitrary template
// functions. Unused children are never evaluated or fetched.
func renderAppPagesPart(host, source *template.Template, p page, part, language string) (template.HTML, error) {
	v := p.Apps
	if v == nil || source == nil {
		return "", connectedapps.ErrUnavailable
	}
	copy := v.Copy()
	text := appCopyText(language)
	d := appPagesMarkupData{
		AppsTitle: text(v, copy.AppsTitle), AppSettings: text(v, copy.AppSettings),
		SearchApps: text(v, copy.SearchApps), Search: text(v, copy.Search), RefreshApps: text(v, copy.RefreshApps),
		Category: v.Category, SearchValue: v.Search, DirectoryUnavailable: text(v, copy.DirectoryUnavailable),
		BackToApps: text(v, copy.BackToApps), Close: translate(language, "Close"),
		YourConnections: text(v, copy.YourConnections), NoConnections: text(v, copy.NoConnections), ServiceTitle: text(v, copy.ServiceTitle),
		Selected: v.Selected != nil, SearchVisible: v.Selected == nil && !v.ConnectedOnly, Unavailable: v.Unavailable,
	}
	slot := func(name string, value any) (template.HTML, error) {
		var out bytes.Buffer
		err := host.ExecuteTemplate(&out, name, value)
		return template.HTML(out.String()), err
	}
	var err error
	switch part {
	case "page", "service", "detail":
		section := part
		if part == "page" {
			d.Revision = strconv.FormatInt(v.State.Revision, 10)
			d.InterfaceMode = v.InterfaceMode()
			d.InterfaceRevision = v.InterfaceRevision()
			d.InterfaceProfile = v.InterfaceJSON()
			d.Localized = v.InterfaceLocalized()
		}
		if part == "detail" {
			if v.Selected == nil {
				break
			}
			d.Slug = v.Selected.Slug
			d.NoAuth = v.Selected.NoAuth
			d.Suggested = v.Suggested
			d.HasInterface = v.InterfaceRevision() != ""
		}
		var children bytes.Buffer
		for _, child := range appParts(v, section) {
			fragment, e := slot("app-"+section+"-"+child, p)
			if e != nil {
				return "", e
			}
			children.WriteString(string(fragment))
		}
		d.Body = template.HTML(children.String())
	case "sidebar":
		d.ManagedPresent = len(v.Managed) > 0
		d.ConnectedApps = text(v, copy.ConnectedApps)
		var entries bytes.Buffer
		for _, c := range v.Managed {
			fragment, e := renderAppSidebarEntry(host, v, c, language)
			if e != nil {
				return "", e
			}
			entries.WriteString(string(fragment))
		}
		d.Entries = template.HTML(entries.String())
	case "accounts":
		var entries bytes.Buffer
		for _, row := range appAccountViews(v) {
			fragment, e := slot("app-account-sections", appAccountContext(v, row))
			if e != nil {
				return "", e
			}
			entry := d
			entry.ID = row.ID
			entry.Toolkit = row.Toolkit
			entry.Status = string(row.Status)
			entry.Public = row.Public
			entry.Body = fragment
			if e = source.ExecuteTemplate(&entries, "app-page-view-account-row", entry); e != nil {
				return "", e
			}
		}
		d.Accounts = template.HTML(entries.String())
	case "groups":
		d.GroupsPresent = len(v.Groups) > 0
		d.NoApps = text(v, copy.NoApps)
		var groups bytes.Buffer
		for _, g := range v.Groups {
			entry := d
			entry.Title = translate(language, g.Title)
			entry.URL = g.URL
			entry.HasURL = g.URL != ""
			entry.ViewAll = text(v, copy.ViewAll)
			var cards bytes.Buffer
			for _, c := range g.Items {
				fragment, e := renderAppCard(host, v, c, language)
				if e != nil {
					return "", e
				}
				cards.WriteString(string(fragment))
			}
			entry.Cards = template.HTML(cards.String())
			if e := source.ExecuteTemplate(&groups, "app-page-view-group", entry); e != nil {
				return "", e
			}
		}
		d.Groups = template.HTML(groups.String())
	case "page-heading":
		d.SettingsIcon, err = slot("icon-settings", p)
	case "page-inventory":
		d.Inventory, err = slot("app-accounts", v.Inventory())
	case "page-body":
		if d.Selected {
			d.Detail, err = slot("app-detail", p)
		} else {
			d.Directory, err = slot("app-directory-sections", p)
		}
	case "detail-heading":
		if c := v.Selected; c != nil {
			d.Name = c.Name
			d.Description = translate(language, c.Description)
			d.Icon, err = appPrimitive(host, "app-icon", *c, v)
		}
	case "detail-accounts":
		if c := v.Selected; c != nil {
			d.EmptyHidden = appCardUI(v, *c).EmptyHidden
			d.Accounts, err = slot("app-accounts", p)
		}
	case "page-unavailable", "page-feedback", "page-dialog", "detail-feedback", "service-heading", "service-back":
	default:
		return "", connectedapps.ErrUnavailable
	}
	if err != nil {
		return "", err
	}
	var out bytes.Buffer
	if err := source.ExecuteTemplate(&out, "app-page-view-"+part, d); err != nil {
		return "", err
	}
	return template.HTML(out.String()), nil
}

func UnavailableAppPagesMarkup(p AppInterfacePolicy) AppInterfacePolicy {
	p = UnavailableAppDirectoryMarkup(p)
	p.pagesManaged = true
	p.pagesMarkup = nil
	return p
}

func ManagedAppPagesMarkup(p AppInterfacePolicy, m contract.Markup) (AppInterfacePolicy, error) {
	if len(m.Source) > 16384 {
		return AppInterfacePolicy{}, connectedapps.ErrUnavailable
	}
	var source contract.PageMarkupSource
	if rsiplugin.Decode([]byte(m.Source), &source) != nil {
		return AppInterfacePolicy{}, connectedapps.ErrUnavailable
	}
	pages, err := compileAppPagesMarkup(source.Pages)
	if err != nil {
		return AppInterfacePolicy{}, err
	}
	raw, err := json.Marshal(source.DirectoryMarkupSource)
	if err != nil {
		return AppInterfacePolicy{}, err
	}
	m.Source = string(raw)
	p, err = ManagedAppDirectoryMarkup(p, m)
	if err != nil {
		return AppInterfacePolicy{}, err
	}
	p.pagesManaged = true
	p.pagesMarkup = pages
	return p, nil
}

func appPagePart(host *template.Template, p page, part, language string) (template.HTML, error) {
	if p.Apps == nil {
		return "", connectedapps.ErrUnavailable
	}
	policy := p.Apps.Interface
	if policy == nil || !policy.pagesManaged {
		name := "app-" + part
		switch part {
		case "page":
			name = "apps-page"
		case "service":
			name = "app-service-page"
		case "groups":
			name = "app-directory-groups"
		}
		var out bytes.Buffer
		err := host.ExecuteTemplate(&out, name+"-legacy-v6", p)
		return template.HTML(out.String()), err
	}
	if !policy.available() || policy.pagesMarkup == nil {
		return "", connectedapps.ErrUnavailable
	}
	return renderAppPagesPart(host, policy.pagesMarkup, p, part, language)
}

package web

import (
	"bytes"
	"encoding/json"
	"github.com/z-chenhao/assistant/internal/connectedapps"
	"github.com/z-chenhao/assistant/internal/plugin/appsinterface/contract"
	"github.com/z-chenhao/assistant/pkg/rsiplugin"
	"html/template"

	"github.com/z-chenhao/assistant/internal/connector/composio"
)

// Only original display/query facts are visible to passive source atoms.
// Category iteration stays over the existing typed projection.
type appDirectoryMarkupData struct {
	AppViews, Discover, YourApps, AllApps, BackToApps                       string
	CategoryLabel, AllCategories, Filter, MoreApps                          string
	Search, Category, NextCursor, OptionID, OptionName                      string
	DiscoverCurrent, ConnectedCurrent, DirectoryCurrent                     bool
	CollectionPresent, CategoriesPresent, PaginationPresent, OptionSelected bool
	Options                                                                 template.HTML
}

func parseAppDirectoryMarkup(source string) (*template.Template, error) {
	return parsePassiveAppAtoms(source, "app-directory-view-", map[string]string{
		"tabs":            "AppViews Discover YourApps AllApps DiscoverCurrent ConnectedCurrent DirectoryCurrent",
		"collection-back": "CollectionPresent BackToApps",
		"categories":      "CategoriesPresent Search CategoryLabel AllCategories Filter Options",
		"option":          "OptionID OptionSelected OptionName",
		"pagination":      "PaginationPresent Search Category NextCursor MoreApps",
	})
}
func renderAppDirectoryMarkup(t *template.Template, part string, d appDirectoryMarkupData, categories []composio.Category, language string) (template.HTML, error) {
	d.CategoriesPresent = len(categories) > 0
	d.PaginationPresent = d.NextCursor != ""
	if part == "categories" {
		var options bytes.Buffer
		for _, c := range categories {
			row := d
			row.OptionID = c.ID
			row.OptionName = translate(language, c.Name)
			row.OptionSelected = c.ID == d.Category
			if err := t.ExecuteTemplate(&options, "app-directory-view-option", row); err != nil {
				return "", err
			}
		}
		d.Options = template.HTML(options.String())
	}
	var out bytes.Buffer
	if err := t.ExecuteTemplate(&out, "app-directory-view-"+part, d); err != nil {
		return "", err
	}
	return template.HTML(out.String()), nil
}

func UnavailableAppDirectoryMarkup(p AppInterfacePolicy) AppInterfacePolicy {
	p = UnavailableAppSettingsMarkup(p)
	p.directoryManaged = true
	p.directoryMarkup = nil
	return p
}
func ManagedAppDirectoryMarkup(p AppInterfacePolicy, m contract.Markup) (AppInterfacePolicy, error) {
	if len(m.Source) > 16384 {
		return AppInterfacePolicy{}, connectedapps.ErrUnavailable
	}
	var source contract.DirectoryMarkupSource
	if rsiplugin.Decode([]byte(m.Source), &source) != nil {
		return AppInterfacePolicy{}, connectedapps.ErrUnavailable
	}
	t, err := compileAppDirectoryMarkup(source.Directory)
	if err != nil {
		return AppInterfacePolicy{}, err
	}
	raw, err := json.Marshal(source.SettingsMarkupSource)
	if err != nil {
		return AppInterfacePolicy{}, err
	}
	m.Source = string(raw)
	p, err = ManagedAppSettingsMarkup(p, m)
	if err != nil {
		return AppInterfacePolicy{}, err
	}
	p.directoryManaged = true
	p.directoryMarkup = t
	return p, nil
}
func appDirectoryPart(host *template.Template, v *appsView, part, language string) (template.HTML, error) {
	if part != "tabs" && part != "collection-back" && part != "categories" && part != "pagination" || v == nil {
		return "", connectedapps.ErrUnavailable
	}
	p := v.Interface
	if p == nil || !p.directoryManaged {
		var out bytes.Buffer
		err := host.ExecuteTemplate(&out, "app-directory-"+part+"-legacy-v5", page{Apps: v})
		return template.HTML(out.String()), err
	}
	if !p.available() || p.directoryMarkup == nil {
		return "", connectedapps.ErrUnavailable
	}
	copy := v.Copy()
	text := appCopyText(language)
	d := appDirectoryMarkupData{AppViews: text(v, copy.AppViews), Discover: text(v, copy.Discover), YourApps: text(v, copy.YourApps), AllApps: text(v, copy.AllApps), BackToApps: text(v, copy.BackToApps), CategoryLabel: text(v, copy.Category), AllCategories: text(v, copy.AllCategories), Filter: text(v, copy.Filter), MoreApps: text(v, copy.MoreApps), Search: v.Search, Category: v.Category, NextCursor: v.Catalog.NextCursor, DiscoverCurrent: !v.ConnectedOnly && !v.Directory, ConnectedCurrent: v.ConnectedOnly, DirectoryCurrent: v.Directory, CollectionPresent: v.Collection != ""}
	return renderAppDirectoryMarkup(p.directoryMarkup, part, d, v.Categories, language)
}

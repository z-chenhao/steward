package web

import (
	"slices"
	"strings"
	"unicode/utf8"

	"github.com/z-chenhao/assistant/internal/connectedapps"
	"github.com/z-chenhao/assistant/internal/plugin/appsinterface/contract"
)

func ManagedAppComposition(p AppInterfacePolicy, c contract.Composition) (AppInterfacePolicy, error) {
	if p.native || !p.available() || !validAppCopy(c.Copy) || !appSectionSet(c.Page, "heading", "unavailable", "feedback", "inventory", "body", "dialog") || !appSectionSet(c.Directory, "tabs", "collection-back", "categories", "groups", "pagination") || !appSectionSet(c.Detail, "heading", "feedback", "connect", "accounts", "additional") || !appSectionSet(c.Account, "copy", "actions") || !appSectionSet(c.Service, "heading", "back", "project") {
		return AppInterfacePolicy{}, connectedapps.ErrUnavailable
	}
	p.composition = &c
	return p, nil
}

// A complete permutation preserves existing reachability, required disclosures
// and original controls. The candidate owns the order, not this vocabulary.
func appSectionSet(parts []string, allowed ...string) bool {
	if len(parts) != len(allowed) {
		return false
	}
	seen := make(map[string]bool, len(parts))
	for _, p := range parts {
		if !slices.Contains(allowed, p) || seen[p] {
			return false
		}
		seen[p] = true
	}
	return true
}
func appParts(view *appsView, section string) []string {
	if view == nil {
		return nil
	}
	var c contract.Composition
	p := view.Interface
	if p == nil || p.native || p.available() && p.composition == nil {
		c = appCompositionLegacyV1()
	} else if p.available() {
		c = *p.composition
	} else {
		return nil
	}
	switch section {
	case "page":
		return c.Page
	case "directory":
		return c.Directory
	case "detail":
		return c.Detail
	case "account":
		return c.Account
	case "service":
		return c.Service
	}
	return nil
}

// No candidate data is involved in binding an already-scoped original row.
type appAccountContextView struct {
	appAccountView
	Apps *appsView
}

func appAccountContext(view *appsView, row appAccountView) appAccountContextView {
	return appAccountContextView{appAccountView: row, Apps: view}
}

func validAppCopy(c contract.Copy) bool {
	for _, text := range []string{c.AppsTitle, c.AppSettings, c.SearchApps, c.Search, c.RefreshApps, c.DirectoryUnavailable, c.BackToApps, c.AppViews, c.Discover, c.YourApps, c.AllApps, c.Category, c.AllCategories, c.Filter, c.ViewAll, c.ManageApp, c.AboutApp, c.NoApps, c.MoreApps, c.ConnectionsUnavailable, c.YourConnections, c.NoConnections, c.AddAccount, c.ServiceTitle, c.ProjectTitle, c.ProjectHelp, c.AuthenticationTitle, c.ConnectedApps} {
		if strings.TrimSpace(text) == "" || utf8.RuneCountInString(text) > 512 {
			return false
		}
	}
	return true
}
func (view *appsView) Copy() contract.Copy {
	if view == nil {
		return contract.Copy{}
	}
	p := view.Interface
	if p == nil || p.native || p.available() && p.composition == nil {
		return appCopyLegacyV1()
	}
	if p.available() {
		return p.composition.Copy
	}
	return contract.Copy{}
}

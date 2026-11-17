package web

import "github.com/z-chenhao/assistant/internal/plugin/appsinterface/contract"

// Exact pre-composition/default23/direct and explicitly retained profile-only
// pages. Never used after an opted-in current graph loses its composition
// service. Delete when those concrete compositions/references have retired.
func appCompositionLegacyV1() contract.Composition {
	return contract.Composition{
		Copy:      appCopyLegacyV1(),
		Page:      []string{"heading", "unavailable", "feedback", "inventory", "body", "dialog"},
		Directory: []string{"tabs", "collection-back", "categories", "groups", "pagination"},
		Detail:    []string{"heading", "feedback", "connect", "accounts", "additional"},
		Account:   []string{"copy", "actions"},
		Service:   []string{"heading", "back", "project"},
	}
}

func appCopyLegacyV1() contract.Copy {
	return contract.Copy{
		AppsTitle:              "Apps",
		AppSettings:            "App settings",
		SearchApps:             "Search apps",
		Search:                 "Search",
		RefreshApps:            "Refresh apps",
		DirectoryUnavailable:   "The live directory is unavailable. Your saved connections have been kept.",
		BackToApps:             "Back to apps",
		AppViews:               "App views",
		Discover:               "Discover",
		YourApps:               "Your apps",
		AllApps:                "All apps",
		Category:               "Category",
		AllCategories:          "All categories",
		Filter:                 "Filter",
		ViewAll:                "View all",
		ManageApp:              "Manage app",
		AboutApp:               "About app",
		NoApps:                 "No apps found.",
		MoreApps:               "More apps",
		ConnectionsUnavailable: "App connections are unavailable. The installation owner manages this service in App settings.",
		YourConnections:        "Your connections",
		NoConnections:          "No app connections yet.",
		AddAccount:             "Add account",
		ServiceTitle:           "App connection service",
		ProjectTitle:           "Composio project",
		ProjectHelp:            "Configure the shared connection service once. People connect their own app accounts from Apps.",
		AuthenticationTitle:    "App authentication",
		ConnectedApps:          "Connected apps",
	}
}

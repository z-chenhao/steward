package appsinterface

import "github.com/z-chenhao/assistant/internal/plugin/appsinterface/contract"

func composition() contract.Composition {
	return contract.Composition{
		Copy:      compositionCopy(),
		Page:      []string{"heading", "unavailable", "feedback", "inventory", "body", "dialog"},
		Directory: []string{"tabs", "collection-back", "categories", "groups", "pagination"},
		Detail:    []string{"heading", "feedback", "connect", "accounts", "additional"},
		Account:   []string{"copy", "actions"},
		Service:   []string{"heading", "back", "project"},
	}
}

func compositionCopy() contract.Copy {
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

package contract

// Composition is private section sequencing for the five actual Apps consumers.
// It is not a public component model, a template language or a DOM capability.
const CompositionInterface = "steward.apps-composition/1"

type Composition struct {
	Copy      Copy     `json:"copy"`
	Page      []string `json:"page"`
	Directory []string `json:"directory"`
	Detail    []string `json:"detail"`
	Account   []string `json:"account"`
	Service   []string `json:"service"`
}

// Copy contains only the existing feature presentation labels; fixed action and
// field meanings plus required privacy/revocation disclosures stay in Host atoms.
type Copy struct {
	AppsTitle              string `json:"apps_title"`
	AppSettings            string `json:"app_settings"`
	SearchApps             string `json:"search_apps"`
	Search                 string `json:"search"`
	RefreshApps            string `json:"refresh_apps"`
	DirectoryUnavailable   string `json:"directory_unavailable"`
	BackToApps             string `json:"back_to_apps"`
	AppViews               string `json:"app_views"`
	Discover               string `json:"discover"`
	YourApps               string `json:"your_apps"`
	AllApps                string `json:"all_apps"`
	Category               string `json:"category"`
	AllCategories          string `json:"all_categories"`
	Filter                 string `json:"filter"`
	ViewAll                string `json:"view_all"`
	ManageApp              string `json:"manage_app"`
	AboutApp               string `json:"about_app"`
	NoApps                 string `json:"no_apps"`
	MoreApps               string `json:"more_apps"`
	ConnectionsUnavailable string `json:"connections_unavailable"`
	YourConnections        string `json:"your_connections"`
	NoConnections          string `json:"no_connections"`
	AddAccount             string `json:"add_account"`
	ServiceTitle           string `json:"service_title"`
	ProjectTitle           string `json:"project_title"`
	ProjectHelp            string `json:"project_help"`
	AuthenticationTitle    string `json:"authentication_title"`
	ConnectedApps          string `json:"connected_apps"`
}

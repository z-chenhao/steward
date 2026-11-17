package contract

// SidebarView composes only the existing scoped connected-app entry atoms.
// It has no authority over row selection, private data, links or pending facts.
const SidebarViewInterface = "steward.apps-sidebar-view/1"
const SidebarViewResource = "apps-sidebar"
const SidebarViewFile = "views/sidebar.json"

type SidebarView struct {
	Resource string   `json:"resource"`
	Digest   string   `json:"digest"`
	Program  []string `json:"program"`
}

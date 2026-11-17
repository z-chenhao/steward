package contract

// Private display source for the existing directory navigation/filter controls.
const DirectoryControlsMarkupInterface = "steward.apps-view-markup/6"

type DirectoryMarkupSource struct {
	SettingsMarkupSource
	Directory string `json:"directory"`
}

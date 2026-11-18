// Package contract defines private coding recipes. Numeric methods receive no
// paths or commands. Project policy receives an explicitly read repository
// document, never Host mounts, credentials, authority or internal domain models.
package contract

const PluginID = "steward.coding-workspace"
const ImportContract = "steward.coding-import-limits/1"
const ReportContract = "steward.coding-report-recipe/1"
const CheckContract = "steward.coding-check-limits/1"

type CheckLimits struct {
	TimeoutSeconds int `json:"timeout_seconds"`
	OutputBytes    int `json:"output_bytes"`
}
type CheckFacts struct {
	Ceilings         CheckLimits `json:"ceilings"`
	RequestedTimeout *int        `json:"requested_timeout,omitempty"`
}

type ImportLimits struct {
	Entries   int `json:"entries"`
	FileBytes int `json:"file_bytes"`
	TreeBytes int `json:"tree_bytes"`
}
type ImportFacts struct {
	Ceilings ImportLimits `json:"ceilings"`
}
type ReportCeilings struct {
	Commands     int `json:"commands"`
	CommandBytes int `json:"command_bytes"`
	PatchBytes   int `json:"patch_bytes"`
}
type ReportFacts struct {
	Ceilings ReportCeilings `json:"ceilings"`
}
type ReportRecipe struct {
	ReviewGuide  []string `json:"review_guide,omitempty"`
	Commands     int      `json:"commands"`
	CommandBytes int      `json:"command_bytes"`
	PatchBytes   int      `json:"patch_bytes"`
	Order        string   `json:"order"`
}

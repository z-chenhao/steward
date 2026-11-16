// Package contract defines private numeric coding recipes. No path, repository,
// command, identity, credential or internal Report/Workspace crosses this seam.
package contract

const PluginID = "steward.coding-workspace"
const ImportContract = "steward.coding-import-limits/1"
const ReportContract = "steward.coding-report-recipe/1"

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
	Commands     int    `json:"commands"`
	CommandBytes int    `json:"command_bytes"`
	PatchBytes   int    `json:"patch_bytes"`
	Order        string `json:"order"`
}

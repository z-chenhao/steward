// Package contract holds private pure file methods. No path, workspace,
// backend, credential or file-writing capability crosses this boundary.
package contract

const PluginID = "steward.file-tools"
const WindowContract = "steward.file-window/1"
const EditContract = "steward.file-edit-span/1"

type WindowFacts struct {
	InputID     string `json:"input_id"`
	Offset      int    `json:"offset"`
	Limit       int    `json:"limit"`
	OutputBytes int    `json:"output_bytes"`
	AsArtifact  bool   `json:"as_artifact"`
}

type Window struct {
	Artifact   bool `json:"artifact"`
	Start      int  `json:"start"`
	End        int  `json:"end"`
	NextOffset int  `json:"next_offset"`
}

type EditFacts struct {
	InputID string `json:"input_id"`
	OldText string `json:"old_text"`
}

type EditSpan struct {
	Start     *int   `json:"start,omitempty"`
	ErrorCode string `json:"error_code,omitempty"`
}

// Package contract contains repository-private, experimental Apps method DTOs.
// No account, credential, tool descriptor, accepted arguments or response body
// crosses this seam. The invocation owner supplies only closed source phases.
package contract

const Method = "steward.apps-method/1"
const Guidance = "steward.apps-guidance/1"
const Source = "steward.apps-source/1"
const ToolPrefix = "mcp__apps__b__"

type Request struct {
	Operation string `json:"operation"`
}

type SourceRequest struct {
	Phase string `json:"phase"`
	Limit int    `json:"limit"`
}

type SourceReply struct {
	NeedsAccount bool `json:"needs_account"`
	HasCursor    bool `json:"has_cursor"`
	Bytes        int  `json:"bytes"`
}

// Plan selects presentation of the original complete Host-owned JSON. It
// cannot supply or alter response data, effect certainty or an Artifact ref.
type Plan struct {
	Mode    string `json:"mode"`
	Name    string `json:"name"`
	Message string `json:"message"`
}

// TextRecipe has exactly one Host-owned factual label slot. It is not a
// general template language, and candidates do not receive the label itself.
type TextRecipe struct {
	Before string `json:"before"`
	After  string `json:"after"`
}

type GuidancePlan struct {
	Namespace TextRecipe `json:"namespace"`
	Search    TextRecipe `json:"search"`
	Execute   TextRecipe `json:"execute"`
}

// Package contract contains private MCP method DTOs, not an external MCP SDK.
package contract

const Method = "steward.mcp-method/1"
const Source = "steward.mcp-source/1"
const Namespace = "steward.mcp-namespace/1"

type NamespaceRequest struct {
	InputID string `json:"input_id"`
}

// NamespacePlan selects original source bytes. Source choice, provenance and
// the final discovery response remain Host-owned.
type NamespacePlan struct {
	Start     int  `json:"start"`
	End       int  `json:"end"`
	Truncated bool `json:"truncated"`
}

// SourceRequest can advance only an invocation-owned peer. It cannot name a
// server, tool, command, arguments or permission.
type SourceRequest struct {
	Phase  string `json:"phase"`
	Cursor string `json:"cursor"`
}

type SourceReply struct {
	NextCursor string   `json:"next_cursor"`
	Text       []string `json:"text"`
	IsError    bool     `json:"is_error"`
}

type Part struct {
	Index int `json:"index"`
	Start int `json:"start"`
	End   int `json:"end"`
}

// Plan selects original accepted text, never a replacement result or effect
// certainty. A separator is presentation, not a new piece of source evidence.
type Plan struct {
	Parts     []Part `json:"parts"`
	Separator string `json:"separator"`
}

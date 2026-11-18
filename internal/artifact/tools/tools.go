// Package tools declares the scoped Artifact contract. Every operation uses
// the ordinary ToolInvocation pipeline; ownership never comes from arguments.
package tools

import "github.com/z-chenhao/assistant/internal/tool"

const Manage = "builtin__artifact_manage"
const Search = "builtin__artifact_search"

const ManageSchema = `{"oneOf":[{"type":"object","properties":{"operation":{"const":"save"},"name":{"type":"string","maxLength":255},"mediaType":{"type":"string","maxLength":128},"text":{"type":"string","maxLength":8192}},"required":["operation","name","mediaType","text"],"additionalProperties":false},{"type":"object","properties":{"operation":{"const":"read"},"artifactRef":{"type":"string","maxLength":256},"offset":{"type":"integer","minimum":0,"maximum":1073741824},"limit":{"type":"integer","minimum":1,"maximum":4096}},"required":["operation","artifactRef"],"additionalProperties":false},{"type":"object","properties":{"operation":{"const":"describe"},"artifactRef":{"type":"string","maxLength":256}},"required":["operation","artifactRef"],"additionalProperties":false},{"type":"object","properties":{"operation":{"const":"publish"},"artifactRefs":{"type":"array","maxItems":127,"items":{"type":"string","maxLength":256}}},"required":["operation","artifactRefs"],"additionalProperties":false}]}`
const originalSearchSchema = `{"type":"object","properties":{"query":{"type":"string","maxLength":4096},"limit":{"type":"integer","minimum":1,"maximum":50}},"additionalProperties":false}`

const pagedSearchSchema = `{"type":"object","properties":{"cursor":{"type":"string","maxLength":512},"query":{"type":"string","maxLength":4096},"limit":{"type":"integer","minimum":1,"maximum":50}},"additionalProperties":false}`

const SearchSchema = `{"type":"object","properties":{"content":{"type":"boolean"},"cursor":{"type":"string","maxLength":512},"query":{"type":"string","maxLength":4096},"limit":{"type":"integer","minimum":1,"maximum":50}},"additionalProperties":false}`

func Definitions() []tool.Definition {
	original := tool.Spec{Name: Search, Description: "Find accessible Artifacts by name or media type. Omit query to browse newest first. Returns up to limit (default 50) scoped descriptors, never file contents. Read via artifact manage.", InputSchema: originalSearchSchema, Version: 1, Permission: "read_only", ReplayPolicy: "safe", OutputBytes: 65536}
	search := original
	search.Version = 2
	search.InputSchema = pagedSearchSchema
	search.Description += " For pagination, pass an empty cursor on the first page and then pass the returned next_cursor with the same query. Paged results contain artifacts and an optional next_cursor."
	paged := search
	search.Version = 3
	search.InputSchema = SearchSchema
	search.Description += " To find an original by an exact text fragment, set content to true with a nonempty query. Only the matching descriptors are returned."
	return []tool.Definition{
		{Host: true, Spec: tool.Spec{Name: Manage, Description: "Manage accessible Artifacts: save an immutable local text file; read a bounded text range (binary returns a reference); describe metadata; publish selects final output references for the current Run. Publish does not send to the user or upload externally. Use operation with only that operation's fields. Scope comes from the Host.", InputSchema: ManageSchema, Version: 1, Permission: "artifact_write", ReplayPolicy: "safe", OutputBytes: 65536}},
		{Host: true, Spec: search, PreviousSpecs: []tool.Spec{original, paged}},
	}
}
func Known(name string) bool { return name == Manage || name == Search }

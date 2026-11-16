package tool

const SearchName = "builtin__tool_search"
const DefaultSearchLimit = 8
const MaxSearchLimit = 128
const SearchSchema = `{"type":"object","properties":{"query":{"type":"string","minLength":1,"maxLength":4096},"limit":{"type":"integer","minimum":1,"maximum":128}},"required":["query"],"additionalProperties":false}`

type SearchRequest struct {
	Query string `json:"query"`
	Limit int    `json:"limit,omitempty"`
}

type ToolMatch struct {
	Name        string `json:"name"`
	Description string `json:"description"`
}

type SearchResult struct {
	Loaded []string    `json:"loaded"`
	Tools  []ToolMatch `json:"tools"`
}

func SearchDefinition() Definition {
	return Definition{Host: true, Spec: Spec{Name: SearchName, Version: 1, Exposure: ModelOnly, InputSchema: SearchSchema, Permission: "read_only", ReplayPolicy: "safe", OutputBytes: 1 << 20, Description: "Discover authorized tools by name or intent. Discovered tools execute through Code Mode: use its searchTools/describeTool helpers, or delegate to a Worker when Code Mode is unavailable. Search never executes a tool, grants permissions or approves an action. Defaults to 8 matches."}}
}

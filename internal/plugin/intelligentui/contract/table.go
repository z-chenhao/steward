package contract

import "encoding/json"

// TableViews is private and pure. Host binds original refs, CAS, group identity
// and local downloads; package code owns presentation and row-order policy.
const TableViews = "steward.ui-table-view/1"
const TableInput = "ui-tables"

type TableFacts struct {
	Tables []TableFact `json:"tables"`
}
type TableFact struct {
	Component int             `json:"component"`
	Props     json.RawMessage `json:"props"`
}

type TableDescription struct {
	Title     string  `json:"title"`
	Sort      *string `json:"sort"`
	Direction int     `json:"direction"`
}

// Descriptions are frozen before browser execution. Only the exact bounded
// sort/direction pair is proposed by the frame, never arbitrary model text.
type TableSortContext struct {
	Sort        int              `json:"sort"`
	Direction   int              `json:"direction"`
	Description TableDescription `json:"description"`
}

type TableView struct {
	Component       int                `json:"component"`
	HTML            string             `json:"html"`
	Worker          string             `json:"worker"`
	CSS             string             `json:"css"`
	CompactMaxWidth int                `json:"compact_max_width"`
	Contexts        []TableSortContext `json:"contexts"`
}
type TableViewsResult struct {
	Views     []TableView `json:"views"`
	ErrorCode string      `json:"error_code"`
}

const TableViewsInputSchema = `{"type":"object","properties":{},"required":[],"additionalProperties":false}`
const TableViewsOutputSchema = `{"type":"object","properties":{"views":{"type":"array","maxItems":16,"items":{"type":"object","properties":{"component":{"type":"integer","minimum":0,"maximum":15},"html":{"type":"string","maxLength":524288,"minLength":1},"worker":{"type":"string","maxLength":16384,"minLength":1},"css":{"type":"string","maxLength":16384,"minLength":1},"compact_max_width":{"type":"integer","minimum":1,"maximum":4096},"contexts":{"type":"array","minItems":4,"maxItems":26,"items":{"type":"object","properties":{"sort":{"type":"integer","minimum":-1,"maximum":11},"direction":{"type":"integer","enum":[1,-1]},"description":{"type":"object","properties":{"title":{"type":"string","maxLength":300},"sort":{"oneOf":[{"type":"string","maxLength":200},{"type":"null"}]},"direction":{"type":"integer","enum":[1,-1]}},"required":["title","sort","direction"],"additionalProperties":false}},"required":["sort","direction","description"],"additionalProperties":false}}},"required":["component","html","worker","css","compact_max_width","contexts"],"additionalProperties":false}},"error_code":{"type":"string","enum":["","INVALID_ARGUMENT"]}},"required":["views","error_code"],"additionalProperties":false}`

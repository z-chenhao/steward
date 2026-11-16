package contract

import "encoding/json"

// CardsViews is the private pure-view service consumed by Main publication
// and ephemeral preview. It is not a public renderer SDK.
const CardsViews = "steward.ui-cards-view/1"
const CardsInput = "ui-cards"

type CardsFacts struct {
	Cards []CardsFact `json:"cards"`
}
type CardsFact struct {
	Component int             `json:"component"`
	Props     json.RawMessage `json:"props"`
}
type CardsContext struct {
	Title       string `json:"title,omitempty"`
	VisibleItem string `json:"visible_item"`
}

type CardsView struct {
	Component int    `json:"component"`
	HTML      string `json:"html"`
	// Runtime is retained only to decode explicitly supported historical snapshots.
	Runtime         string         `json:"runtime,omitempty"`
	Worker          string         `json:"worker,omitempty"`
	CSS             string         `json:"css"`
	CompactMaxWidth int            `json:"compact_max_width"`
	Contexts        []CardsContext `json:"contexts"`
}
type CardsViewsResult struct {
	Views     []CardsView `json:"views"`
	ErrorCode string      `json:"error_code"`
}

const CardsViewsInputSchema = `{"type":"object","properties":{},"required":[],"additionalProperties":false}`
const CardsViewsOutputSchema = `{"type":"object","properties":{"views":{"type":"array","maxItems":16,"items":{"type":"object","properties":{"component":{"type":"integer","minimum":0,"maximum":15},"html":{"type":"string","maxLength":524288},"css":{"type":"string","maxLength":16384},"compact_max_width":{"type":"integer","minimum":1,"maximum":4096},"contexts":{"type":"array","minItems":1,"maxItems":16,"items":{"type":"object","properties":{"title":{"type":"string","maxLength":300},"visible_item":{"type":"string","minLength":1,"maxLength":300}},"required":["visible_item"],"additionalProperties":false}},"worker":{"type":"string","minLength":1,"maxLength":16384}},"required":["component","html","css","compact_max_width","contexts","worker"],"additionalProperties":false}},"error_code":{"type":"string","enum":["","INVALID_ARGUMENT"]}},"required":["views","error_code"],"additionalProperties":false}`

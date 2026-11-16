package contract

const MarkupInterface = "steward.apps-view-markup/1"
const MarkupResource = "apps-view-markup"
const MarkupFile = "views/markup.json"

// Exact declared source identity only;display facts never enter this wire DTO.
type Markup struct {
	Resource string `json:"resource"`
	Digest   string `json:"digest"`
	Source   string `json:"source"`
}
type MarkupSource struct {
	Card    string `json:"card"`
	Sidebar string `json:"sidebar"`
}

// Version2 owns the three graphic primitive templates as well as atom markup.
const GraphicMarkupInterface = "steward.apps-view-markup/2"

type GraphicMarkupSource struct {
	Card     string `json:"card"`
	Sidebar  string `json:"sidebar"`
	Graphics string `json:"graphics"`
}

// AccountMarkupSource adds the two existing account sections to the same sealed
// resource. It contains template source only, never account State or credentials.
// Host-created control slots retain the original identity and effect semantics.
const AccountMarkupInterface = "steward.apps-view-markup/3"

type AccountMarkupSource struct {
	Card     string `json:"card"`
	Sidebar  string `json:"sidebar"`
	Graphics string `json:"graphics"`
	Account  string `json:"account"`
}

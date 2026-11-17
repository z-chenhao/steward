// Package contract defines the private, pure Computer methods.
// Native identities, handles, revisions, elements and effects never cross it.
package contract

const PluginID = "steward.computer-use"
const ObservationContract = "steward.computer-observation/1"
const WaitContract = "steward.computer-wait-recipe/1"
const DocumentationContract = "steward.computer-documentation/1"
const SelectionContract = "steward.computer-text-selection/1"

type SelectionFacts struct {
	Prefix string `json:"prefix"`
	Length int    `json:"length"`
	Wanted string `json:"wanted"`
}

// SelectionRecipe contains only bounded caret movement. It grants no key,
// target, text-injection, native identity or Driver callback authority.
type SelectionRecipe struct {
	Anchor string `json:"anchor"`
	Move   int    `json:"move"`
	Extend int    `json:"extend"`
}

type DocumentationFacts struct {
	Kind string `json:"kind"`
}
type Documentation struct {
	Text string `json:"text"`
}

type WaitCeilings struct {
	TimeoutMS int `json:"timeout_ms"`
	MinPollMS int `json:"min_poll_ms"`
	MaxPollMS int `json:"max_poll_ms"`
}
type WaitFacts struct {
	Ceilings WaitCeilings `json:"ceilings"`
}
type WaitRecipe struct {
	TimeoutMS int `json:"timeout_ms"`
	PollMS    int `json:"poll_ms"`
}

type ObservationFacts struct {
	DiffAllowed bool `json:"diff_allowed"`
	Full        bool `json:"full"`
}

type ObservationSnapshots struct {
	Before string `json:"before"`
	After  string `json:"after"`
}

// ObservationPlan selects a complete contiguous replacement. Host reconstructs
// the public text from original snapshots, never from candidate-authored prose.
type ObservationPlan struct {
	Mode    string `json:"mode"`
	Start   int    `json:"start"`
	Removed int    `json:"removed"`
	Added   int    `json:"added"`
}

const InventoryOrderContract = "steward.computer-inventory-order/1"
const TargetMatchContract = "steward.computer-target-match/1"

type InventoryFacts struct {
	Kind string `json:"kind"`
}
type TargetLabel struct {
	Name string `json:"name"`
	URL  string `json:"url"`
}
type InventoryLabels struct {
	Labels []TargetLabel `json:"labels"`
}
type InventoryOrder struct {
	Indices []int `json:"indices"`
}

type MatchFacts struct {
	Kind string `json:"kind"`
}

// CompleteText explicitly distinguishes a complete label from a bounded prefix.
// A truncated prefix cannot be treated as an equal complete label.
type CompleteText struct {
	Text     string `json:"text"`
	Complete bool   `json:"complete"`
}
type MatchLabel struct {
	Name    CompleteText `json:"name"`
	URL     CompleteText `json:"url"`
	IDMatch bool         `json:"id_match"`
}
type MatchLabels struct {
	Selector string       `json:"selector"`
	Labels   []MatchLabel `json:"labels"`
}
type TargetMatches struct {
	Indices []int `json:"indices"`
}

const ActionParametersContract = "steward.computer-action-parameters/1"
const PlaywrightParametersContract = "steward.computer-playwright-parameters/1"
const TransferPresentationContract = "steward.computer-transfer-presentation/1"
const BrowserUploadContract = "steward.computer-browser-upload/1"

type ActionFacts struct {
	Method  string `json:"method"`
	Surface string `json:"surface"`
}
type ActionArguments struct {
	Args []any `json:"args"`
}

// ActionParameters has no target, reference, revision or dispatch authority.
// The owner freezes the complete result before its original action approval.
type ActionParameters struct {
	NameFromSource bool       `json:"name_from_source,omitempty"`
	Name           string     `json:"name,omitempty"`
	MediaType      string     `json:"media_type,omitempty"`
	Selector       string     `json:"selector,omitempty"`
	TimeoutMS      *int       `json:"timeout_ms,omitempty"`
	KeyInput       *KeyRecipe `json:"key_input,omitempty"`
	Button         string     `json:"button,omitempty"`
	Count          *int       `json:"count,omitempty"`
	Direction      string     `json:"direction,omitempty"`
	Amount         *float64   `json:"amount,omitempty"`
	Text           string     `json:"text,omitempty"`
	Value          string     `json:"value,omitempty"`
	URL            string     `json:"url,omitempty"`
	App            string     `json:"app,omitempty"`
	Browser        string     `json:"browser,omitempty"`
	Failure        string     `json:"failure,omitempty"`
}

// KeyRecipe is a lossless decomposition of the original approved key string.
type KeyRecipe struct {
	Keys []string `json:"keys"`
}

const BrowserProjectionContract = "steward.computer-browser-projection/1"

type BrowserRowFacts struct {
	RoleUnits        int  `json:"role_units"`
	AriaUnits        int  `json:"aria_units"`
	InnerUnits       int  `json:"inner_units"`
	PlaceholderUnits int  `json:"placeholder_units"`
	ValueUnits       *int `json:"value_units,omitempty"`
}
type BrowserProjectionFacts struct {
	BodyUnits int               `json:"body_units"`
	Rows      []BrowserRowFacts `json:"rows"`
}
type BrowserRowView struct {
	Index      int    `json:"index"`
	Label      string `json:"label"`
	LabelUnits int    `json:"label_units"`
	ValueUnits *int   `json:"value_units,omitempty"`
}
type BrowserProjection struct {
	BodyUnits int              `json:"body_units"`
	Rows      []BrowserRowView `json:"rows"`
}

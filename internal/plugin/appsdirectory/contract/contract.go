// Package contract is the repository-private experimental editorial seam.
// Public catalogue metadata and the owner-supplied directory query only;
// no internal account model, credentials or authority.
package contract

const Editorial = "steward.apps-editorial/1"
const CategoryOptions = "steward.apps-category-options/1"
const CategoryInputID = "categories"

// The input contains LE32 equality identities, not source IDs or labels.
// Keep is an LSB-first bitmap over the original eligible source positions.
type CategoryRequest struct {
	Count        int    `json:"count"`
	HasRequested bool   `json:"has_requested"`
	RequestedKey uint32 `json:"requested_key"`
}

type CategoryPlan struct {
	Keep            []byte `json:"keep"`
	AppendRequested bool   `json:"append_requested"`
}

type Query struct {
	Search         string `json:"search,omitempty"`
	Category       string `json:"category,omitempty"`
	LiveConfigured bool   `json:"live_configured,omitempty"`
	HasCursor      bool   `json:"has_cursor,omitempty"`
}

type Entry struct {
	Slug         string `json:"slug"`
	Name         string `json:"name"`
	Description  string `json:"description"`
	CategoryID   string `json:"category_id"`
	CategoryName string `json:"category_name"`
}

type Catalogue struct {
	Items     []Entry      `json:"items"`
	Discovery *Discovery   `json:"discovery"`
	Filter    *Filter      `json:"filter"`
	Route     *Route       `json:"route"`
	Lookup    *Lookup      `json:"lookup"`
	Labels    *LabelPolicy `json:"labels"`
}

// Lookup chooses presentation sources, never an account or provider request.
type Lookup struct {
	PreferLive  bool   `json:"prefer_live"`
	LocalFirst  string `json:"local_first"`
	OnLiveError string `json:"on_live_error"`
}

type Route struct {
	Source      string `json:"source"`
	PageSize    int    `json:"page_size,omitempty"`
	OnLiveError string `json:"on_live_error,omitempty"`
}

type Category struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

type Filter struct {
	Slugs      []string   `json:"slugs"`
	Categories []Category `json:"categories"`
}

// Discovery contains public references, never account state or availability.
// Home groups may link to an independently complete collection.
type Group struct {
	ID    string   `json:"id"`
	Title string   `json:"title"`
	Slugs []string `json:"slugs"`
}

type Discovery struct {
	Home        []Group `json:"home"`
	Collections []Group `json:"collections"`
}

// Captions receives only rune counts. Original descriptions and account names
// stay with the consumer; the result references one original source prefix.
const Captions = "steward.apps-card-captions/1"
const CaptionBatchSize = 64

type CaptionFacts struct {
	EditorialRunes int `json:"editorial_runes"`
	ProviderRunes  int `json:"provider_runes"`
}
type CaptionPlan struct {
	Source string `json:"source"`
	End    int    `json:"end"`
	Text   string `json:"text"`
}

// LabelPolicy has exactly the two existing name-presence states. Plans contain
// original field references only, never private names or account records.
type LabelPolicy struct {
	Named   LabelPlan `json:"named"`
	Unnamed LabelPlan `json:"unnamed"`
}
type LabelPlan struct {
	NameSource   string `json:"name_source"`
	UpperInitial bool   `json:"upper_initial"`
}

// CatalogueOrder is a repository-private public-catalogue seam, not account inventory.
const CatalogueOrder = "steward.apps-catalogue-order/1"
const OrderInputID = "catalogue-order"
const OrderPageSize = 16384

type OrderRequest struct {
	Count  int         `json:"count"`
	Offset int         `json:"offset"`
	Labels LabelPolicy `json:"labels"`
}
type OrderPage struct {
	Indices []int `json:"indices"`
}
type OrderFact struct{ Name, Slug string }

const CardStates = "steward.apps-card-states/1"
const CardStateInputID = "card-states"
const CardStateWindow = 65536
const (
	FactActive byte = 1 << iota
	FactPending
	FactDisconnected
	FactRevocationConfirmed
	FactPublic
)

// References name an original row within one Host-bound toolkit equivalence
// class. Private names, IDs and the state revision never enter candidate JSON.
type CardStatePlan struct {
	Managed *int `json:"managed,omitempty"`
	Active  *int `json:"active,omitempty"`
	Pending *int `json:"pending,omitempty"`
}
type CardStateRequest struct {
	Count  int           `json:"count"`
	Offset int           `json:"offset"`
	Prior  CardStatePlan `json:"prior"`
}

// Package contract is the repository-private Artifact policy seam. Host-owned
// scopes, references, file paths, SQL and credentials never enter it. Only BodyMatch receives authorized text windows.
package contract

const PluginID = "steward.artifact-workbench"
const Recipe = "steward.artifact-recipe/1"
const Search = "steward.artifact-selection/1"
const Read = "steward.artifact-read/1"
const Cleanup = "steward.artifact-cleanup/1"
const BodyMatch = "steward.artifact-body-match/1"
const BodyInput = "text-window"
const SearchInput = "artifact-metadata"
const CleanupInput = "expired-metadata"

// Zero means absent, not a caller-supplied zero retention. The adapter must
// preserve the original configuration's explicit/default provenance.
type RecipeRequest struct {
	RetentionDays int `json:"retention_days"`
}
type RecipePlan struct {
	RetentionDays int      `json:"retention_days"`
	MediaKinds    []string `json:"media_kinds"`
	OverflowName  string   `json:"overflow_name"`
	OverflowMedia string   `json:"overflow_media"`
	StoredText    string   `json:"stored_text"`
	PublishText   string   `json:"publish_text"`
}
type SearchRequest struct {
	Paged bool   `json:"paged,omitempty"`
	Count int    `json:"count"`
	Query string `json:"query"`
	Limit int    `json:"limit"`
}

// Row order is the Host's immutable reference order, used only to break equal
// creation timestamps. The producer owns matching and newest-first ranking.
type SearchRow struct {
	CreatedAt       int64
	Name, MediaType string
}
type Selection struct {
	More      bool   `json:"more,omitempty"`
	Indices   []int  `json:"indices"`
	Limit     int    `json:"limit"`
	ErrorCode string `json:"error_code,omitempty"`
}
type ReadRequest struct {
	MediaType string `json:"media_type"`
	ByteSize  int64  `json:"byte_size"`
	Offset    int64  `json:"offset"`
	Limit     int    `json:"limit"`
}
type ReadPlan struct {
	Kind      string `json:"kind"`
	Limit     int    `json:"limit"`
	Notice    string `json:"notice,omitempty"`
	ErrorCode string `json:"error_code,omitempty"`
}
type CleanupRequest struct {
	Count int `json:"count"`
	Batch int `json:"batch"`
}

// The Host supplies only expired, unreferenced, owner-eligible rows. Candidate
// selection cannot authorize deletion, remove pins or change stored expiry.
type CleanupRow struct{ ExpiresAt int64 }

type BodyRequest struct {
	Query string `json:"query"`
}
type BodyResult struct {
	Match bool `json:"match"`
}

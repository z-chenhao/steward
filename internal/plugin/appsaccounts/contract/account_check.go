package contract

const AccountCheck = "steward.apps-account-check/1"

// AccountCheckRequest contains only original source proofs needed by status
// policy. No raw response/status, account/user/toolkit ID or credential crosses.
type AccountCheckRequest struct {
	Public       bool `json:"public"`
	Disabled     bool `json:"disabled"`
	Active       bool `json:"active"`
	Initializing bool `json:"initializing"`
	Initiated    bool `json:"initiated"`
}
type AccountCheckPlan struct {
	Status string `json:"status"`
}

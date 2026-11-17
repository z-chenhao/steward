package contract

// AccountClose is private remote cleanup, after independently durable local
// owner closure. It carries no identity, URL, credentials or account object.
const AccountClose = "steward.apps-account-close/1"

type AccountCloseRequest struct {
	Step    string `json:"step"`
	Matched bool   `json:"matched"`
	Failed  bool   `json:"failed"`
	Revoked bool   `json:"revoked"`
}
type AccountClosePlan struct {
	Kind string `json:"kind"`
}

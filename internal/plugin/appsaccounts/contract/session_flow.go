package contract

// SessionFlow is a private, closed connection workflow. It carries no owner,
// Toolkit, config/session/account ID, credential or authorization URL.
const SessionFlow = "steward.apps-session-flow/1"

type SessionRequest struct {
	Step   string `json:"step"`
	NoAuth bool   `json:"no_auth"`
}
type SessionPlan struct {
	Kind       string `json:"kind"`
	TTLSeconds int    `json:"ttl_seconds,omitempty"`
}

package contract

const Reconnect = "steward.apps-reconnect/1"

// ReconnectRequest exposes only stage and original pending fact. Identity,
// original alias/Toolkit, credentials and accepted Link remain owner private.
type ReconnectRequest struct {
	Step    string `json:"step"`
	Pending bool   `json:"pending"`
}
type ReconnectPlan struct {
	Kind string `json:"kind"`
}

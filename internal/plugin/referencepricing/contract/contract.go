// Package contract is a private reference-price seam, not a spend authorization API.
package contract

const PluginID = "steward.reference-pricing"
const Service = "steward.reference-pricing/1"

type Request struct {
	ProviderKind string `json:"provider_kind"`
	EndpointHash string `json:"endpoint_hash"`
	WireAPI      string `json:"wire_api"`
	ModelID      string `json:"model_id"`
}
type Rates struct {
	Input      int64 `json:"input"`
	Output     int64 `json:"output"`
	CacheRead  int64 `json:"cache_read"`
	CacheWrite int64 `json:"cache_write"`
}
type Tier struct {
	Above int64 `json:"above_input_tokens"`
	Rates Rates `json:"rates"`
}
type Table struct {
	Currency string `json:"currency"`
	Source   string `json:"source"`
	Version  string `json:"version"`
	Base     Rates  `json:"base"`
	Tiers    []Tier `json:"tiers"`
}
type Response struct {
	Table *Table `json:"table"`
}

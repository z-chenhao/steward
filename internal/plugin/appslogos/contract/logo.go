package contract

const Logo = "steward.apps-logo/1"

type Request struct {
	Slug       string `json:"slug"`
	Registered bool   `json:"registered"`
	Cached     bool   `json:"cached"`
	AgeNS      int64  `json:"age_ns"`
}
type Plan struct {
	Kind         string `json:"kind"`
	Body         []byte `json:"body,omitempty"`
	CacheSeconds int    `json:"cache_seconds"`
}

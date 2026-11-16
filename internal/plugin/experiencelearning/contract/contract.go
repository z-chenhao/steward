// Package contract exposes only a private reusable-method recipe, without
// candidate content, source IDs, execution proofs or publication authority.
package contract

const PluginID = "steward.experience-learning"
const Contract = "steward.experience-policy/1"

type Request struct {
	Reason string `json:"reason"`
}
type Policy struct {
	RequiredRuns int    `json:"required_runs"`
	Introduction string `json:"introduction"`
}

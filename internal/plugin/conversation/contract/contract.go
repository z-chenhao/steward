// Package contract is the private Main-method policy seam. It grants no role,
// capability, input queue, delegation, delivery or message mutation authority.
package contract

const PluginID = "steward.conversation"
const Contract = "steward.conversation-policy/1"

type Request struct {
	Role string `json:"role"`
}
type Policy struct {
	RolePolicy      string `json:"role_policy"`
	ExecutionPolicy string `json:"execution_policy"`
}

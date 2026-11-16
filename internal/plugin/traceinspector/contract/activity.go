package contract

// Activity is the private uniform histogram policy used before Host counting.
const Activity = "steward.trace-activity/1"

type ActivityFacts struct {
	First *int64 `json:"first,omitempty"`
	Last  *int64 `json:"last,omitempty"`
	Since *int64 `json:"since,omitempty"`
	Until *int64 `json:"until,omitempty"`
}
type ActivityPlan struct {
	Present bool  `json:"present"`
	Start   int64 `json:"start"`
	End     int64 `json:"end"`
	Base    int64 `json:"base"`
	Step    int64 `json:"step"`
}

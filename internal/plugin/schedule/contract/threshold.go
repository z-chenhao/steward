package contract

// Threshold is private and experimental. It carries bounded observation facts,
// not authority to read a fire, update its state, or send a notification.
const Threshold = "steward.schedule-threshold/1"

type ThresholdFacts struct {
	Value   int64 `json:"value"`
	Below   int64 `json:"below"`
	Entered bool  `json:"entered"`
	Fresh   bool  `json:"fresh"`
}

type ThresholdResult struct {
	IsBelow bool `json:"is_below"`
	Notify  bool `json:"notify"`
}

const ThresholdInputSchema = `{"type":"object","properties":{"value":{"type":"integer","minimum":0,"maximum":1000000000000},"below":{"type":"integer","minimum":0,"maximum":1000000000000},"entered":{"type":"boolean"},"fresh":{"type":"boolean"}},"required":["value","below","entered","fresh"],"additionalProperties":false}`
const ThresholdOutputSchema = `{"type":"object","properties":{"is_below":{"type":"boolean"},"notify":{"type":"boolean"}},"required":["is_below","notify"],"additionalProperties":false}`

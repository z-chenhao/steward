package coding

// ReviewFacts are scoped observations, not an acceptance decision or score.
// Missing provider usage/prices stay unavailable; reported sums may be partial.
type ReviewFacts struct {
	TaskStatus            string        `json:"task_status"`
	OriginalArtifacts     []string      `json:"original_artifacts"`
	OriginalTask          string        `json:"original_task"`
	TaskEpoch             int64         `json:"task_epoch"`
	InstructionSequence   int64         `json:"instruction_sequence"`
	Instructions          []Instruction `json:"instructions"`
	InstructionsTruncated bool          `json:"instructions_truncated"`
	ObservedAt            int64         `json:"observed_at_unix_micro"`
	Guide                 []string      `json:"guide,omitempty"`
	Metrics               ReviewMetrics `json:"metrics"`
}

type Instruction struct {
	ArtifactRefs []string `json:"artifact_refs"`
	Epoch        int64    `json:"epoch"`
	Sequence     int64    `json:"sequence"`
	Text         string   `json:"text"`
}

type ReviewMetrics struct {
	TaskWallMS           int64        `json:"task_wall_ms"`
	Runs                 int          `json:"runs"`
	ModelRequests        int          `json:"model_requests"`
	UsageRecords         int          `json:"usage_records"`
	UsageUnavailable     int          `json:"usage_unavailable"`
	InputTokensReported  *int64       `json:"input_tokens_reported"`
	OutputTokensReported *int64       `json:"output_tokens_reported"`
	InputTokensMissing   int          `json:"input_tokens_missing"`
	OutputTokensMissing  int          `json:"output_tokens_missing"`
	Priced               []ReviewCost `json:"priced"`
	UnpricedUsage        int          `json:"unpriced_usage"`
}

type ReviewCost struct {
	Currency     string `json:"currency"`
	ActualMicros int64  `json:"actual_micros"`
	Generations  int    `json:"generations"`
}

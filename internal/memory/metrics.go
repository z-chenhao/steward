package memory

// Metrics separate maintenance work, changed knowledge, and later exposure.
// Exposure is not a usefulness score. Unknown pricing remains unknown.
type MaintenanceMetrics struct {
	InputTokens           int64             `json:"input_tokens"`
	OutputTokens          int64             `json:"output_tokens"`
	CacheReadTokens       int64             `json:"cache_read_tokens"`
	KnownUsageGenerations int               `json:"known_usage_generations"`
	ModelMilliseconds     int64             `json:"model_milliseconds"`
	Generations           int               `json:"generations"`
	DocumentsWritten      int               `json:"documents_written"`
	DocumentsRetired      int               `json:"documents_retired"`
	UnchangedWrites       int               `json:"unchanged_writes"`
	LaterToolReads        int               `json:"later_tool_reads"`
	LaterInjections       int               `json:"later_injections"`
	UnpricedGenerations   int               `json:"unpriced_generations"`
	Costs                 []MaintenanceCost `json:"costs"`
}
type MaintenanceCost struct {
	Currency          string `json:"currency"`
	ReferenceMicros   int64  `json:"reference_micros"`
	PricedGenerations int    `json:"priced_generations"`
}

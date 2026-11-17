// Package contract is a private, experimental context-selection seam. Only
// costs and opaque image equality keys cross it; no prompt, memory or ciphertext.
package contract

const PluginID = "steward.context-compact"
const Contract = "steward.context-selection/1"
const OmittedImageNotice = "\nHistorical image pixels omitted; reopen the scoped Artifact if needed."

type Cost struct {
	Model int `json:"model"`
	Frame int `json:"frame"`
}
type Image struct {
	Key   string `json:"key"`
	Bytes int    `json:"bytes"`
}
type Message struct {
	Role      string  `json:"role"`
	Protected bool    `json:"protected"`
	Input     bool    `json:"input"`
	Recall    bool    `json:"recall"`
	Full      Cost    `json:"full"`
	Reduced   Cost    `json:"reduced"`
	Images    []Image `json:"images"`
}
type Facts struct {
	Messages           []Message `json:"messages"`
	ModelBytes         int       `json:"model_bytes"`
	HardModelBytes     int       `json:"hard_model_bytes"`
	FrameBytes         int       `json:"frame_bytes"`
	Recent             int       `json:"recent"`
	SummaryBytes       int       `json:"summary_bytes"`
	ImageLimit         int       `json:"image_limit"`
	FullWindowFallback bool      `json:"full_window_fallback"`
	Aggressive         bool      `json:"aggressive"`
}
type Request struct {
	InputID string `json:"input_id"`
}
type Plan struct {
	Images       [][]int `json:"images"`
	Through      int     `json:"summarize_through"`
	Instructions string  `json:"summary_instructions,omitempty"`
	ErrorCode    string  `json:"error_code,omitempty"`
}

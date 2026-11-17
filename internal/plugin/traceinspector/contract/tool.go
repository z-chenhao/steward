package contract

const ToolContract = "steward.trace-tool-preview/1"

type ToolFacts struct {
	Name      string `json:"name"`
	Available bool   `json:"available"`
	Complete  bool   `json:"complete"`
	Offset    int    `json:"offset"`
	Content   string `json:"content"`
}
type ToolError struct {
	Code      string `json:"code"`
	Message   string `json:"message"`
	Retryable bool   `json:"retryable"`
}
type ToolPreview struct {
	Texts     []string   `json:"texts"`
	Artifacts []string   `json:"artifacts"`
	Images    []string   `json:"images"`
	Error     *ToolError `json:"error,omitempty"`
}

// Package contract contains the repository-private consumer DTOs only.
package contract

const Contract = "steward.web-extract/1"
const EngineContract = "steward.web-extract-engine/1"
const ModuleFile = "extract.mjs"
const UnavailableRuntime = "web-parser-v1:unavailable"

type Config struct {
	RuntimeRevision  string `json:"runtime_revision"`
	SourceIdentity   string `json:"source_identity"`
	UnavailableError string `json:"unavailable_error,omitempty"`
}
type Request struct {
	InputID   string `json:"input_id"`
	URL       string `json:"url"`
	MediaType string `json:"media_type"`
}
type Metadata struct {
	TitleBytes int    `json:"title_bytes"`
	TextBytes  int    `json:"text_bytes"`
	ErrorCode  string `json:"error_code"`
}
type Plan struct {
	Metadata
	Digest string `json:"digest"`
}

// Package contract defines private experimental image OCR service messages.
// Artifact identity and output persistence are deliberately not exposed.
package contract

const PluginID = "steward.image-ocr"
const Contract = "steward.image-ocr/1"
const TSVContract = "steward.ocr-tsv/1"
const EngineContract = "steward.ocr-engine/1"

type Request struct {
	InputID   string   `json:"input_id"`
	MediaType string   `json:"media_type"`
	Languages []string `json:"languages"`
}
type EngineRequest struct {
	InputID   string   `json:"input_id"`
	MediaType string   `json:"media_type"`
	Languages []string `json:"languages"`
	Layout    int      `json:"layout"`
}
type Metadata struct {
	Width     int      `json:"width"`
	Height    int      `json:"height"`
	Languages []string `json:"languages"`
	ErrorCode string   `json:"error_code,omitempty"`
}
type TSVRequest struct {
	InputID   string   `json:"input_id"`
	Width     int      `json:"width"`
	Height    int      `json:"height"`
	Languages []string `json:"languages"`
}
type Response struct {
	Document  *Document `json:"document,omitempty"`
	ErrorCode string    `json:"error_code,omitempty"`
}

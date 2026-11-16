// Package contract defines the experimental DOCX feature seam shared by Host and plugin.
package contract

const PluginID = "steward.docx-reader"
const Contract = "steward.docx-text/1"

const InputSchema = `{"type":"object","properties":{"input_id":{"type":"string","minLength":1,"maxLength":128}},"required":["input_id"],"additionalProperties":false}`
const OutputSchema = `{"type":"object","properties":{"text":{"type":"string","maxLength":262144},"error_code":{"type":"string","enum":["INVALID_ARGUMENT","RESOURCE_LIMIT"]}},"additionalProperties":false}`

type Request struct {
	InputID string `json:"input_id"`
}
type Response struct {
	Text      string `json:"text,omitempty"`
	ErrorCode string `json:"error_code,omitempty"`
}

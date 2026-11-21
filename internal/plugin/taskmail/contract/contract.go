// Package contract is the experimental task-mail parser seam. Recipient and
// task authority come from the Host's delivery envelope, never the message.
package contract

const PluginID = "steward.task-mail"
const Service = "steward.task-mail-parse/1"
const MaxBytes = 256 << 10

type Message struct {
	MessageID string `json:"message_id,omitempty"`
	From      string `json:"from,omitempty"`
	Subject   string `json:"subject,omitempty"`
	Text      string `json:"text,omitempty"`
	Error     string `json:"error,omitempty"`
}

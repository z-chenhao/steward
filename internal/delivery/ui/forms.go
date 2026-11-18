package ui

import (
	"encoding/json"
	"html/template"
	"strings"
	"unicode/utf8"

	"github.com/z-chenhao/assistant/internal/conversation"
	"github.com/z-chenhao/assistant/internal/delivery/uidocv1"
)

func validFields(fields []Field) bool {
	if len(fields) == 0 || len(fields) > 16 {
		return false
	}
	seen := map[string]bool{}
	for _, f := range fields {
		if !uidocv1.ValidFieldName(f.Name) || seen[f.Name] || strings.TrimSpace(f.Label) == "" || len(f.Label) > 256 || len(f.Value) > 4096 {
			return false
		}
		seen[f.Name] = true
	}
	return true
}

// Form resolves only a Host-bound component in the authoritative projection.
func Form(raw json.RawMessage, messageID, formID string) (Component, error) {
	var spec Spec
	if len(messageID) == 0 || len(messageID) > 256 || len(formID) != 64 || json.Unmarshal(raw, &spec) != nil || spec.SchemaVersion != Version {
		return Component{}, conversation.ErrInvalid
	}
	for _, c := range spec.Components {
		if Respondable(c) && c.MessageID == messageID && c.FormID == formID && ValidComponent(c) {
			return c, nil
		}
	}
	return Component{}, conversation.ErrConflict
}

// Answer retains the native form-v2 protocol and Runs whose frozen graph
// predates steward.ui-form/1. Managed form answers use Runs.AcceptWebForm.
// Remove the named native catalog branches after those retained Runs expire.
func Answer(c Component, values map[string]string) ([]conversation.ContentPart, error) {
	if c.Type == "openintelligentui" && c.Name == "steward.form" {
		return extensionFormAnswer(c, values)
	}
	if c.Type == "openintelligentui" && c.Name == "scheduleTime" {
		return meetingAnswer(c, values)
	}
	if c.Type != "form" || !ValidComponent(c) || len(values) != len(c.Fields) {
		return nil, conversation.ErrInvalid
	}
	// Separate semantic parts preserve field boundaries without exposing a
	// transport envelope or making multiline values ambiguous on recovery.
	parts := conversation.Text("Response to: " + c.Text + "\n")
	size := len(parts[0].Text)
	for _, f := range c.Fields {
		v, ok := values[f.Name]
		// Native HTML form encoding uses CRLF; JavaScript textarea values use
		// LF. Normalize before validation and hashing so cross-mode retries
		// represent the same accepted answer.
		v = strings.ReplaceAll(strings.ReplaceAll(v, "\r\n", "\n"), "\r", "\n")
		if !ok || strings.TrimSpace(v) == "" || len(v) > 4096 || !utf8.ValidString(v) || strings.ContainsRune(v, 0) {
			return nil, conversation.ErrInvalid
		}
		text := f.Label + ": " + v + "\n"
		parts = append(parts, conversation.ContentPart{Type: "text", Text: text})
		size += len(text)
	}
	if size > 16<<10 {
		return nil, conversation.ErrInvalid
	}
	return parts, nil
}

// Restore displays only answers already accepted through the ordinary ingress.
// Native catalog branches serve only the same historical boundary as Answer;
// current managed forms restore their atomically accepted Host result.
func Restore(c *Component, content []conversation.ContentPart) error {
	if c.Type == "openintelligentui" && c.Name == "steward.form" {
		return restoreExtensionForm(c, content)
	}
	if c.Type == "openintelligentui" && c.Name == "scheduleTime" {
		return restoreMeeting(c, content)
	}
	if len(content) != len(c.Fields)+1 || content[0].Type != "text" || content[0].Text != "Response to: "+c.Text+"\n" {
		return conversation.ErrConflict
	}
	values := make([]string, len(c.Fields))
	for i, f := range c.Fields {
		p := content[i+1]
		value, ok := strings.CutPrefix(p.Text, f.Label+": ")
		value, ended := strings.CutSuffix(value, "\n")
		if p.Type != "text" || !ok || !ended || len(value) > 4096 || !utf8.ValidString(value) {
			return conversation.ErrConflict
		}
		values[i] = value
	}
	for i, value := range values {
		c.Fields[i].Value = value
	}
	c.Submitted = true
	return nil
}

var formComponent = template.Must(template.New("form").Parse(`{{$form:=.}}<form method="post" action="/ui/form" class="rich-answer-form" data-form-id="{{.FormID}}" data-submitted="{{.Submitted}}"><p class="semantic">{{.Text}}</p><input type="hidden" name="message_id" value="{{.MessageID}}"><input type="hidden" name="form_id" value="{{.FormID}}"><fieldset {{if or .Submitted .Disabled}}disabled{{end}}>{{range .Fields}}<div class="rich-field"><label for="{{$form.FormID}}-{{.Name}}">{{.Label}}</label><textarea id="{{$form.FormID}}-{{.Name}}" name="field.{{.Name}}" required maxlength="4096" rows="2">{{.Value}}</textarea></div>{{end}}<button type="submit">Send response</button></fieldset><p class="field-help" role="status" aria-live="polite">{{if .Submitted}}Saved.{{else if .Disabled}}Unavailable.{{else}}{{end}}</p></form>`))

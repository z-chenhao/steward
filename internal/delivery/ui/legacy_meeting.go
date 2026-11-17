package ui

import (
	"encoding/json"
	"fmt"
	"html/template"
	"strings"
)

// Only original UI releases before 1.25 and pre-managed historical projections
// use this interpretation. Delete after their delivered references and in-flight
// Runs retire; missing/corrupt new receipts never select it.
func legacyMeetingHTML(c Component) string {
	var p nativeProps
	_ = json.Unmarshal(c.Props, &p)
	disabled := ""
	if c.Disabled || c.Submitted {
		disabled = " disabled"
	}
	var out strings.Builder
	fmt.Fprintf(&out, `<form class="native-meeting-fallback" method="post" action="/ui/form" data-form-id="%s" data-submitted="%t"><p>%s</p><input type="hidden" name="message_id" value="%s"><input type="hidden" name="form_id" value="%s"><fieldset%s>`, c.FormID, c.Submitted, template.HTMLEscapeString(p.Reason), template.HTMLEscapeString(c.MessageID), c.FormID, disabled)
	for i, slot := range p.TimeSlots {
		fmt.Fprintf(&out, `<button name="field.choice" value="%d">%s · %s (%g min)</button>`, i, template.HTMLEscapeString(slot.Date), template.HTMLEscapeString(slot.Time), p.Duration)
	}
	out.WriteString(`<button name="field.choice" value="decline">None of these work</button></fieldset>`)
	if c.Submitted {
		out.WriteString(`<p role="status">Time preference saved.</p>`)
	}
	out.WriteString(`</form>`)
	return out.String()
}

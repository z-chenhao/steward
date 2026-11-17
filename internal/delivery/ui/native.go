package ui

import (
	"encoding/json"
	"fmt"
	"html/template"
	"strconv"
	"strings"

	"github.com/z-chenhao/assistant/internal/conversation"
	"github.com/z-chenhao/assistant/internal/tool"
)

type nativeProps struct {
	Title       string     `json:"title"`
	Description string     `json:"description"`
	Columns     []string   `json:"columns"`
	Rows        [][]string `json:"rows"`
	Source      string     `json:"source"`
	Data        []struct {
		Label string  `json:"label"`
		Value float64 `json:"value"`
	} `json:"data"`
	Approach    string   `json:"approach"`
	Technology  string   `json:"technology"`
	KeyElements []string `json:"key_elements"`
	Reason      string   `json:"reasonForScheduling"`
	Duration    float64  `json:"meetingDuration"`
	TimeSlots   []struct {
		Date     string `json:"date"`
		Time     string `json:"time"`
		Duration string `json:"duration"`
	} `json:"timeSlots"`
}

func validNative(c Component) bool {
	raw, err := json.Marshal(struct {
		Name  string          `json:"name"`
		Props json.RawMessage `json:"props"`
	}{c.Name, c.Props})
	if err != nil || !tool.Validate(NativeSchema, string(raw)) {
		return false
	}
	var p nativeProps
	if json.Unmarshal(c.Props, &p) != nil {
		return false
	}
	if strings.HasPrefix(c.Name, "steward.") {
		return validExtension(c)
	}
	switch c.Name {
	case "generateSandboxedUi":
		var visual SandboxedUI
		if json.Unmarshal(c.Props, &visual) != nil || strings.TrimSpace(visual.HTML) == "" {
			return false
		}
	case "Table":
		for _, r := range p.Rows {
			if len(r) != len(p.Columns) {
				return false
			}
		}
	case "pieChart":
		total := 0.0
		for _, d := range p.Data {
			if d.Value < 0 {
				return false
			}
			total += d.Value
		}
		if len(p.Data) > 0 && total <= 0 {
			return false
		}
	case "scheduleTime":
		if p.Duration <= 0 || strings.TrimSpace(p.Reason) == "" {
			return false
		}
		seen := map[string]bool{}
		for _, slot := range p.TimeSlots {
			key := slot.Date + "\x00" + slot.Time
			if strings.TrimSpace(slot.Date) == "" || strings.TrimSpace(slot.Time) == "" || seen[key] {
				return false
			}
			seen[key] = true
		}
	}
	return true
}

func nativeSummary(c Component) string {
	if strings.HasPrefix(c.Name, "steward.") {
		return extensionSummary(c)
	}
	var p nativeProps
	if json.Unmarshal(c.Props, &p) != nil {
		return ""
	}
	lines := []string{p.Title}
	switch c.Name {
	case "Table":
		lines = append(lines, strings.Join(p.Columns, " | "))
		for _, r := range p.Rows {
			lines = append(lines, strings.Join(r, " | "))
		}
		lines = append(lines, p.Source)
	case "barChart", "pieChart":
		lines = append(lines, p.Description)
		for _, d := range p.Data {
			lines = append(lines, fmt.Sprintf("%s: %g", d.Label, d.Value))
		}
	case "plan_visualization":
		lines = []string{p.Technology, p.Approach}
		lines = append(lines, p.KeyElements...)
	case "scheduleTime":
		lines = append(lines, p.Reason)
		for _, slot := range p.TimeSlots {
			lines = append(lines, fmt.Sprintf("%s %s (%g min)", slot.Date, slot.Time, p.Duration))
		}
	}
	return strings.TrimSpace(strings.Join(lines, "\n"))
}

func Respondable(c Component) bool {
	return c.Type == "form" || c.Type == "openintelligentui" && (c.Name == "scheduleTime" || c.Name == "steward.form")
}

func meetingAnswer(c Component, values map[string]string) ([]conversation.ContentPart, error) {
	if !validNative(c) || len(values) != 1 {
		return nil, conversation.ErrInvalid
	}
	value, ok := values["choice"]
	if !ok {
		return nil, conversation.ErrInvalid
	}
	var p nativeProps
	_ = json.Unmarshal(c.Props, &p)
	if value == "decline" {
		return conversation.Text("Meeting time preference for " + p.Reason + ": none of the proposed times work. Please suggest alternatives."), nil
	}
	i, err := strconv.Atoi(value)
	if err != nil || i < 0 || i >= len(p.TimeSlots) || strconv.Itoa(i) != value {
		return nil, conversation.ErrInvalid
	}
	slot := p.TimeSlots[i]
	return conversation.Text(fmt.Sprintf("Meeting time preference for %s: %s at %s (%g min). This selects a preferred time; it does not book a meeting.", p.Reason, slot.Date, slot.Time, p.Duration)), nil
}

func restoreMeeting(c *Component, content []conversation.ContentPart) error {
	var p nativeProps
	_ = json.Unmarshal(c.Props, &p)
	choices := []string{"decline"}
	for i := range p.TimeSlots {
		choices = append(choices, strconv.Itoa(i))
	}
	for _, choice := range choices {
		want, err := meetingAnswer(*c, map[string]string{"choice": choice})
		if err == nil && Hash(want) == Hash(content) {
			c.Response = choice
			c.Submitted = true
			return nil
		}
	}
	return conversation.ErrConflict
}

func nativeHTML(c Component) string {
	if c.Name == "steward.form" {
		return extensionFormHTML(c)
	}
	if c.Name != "scheduleTime" || c.FormID == "" {
		return `<p class="semantic">` + template.HTMLEscapeString(c.Text) + `</p>`
	}
	if c.MeetingManaged {
		if c.MeetingView == nil {
			return `<p class="semantic">` + template.HTMLEscapeString(c.Text) + `</p>`
		}
		return meetingViewHTML(c)
	}
	return legacyMeetingHTML(c)
}

// Fixed escaped form driver; owning labels and original choices arrive from
// the accepted normalizer receipt. No plugin code runs in the browser.
func meetingViewHTML(c Component) string {
	v := c.MeetingView
	esc := template.HTMLEscapeString
	disabled := ""
	if c.Disabled || c.Submitted {
		disabled = " disabled"
	}
	var out strings.Builder
	fmt.Fprintf(&out, `<form class="native-meeting-fallback" method="post" action="/ui/form" data-form-id="%s" data-submitted="%t"><p>%s</p><input type="hidden" name="message_id" value="%s"><input type="hidden" name="form_id" value="%s"><fieldset%s>`, esc(c.FormID), c.Submitted, esc(v.Title), esc(c.MessageID), esc(c.FormID), disabled)
	for _, slot := range v.Slots {
		fmt.Fprintf(&out, `<button name="field.choice" value="%d">%s · %s (%s)</button>`, slot.Choice, esc(slot.Date), esc(slot.Time), esc(slot.Duration))
	}
	fmt.Fprintf(&out, `<button name="field.choice" value="decline">%s</button></fieldset>`, esc(v.DeclineLabel))
	if c.Submitted {
		fmt.Fprintf(&out, `<p role="status">%s</p>`, esc(v.SavedLabel))
	}
	out.WriteString(`</form>`)
	return out.String()
}

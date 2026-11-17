package web

import (
	"bytes"
	"encoding/json"
	"fmt"
	"github.com/z-chenhao/assistant/internal/llm"
	"github.com/z-chenhao/assistant/internal/observability"
	"strings"
	"time"
)

func prettyBody(body string) string {
	var formatted bytes.Buffer
	// Indent tokens directly: decoding into a map loses duplicate fields and
	// changes numeric or escape representations in recorded evidence.
	if json.Indent(&formatted, []byte(body), "", "  ") != nil {
		return body
	}
	return formatted.String()
}

func duration(start int64, end *int64) string {
	if end == nil {
		return "—"
	}
	return elapsedLabel(float64(max(0, *end-start)) / 1000)
}

// elapsedLabel is shared by all server-rendered Web latency metrics.
func elapsedLabel(ms float64) string {
	ms = max(0, ms)
	if ms < 1000 {
		return fmt.Sprintf("%.2fms", ms)
	}
	return fmt.Sprintf("%.2fs", ms/1000)
}

func milliseconds(value *int64) string {
	if value == nil {
		return "—"
	}
	return elapsedLabel(float64(*value))
}

func elapsed(created, ended string) string {
	if ended == "" {
		return "—"
	}
	a, e := time.Parse(time.RFC3339Nano, created)
	b, f := time.Parse(time.RFC3339Nano, ended)
	if e != nil || f != nil {
		return "—"
	}
	return elapsedLabel(float64(b.Sub(a)) / float64(time.Millisecond))
}

func money(m int64) string {
	if m > 0 && m < 10000 {
		m = 10000
	}
	return fmt.Sprintf("%.2f", float64(m)/1e6)
}
func viewJSON(v any) string { body, _ := json.MarshalIndent(v, "", "  "); return string(body) }

// Decode authorized frozen messages without rewriting their source identity.
// Sealed Memory remains redacted; current Memory is never substituted.
func completeMessages(body string) ([]observability.ChatPreview, bool) {
	var saved []llm.Message
	if json.Unmarshal([]byte(body), &saved) != nil || !strings.HasPrefix(strings.TrimSpace(body), "[") {
		return nil, false
	}
	out := make([]observability.ChatPreview, 0, len(saved))
	for i, m := range saved {
		text := m.Text
		if m.Memory != nil {
			if text != "" {
				text += "\n"
			}
			text += "[Encrypted memory content; resolved before model transport. Not shown in this view.]"
		}
		row := observability.ChatPreview{Index: i, Role: m.Role, Text: text, CallID: m.ToolCallID, Images: m.ImageRefs}
		if m.Source != nil {
			row.Source = m.Source.Kind
		}
		if len(m.ToolCalls) > 0 {
			raw, _ := json.Marshal(m.ToolCalls)
			row.Calls = string(raw)
		}
		out = append(out, row)
	}
	return out, true
}

// tokenRate formats an admitted display value; it does not select evidence.
func tokenRate(rate *float64) string {
	if rate == nil {
		return "—"
	}
	return fmt.Sprintf("%.2f tok/s", *rate)
}
func totalInput(m observability.Metrics) int64 { return m.Input + m.CacheRead + m.CacheWrite }

func traceRangeURL(v *traceView, since, until string) string {
	copy := *v
	copy.Query.Cursor = ""
	copy.Query.Since, _ = time.Parse(time.RFC3339Nano, since)
	copy.Query.Until, _ = time.Parse(time.RFC3339Nano, until)
	return traceListURL(&copy)
}

func traceSpan(start, end int64) string { return duration(start, &end) }

// Primary UI groups can evolve while an archived reader keeps its logical
// evidence destinations. This is presentation, never source authorization.
func traceHasView(v *traceView, name string) bool {
	if v.EvidenceTabs == nil {
		return true
	}
	for _, tab := range v.EvidenceTabs {
		if tab == name {
			return true
		}
	}
	return false
}

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

func providerTimingLabel(phase string) string {
	return map[string]string{
		"started":             "HTTP request sent",
		"headers":             "Response headers received",
		"error":               "HTTP request failed",
		"first_body":          "First response data",
		"first_text":          "First model text",
		"first_tool_call":     "First model tool call",
		"stream_terminal":     "Provider stream terminal event",
		"header_timeout":      "Response headers timed out",
		"stream_idle_timeout": "Response stream idle timeout",
		"generation_deadline": "Generation time limit reached",
	}[phase]
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

func traceRows(tree observability.ObservationTree) []traceRow {
	present := map[string]bool{}
	children := map[string][]observability.Observation{}
	for _, o := range tree.Items {
		present[o.ID] = true
		children[o.Parent] = append(children[o.Parent], o)
	}
	start, end := traceTimeRange(tree)
	span := max(int64(1), end-start)
	rows := []traceRow{}
	seen := map[string]bool{}
	var visit func(observability.Observation, int, []bool)
	visit = func(o observability.Observation, depth int, guides []bool) {
		if seen[o.ID] {
			return
		}
		seen[o.ID] = true
		x := max(0, min(990, float64(o.Started-start)/float64(span)*1000))
		width := float64(4)
		if o.Ended != nil {
			width = max(4, min(1000-x, float64(*o.Ended-o.Started)/float64(span)*1000))
		}
		running := o.Ended == nil && tree.Trace.EndedAt == "" && (o.Type == "agent" || o.Type == "generation" && o.Status == "pending" || o.Type == "tool" && (o.Status == "running" || o.Status == "queued"))
		if running {
			width = max(0, min(1000-x, float64(end-o.Started)/float64(span)*1000))
		}
		rows = append(rows, traceRow{Running: running, Observation: o, Depth: min(16, depth), ParentOutside: o.Parent != "" && !present[o.Parent], HasChildren: len(children[o.ID]) > 0, Guides: append([]bool(nil), guides[:min(16, len(guides))]...), X: x, Width: width})
		for i, child := range children[o.ID] {
			visit(child, depth+1, append(append([]bool(nil), guides...), i < len(children[o.ID])-1))
		}
	}
	for _, o := range tree.Items {
		if o.Parent == "" || !present[o.Parent] {
			visit(o, 0, nil)
		}
	}
	for _, o := range tree.Items {
		visit(o, 0, nil)
	}
	return rows
}

func observationRole(name string) string {
	switch name {
	case "Main":
		return "main"
	case "Root Worker":
		return "root"
	case "Child Worker":
		return "child"
	}
	return ""
}
func observationIcon(kind string) string {
	switch kind {
	case "generation":
		return "✧"
	case "agent":
		return "◈"
	case "tool":
		return "⌁"
	case "retriever":
		return "⌕"
	case "chain":
		return "◇"
	case "event":
		return "•"
	}
	return "↔"
}
func observationParent(id string) string { return strings.TrimPrefix(id, "agent:") }
func money(m int64) string {
	if m > 0 && m < 10000 {
		m = 10000
	}
	return fmt.Sprintf("%.2f", float64(m)/1e6)
}
func viewJSON(v any) string { body, _ := json.MarshalIndent(v, "", "  "); return string(body) }

// Tabs are meaningful views of the selected node, never empty destinations.
func observationTabs(o observability.Observation) []string {
	return []string{"overview", "metadata"}
}

// Noise filtering is a presentation choice only. Keep the exact selected node
// and its ancestors, failed steps, waits, retrieval and actual compaction.
func primaryObservations(tree observability.ObservationTree) ([]observability.Observation, int) {
	byID := map[string]observability.Observation{}
	keep := map[string]bool{}
	for _, o := range tree.Items {
		byID[o.ID] = o
	}
	for _, o := range tree.Items {
		internal := o.Name == "Prepare context" || o.Name == "Provider HTTP" || o.Name == "Execute tool" || o.Name == "Enter / resume Agent" || o.Name == "Agent finished"
		if internal && o.ID != tree.Selected.ID && o.Status != "failed" && o.Status != "http_error" {
			continue
		}
		keep[o.ID] = true
		parent := o.Parent
		for n := 0; parent != "" && n < len(tree.Items); n++ {
			if keep[parent] {
				break
			}
			keep[parent] = true
			p, ok := byID[parent]
			if !ok {
				break
			}
			parent = p.Parent
		}
	}
	out := []observability.Observation{}
	for _, o := range tree.Items {
		if keep[o.ID] {
			out = append(out, o)
		}
	}
	return out, len(tree.Items) - len(out)
}

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

// Throughput uses only the successful terminal attempt's first meaningful
// output and stream terminal. Acceptance/DB latency and other retries are not
// model decoding time. Missing or contradictory evidence stays unknown.
func generationOutputRate(tree observability.ObservationTree) *float64 {
	if tree.Generation == nil || tree.Generation.Metrics.KnownUsage == 0 || tree.Selected.Status != "completed" {
		return nil
	}
	starts := map[string]int64{}
	first := map[string]int64{}
	terminal := map[string]int64{}
	for _, m := range tree.ProviderTimings {
		if m.Phase == "started" {
			starts[m.Attempt] = m.At
		}
	}
	for _, m := range tree.ProviderTimings {
		at, ok := starts[m.Attempt]
		if !ok || m.At < at {
			continue
		}
		if m.Phase == "first_text" || m.Phase == "first_tool_call" {
			if first[m.Attempt] == 0 || m.At < first[m.Attempt] {
				first[m.Attempt] = m.At
			}
		}
		if m.Phase == "stream_terminal" {
			terminal[m.Attempt] = m.At
		}
	}
	var last int64
	var rate *float64
	for attempt, end := range terminal {
		begin := first[attempt]
		if begin > 0 && end > begin && end > last {
			v := float64(tree.Generation.Metrics.Output) * 1e6 / float64(end-begin)
			rate = &v
			last = end
		}
	}
	return rate
}

func tokenRate(rate *float64) string {
	if rate == nil {
		return "—"
	}
	return fmt.Sprintf("%.2f tok/s", *rate)
}
func totalInput(m observability.Metrics) int64 { return m.Input + m.CacheRead + m.CacheWrite }

func observationKind(o observability.Observation) string {
	if o.Name == "Provider HTTP" {
		return "http"
	}
	if o.Type == "span" && (o.Timing == "approval wait" || o.Timing == "observed wait") {
		return "wait"
	}
	return o.Type
}

func traceTimeRange(tree observability.ObservationTree) (int64, int64) {
	start, _ := time.Parse(time.RFC3339Nano, tree.Trace.CreatedAt)
	end := time.Now()
	if tree.Trace.EndedAt != "" {
		end, _ = time.Parse(time.RFC3339Nano, tree.Trace.EndedAt)
	}
	return start.UnixMicro(), max(start.UnixMicro()+1, end.UnixMicro())
}
func traceRangeURL(v *traceView, since, until string) string {
	copy := *v
	copy.Query.Cursor = ""
	copy.Query.Since, _ = time.Parse(time.RFC3339Nano, since)
	copy.Query.Until, _ = time.Parse(time.RFC3339Nano, until)
	return traceListURL(&copy)
}

func activityHeight(count int64, buckets []observability.TraceBucket) float64 {
	maximum := int64(1)
	for _, b := range buckets {
		maximum = max(maximum, b.Count)
	}
	return float64(count) / float64(maximum) * 100
}
func traceSpan(start, end int64) string { return duration(start, &end) }

func activityOffset(count int64, buckets []observability.TraceBucket) float64 {
	return 100 - activityHeight(count, buckets)
}

func activityX(bucket observability.TraceBucket, buckets []observability.TraceBucket) float64 {
	if len(buckets) == 0 {
		return 0
	}
	start, _ := time.Parse(time.RFC3339Nano, buckets[0].At)
	end, _ := time.Parse(time.RFC3339Nano, buckets[len(buckets)-1].Until)
	at, _ := time.Parse(time.RFC3339Nano, bucket.At)
	if !end.After(start) {
		return 0
	}
	return float64(at.UnixMicro()-start.UnixMicro()) / float64(end.UnixMicro()-start.UnixMicro()) * 1000
}
func activityWidth(bucket observability.TraceBucket, buckets []observability.TraceBucket) float64 {
	end := bucket
	end.At = bucket.Until
	return max(0, activityX(end, buckets)-activityX(bucket, buckets))
}

package web

import (
	"github.com/z-chenhao/assistant/internal/observability"
	"strings"
	"time"
)

// Historical Trace1.0.1 and explicit never-managed direct readers only.
// Managed templates own these policies; missing candidate data never calls here.
// Remove with the named historical template consumers.

func legacyTraceProviderTimingLabelV1(phase string) string {
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

func legacyTraceColumnNamesV1() []string {
	return []string{"Trace ID", "Created time", "Type", "Status", "Output", "Wall time", "TTFT", "Model", "Tokens", "Model cost"}
}

func legacyTraceStatusesV1() []string {
	return []string{"queued", "running", "waiting", "completed", "failed", "cancelled"}
}

func legacyTraceLabelV1(trace observability.Trace) string {
	if trace.Role == "main" && strings.TrimSpace(trace.Input) != "" {
		return trace.Input
	}
	return trace.Title
}

func legacyTraceRoleLabelV1(role string) string {
	switch role {
	case "main":
		return "Main"
	case "root":
		return "Root Worker"
	case "child":
		return "Child Worker"
	}
	return "Execution"
}

func legacyTraceTabLabelV1(name string) string {
	switch name {
	case "overview":
		return "Input / Output"
	case "metadata":
		return "Details"
	case "context":
		return "Context"
	case "wire":
		return "Requests"
	case "diff":
		return "Compare"
	}
	return name
}

func legacyTraceObservationRoleV1(name string) string {
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

func legacyTraceObservationParentV1(id string) string { return strings.TrimPrefix(id, "agent:") }

func legacyTraceObservationKindV1(o observability.Observation) string {
	if o.Name == "Provider HTTP" {
		return "http"
	}
	if o.Type == "span" && (o.Timing == "approval wait" || o.Timing == "observed wait") {
		return "wait"
	}
	return o.Type
}

func legacyTraceActivityHeightV1(count int64, buckets []observability.TraceBucket) float64 {
	maximum := int64(1)
	for _, b := range buckets {
		maximum = max(maximum, b.Count)
	}
	return float64(count) / float64(maximum) * 100
}

func legacyTraceActivityOffsetV1(count int64, buckets []observability.TraceBucket) float64 {
	return 100 - legacyTraceActivityHeightV1(count, buckets)
}

func legacyTraceActivityXV1(bucket observability.TraceBucket, buckets []observability.TraceBucket) float64 {
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

func legacyTraceActivityWidthV1(bucket observability.TraceBucket, buckets []observability.TraceBucket) float64 {
	end := bucket
	end.At = bucket.Until
	return max(0, legacyTraceActivityXV1(end, buckets)-legacyTraceActivityXV1(bucket, buckets))
}

func legacyTraceTaskActionV1(operation string) string {
	switch operation {
	case "create":
		return "Created Task"
	case "append":
		return "Appended instruction"
	case "pause":
		return "Paused Task"
	case "cancel":
		return "Cancelled Task"
	case "complete":
		return "Completed Task"
	}
	return "Recorded Task action"
}

func legacyTracePrimaryTabsV1(o observability.Observation) []string {
	return []string{"overview", "metadata"}
}

func legacyTraceHasPreviousV1(step *int64) bool { return step != nil && *step > 0 }

func legacyTraceRowsV1(tree observability.ObservationTree) []traceRow {
	present := map[string]bool{}
	children := map[string][]observability.Observation{}
	for _, o := range tree.Items {
		present[o.ID] = true
		children[o.Parent] = append(children[o.Parent], o)
	}
	start, end := legacyTraceTimeRangeV1(tree)
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

func legacyTracePrimaryObservationsV1(tree observability.ObservationTree) ([]observability.Observation, int) {
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

func legacyTraceTimeRangeV1(tree observability.ObservationTree) (int64, int64) {
	start, _ := time.Parse(time.RFC3339Nano, tree.Trace.CreatedAt)
	end := time.Now()
	if tree.Trace.EndedAt != "" {
		end, _ = time.Parse(time.RFC3339Nano, tree.Trace.EndedAt)
	}
	return start.UnixMicro(), max(start.UnixMicro()+1, end.UnixMicro())
}

// Released1.0.1 projections and explicit never-managed direct readers only.
// Current managed readers are rejected before this boundary when data is absent.
func legacyTracePresentationRowsV1(tree observability.ObservationTree, presentation *observability.TracePresentation, showInternal bool) (rows []traceRow, hidden int, start, end int64) {
	visible := tree
	if !showInternal {
		visible.Items, hidden = legacyTracePrimaryObservationsV1(tree)
	}
	start, end = legacyTraceTimeRangeV1(tree)
	if presentation == nil {
		return legacyTraceRowsV1(visible), hidden, start, end
	}
	keep, parents := map[string]bool{}, map[string]bool{}
	for _, o := range visible.Items {
		keep[o.ID] = true
		parents[o.Parent] = true
	}
	for _, row := range presentation.Rows {
		if !keep[row.ID] {
			continue
		}
		row.HasChildren = row.HasChildren && parents[row.ID]
		row.Running = row.Ended == nil && tree.Trace.EndedAt == "" && (row.Type == "agent" || row.Type == "generation" && row.Status == "pending" || row.Type == "tool" && (row.Status == "running" || row.Status == "queued"))
		if row.Running {
			row.Width = max(0, min(1000-row.X, float64(end-row.Started)/float64(max(int64(1), end-start))*1000))
		}
		rows = append(rows, row)
	}
	return rows, hidden, start, end
}

func legacyTraceGenerationOutputRateV1(tree observability.ObservationTree) *float64 {
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

func legacyTraceTimingV1(tree observability.ObservationTree, presentation *observability.TracePresentation) (ttft *int64, first *int64) {
	var started, firstText int64
	starts := map[string]int64{}
	for _, mark := range tree.ProviderTimings {
		if mark.Phase == "started" {
			starts[mark.Attempt] = mark.At
			if started == 0 || mark.At < started {
				started = mark.At
			}
		}
	}
	for _, mark := range tree.ProviderTimings {
		if at, ok := starts[mark.Attempt]; ok && (mark.Phase == "first_text" || mark.Phase == "first_tool_call") && mark.At >= at && (firstText == 0 || mark.At < firstText) {
			firstText = mark.At
		}
	}
	if started > 0 && firstText >= started {
		ms := (firstText - started) / 1000
		ttft = &ms
	}
	if presentation != nil {
		// v1 readers measure first text. A recorded earlier tool delta is
		// additional first-output evidence introduced by the current UI.
		toolFirst := false
		for _, mark := range tree.ProviderTimings {
			toolFirst = toolFirst || (mark.At == firstText && mark.Phase == "first_tool_call")
		}
		if !toolFirst {
			ttft = presentation.TTFTMS
		}
	}

	if firstText > 0 {
		first = &firstText
	}
	return ttft, first
}

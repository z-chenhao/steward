package web

import (
	"embed"
	"html/template"
	"time"

	"github.com/z-chenhao/assistant/internal/task"
)

//go:embed templates/*.html
var templates embed.FS

func newLayout(language string) *template.Template {
	return template.Must(template.New("layout.html").Funcs(template.FuncMap{
		"tr":                  func(text string) string { return translate(language, text) },
		"fontURL":             fontURL,
		"help":                help,
		"identifier":          identifier,
		"memorySourcesOffset": memorySourcesOffset, "memoryOffset": memoryOffset, "memoryURL": memoryURL, "memorySourceLabel": memorySourceLabel, "memoryOriginLabel": memoryOriginLabel, "memoryStatus": memoryStatus, "memoryLayerLabel": memoryLayerLabel,
		"capabilityURL": capabilityURL,
		"traceColumns": func() []string {
			return []string{"Trace ID", "Created time", "Type", "Status", "Output", "Wall time", "TTFT", "Model", "Tokens", "Model cost"}
		},
		"traceRangeURL": traceRangeURL, "activityHeight": activityHeight, "activityOffset": activityOffset, "activityX": activityX, "activityWidth": activityWidth, "traceSpan": traceSpan, "traceLabel": traceLabel, "traceURL": traceURL, "traceListURL": traceListURL, "traceClearURL": traceClearURL, "tracePresetURL": tracePresetURL,
		"canSend":       canSend,
		"bindingTarget": bindingTarget, "toolLabel": toolLabel,
		"taskConfigurationPage": taskConfigurationPage, "taskURL": taskURL, "executionURL": executionURL, "taskTitle": task.Title, "taskListURL": taskListURL, "taskPageCount": taskPageCount, "historyLabel": historyLabel,
		"scheduleTime":      func(raw, zone string) string { return localScheduleTime(language, raw, zone) },
		"scheduleFrequency": scheduleFrequency,
		"conversationURL":   conversationURL, "tabLabel": tabLabel,
		"prettyBody":          prettyBody,
		"providerTimingLabel": providerTimingLabel,
		"duration":            duration,
		"elapsed":             elapsed,
		"milliseconds":        milliseconds,
		"obsStatuses":         func() []string { return []string{"queued", "running", "waiting", "completed", "failed", "cancelled"} },
		"obsKind":             observationKind,
		"obsRole":             observationRole,
		"parentRun":           observationParent,
		"obsIcon":             observationIcon,

		"money":       money,
		"hasPrevious": func(step *int64) bool { return step != nil && *step > 0 }, "tokenRate": tokenRate, "totalInput": totalInput,
		"obsJSON":         viewJSON,
		"authURL":         AuthURL,
		"runRole":         runRole,
		"taskAction":      taskAction,
		"indent":          treeIndent,
		"contextSections": func() []string { return []string{"messages", "tools", "response", "provenance", "recall"} },
		"contextURL":      contextURL,
		"contextLink":     contextLink,
		"partEnd":         func(v *contextView) int { return v.Query.Offset + len(v.Frozen.Content) },
	}).Funcs(timeFunctions(time.Local, language)).ParseFS(templates, "templates/*.html"))

}

// Keep unexecuted sources for request-local function bindings.
var layoutSource = newLayout("en")
var chineseLayoutSource = newLayout("zh-CN")
var layout = template.Must(layoutSource.Clone())

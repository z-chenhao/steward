package web

import (
	"embed"
	"html/template"
	"time"

	"github.com/z-chenhao/assistant/internal/plugin/appsinterface/contract"
	"github.com/z-chenhao/assistant/internal/task"
)

//go:embed templates/*.html legacy-ui/memory-v1/memory.html legacy-ui/trace-v1/* legacy-ui/workbench-v1/*.html
var templates embed.FS

func newLayout(language string) *template.Template {
	var parsed, atoms *template.Template
	parsed = template.Must(template.New("layout.html").Funcs(template.FuncMap{
		"appIconView": func(v *appsView, c appCard) (template.HTML, error) { return appPrimitive(atoms, "app-icon", c, v) },
		"appSidebarView": func(v *appsView, c appCard) (template.HTML, error) {
			return renderAppSidebarEntry(atoms, v, c, language)
		},
		"appDirectoryPart": func(v *appsView, part string) (template.HTML, error) {
			return appDirectoryPart(atoms, v, part, language)
		},
		"appPagePart":        func(p page, part string) (template.HTML, error) { return appPagePart(atoms, p, part, language) },
		"appSettingsProject": func(p page) (template.HTML, error) { return appSettingsProject(atoms, p, language) },
		"appAccountCopy":     func(v *appsView) contract.AccountCopy { return appAccountCopy(v, language) },
		"appConnectPart":     func(v *appsView, part string) (template.HTML, error) { return appConnectPart(atoms, v, part, language) },
		"appAccountPart": func(v appAccountContextView, part string) (template.HTML, error) {
			return appAccountPart(atoms, v, part, language)
		},
		"appCardView":    func(v *appsView, c appCard) (template.HTML, error) { return renderAppCard(atoms, v, c, language) },
		"appProfileText": appProfileText(language), "appCopyText": appCopyText(language), "appPageText": appPageText(language), "appPageTitle": appPageTitle(language), "appAccountContext": appAccountContext, "appParts": appParts, "appAccountViews": appAccountViews, "appCardUI": appCardUI, "appAdvanced": appAdvanced, "appControlLabel": appControlLabel,
		"tr":                           func(text string) string { return translate(language, text) },
		"fontURL":                      fontURL,
		"workbenchClientConfiguration": workbenchClientConfiguration,
		"workbenchLegacyStyles":        workbenchLegacyStyles,
		"help":                         help,
		"identifier":                   identifier,
		"memorySourcesOffset":          memorySourcesOffset, "memoryOffset": memoryOffset, "memoryURL": legacyMemoryURLV1, "memorySourceLabel": legacyMemorySourceLabelV1, "memoryOriginLabel": legacyMemoryOriginLabelV1, "memoryStatus": legacyMemoryStatusV1, "memoryLayerLabel": legacyMemoryLayerLabelV1,
		"capabilityURL": capabilityURL,
		"traceColumns":  legacyTraceColumnNamesV1,
		"traceRangeURL": traceRangeURL, "activityHeight": legacyTraceActivityHeightV1, "activityOffset": legacyTraceActivityOffsetV1, "activityX": legacyTraceActivityXV1, "activityWidth": legacyTraceActivityWidthV1, "traceSpan": traceSpan, "traceLabel": legacyTraceLabelV1, "traceHasView": traceHasView, "traceURL": traceURL, "traceListURL": traceListURL, "traceClearURL": traceClearURL, "tracePresetURL": tracePresetURL,
		"canSend":       canSend,
		"bindingTarget": bindingTarget, "toolLabel": toolLabel,
		"taskConfigurationPage": taskConfigurationPage, "taskURL": taskURL, "executionURL": executionURL, "taskTitle": task.Title, "taskListURL": taskListURL, "taskPageCount": legacyWorkbenchPageCountV1, "historyLabel": legacyWorkbenchHistoryLabelV1,
		"scheduleTime":      func(v *task.Schedule) string { return localScheduleTime(language, v) },
		"scheduleFrequency": func(v *task.Schedule) string { return scheduleFrequency(language, v) },
		"conversationURL":   conversationURL, "tabLabel": legacyTraceTabLabelV1,
		"prettyBody":          prettyBody,
		"providerTimingLabel": legacyTraceProviderTimingLabelV1,
		"duration":            duration,
		"elapsed":             elapsed,
		"milliseconds":        milliseconds,
		"obsStatuses":         legacyTraceStatusesV1,
		"obsKind":             legacyTraceObservationKindV1,
		"obsRole":             legacyTraceObservationRoleV1,
		"parentRun":           legacyTraceObservationParentV1,

		"money":       money,
		"hasPrevious": legacyTraceHasPreviousV1, "tokenRate": tokenRate, "totalInput": totalInput,
		"obsJSON":         viewJSON,
		"modelPickerView": modelPickerView,
		"authURL":         AuthURL,
		"runRole":         legacyTraceRoleLabelV1,
		"taskAction":      legacyTraceTaskActionV1,
		"indent":          treeIndent,
		"contextSections": func() []string { return []string{"messages", "tools", "response", "provenance", "recall"} },
		"contextURL":      contextURL,
		"contextLink":     contextLink,
		"partEnd":         func(v *contextView) int { return v.Query.Offset + len(v.Frozen.Content) },
	}).Funcs(timeFunctions(time.Local, language)).ParseFS(templates, "templates/*.html", "legacy-ui/memory-v1/memory.html", "legacy-ui/trace-v1/*.html", "legacy-ui/workbench-v1/*.html"))
	// Keep the request-clone source unexecuted; atoms have their own namespace.
	atoms = template.Must(parsed.Clone())
	return parsed

}

// Keep unexecuted sources for request-local function bindings.
var layoutSource = newLayout("en")
var chineseLayoutSource = newLayout("zh-CN")
var layout = template.Must(layoutSource.Clone())

package web

import (
	"bytes"
	"encoding/json"
	"github.com/z-chenhao/assistant/internal/llm"
	"html/template"
	"net/http"
	"net/url"
	"strings"

	"github.com/z-chenhao/assistant/internal/observability"
	"golang.org/x/net/html"
	"golang.org/x/net/html/atom"
)

// A repository-private read facade, not the page/Server or a public domain API.
// Candidate syntax cannot reach other page owners, effects or ViewSource.
type traceTemplateModel struct{ Trace *traceTemplateView }
type traceTemplateView struct {
	WorkbenchManaged, ShowInternal, InputComplete bool
	HiddenInternal                                int
	ReaderRevision                                string
	TimelineStart, TimelineEnd                    int64
	Query                                         observability.TraceQuery
	Task, After, Tab, Compare                     string
	List                                          observability.Traces
	Tree                                          *observability.ObservationTree
	Rows                                          []traceRow
	Input, Output, Wire                           observability.Body
	Messages                                      []observability.ChatPreview
	ModelOutput                                   *llm.GenerationResult
	ToolOutput                                    *toolOutputView
	Diff                                          *observability.ContextDiff
	Relations                                     *observability.Relationships
	FullContext                                   *contextView
	EvidenceTabs                                  []string
	TTFTMS                                        *int64
	OutputRate                                    *float64
	source                                        *traceView
}

func traceTemplateData(v *traceView) traceTemplateModel {
	return traceTemplateModel{Trace: &traceTemplateView{WorkbenchManaged: v.WorkbenchManaged, ShowInternal: v.ShowInternal, InputComplete: v.InputComplete, HiddenInternal: v.HiddenInternal, ReaderRevision: v.ReaderRevision, TimelineStart: v.TimelineStart, TimelineEnd: v.TimelineEnd, Query: v.Query, Task: v.Task, After: v.After, Tab: v.Tab, Compare: v.Compare, List: v.List, Tree: v.Tree, Rows: v.Rows, Input: v.Input, Output: v.Output, Wire: v.Wire, Messages: v.Messages, ModelOutput: v.ModelOutput, ToolOutput: v.ToolOutput, Diff: v.Diff, Relations: v.Relations, FullContext: v.FullContext, EvidenceTabs: v.EvidenceTabs, TTFTMS: v.TTFTMS, OutputRate: v.OutputRate, source: v}}
}

// Only these two entry points are called by Host. Internal fragments are private.
var traceViewNames = map[string]bool{"observability-page": true, "trace-list-time-controls": true}
var traceSharedNames = map[string]bool{"help": true, "identifier": true, "context-reader": true, "icon-arrow-right": true, "icon-calendar": true, "icon-chevron": true, "icon-close": true, "icon-columns": true, "icon-copy": true, "icon-expand": true, "icon-filter": true, "icon-refresh": true, "icon-search": true, "icon-zoom-in": true, "icon-zoom-out": true}

const traceViewFields = "API Activity After Arguments Artifacts At Attempt Available Before CacheRead CacheWrite CallID Calls Clipped Code Compare Content Costs Count CreatedAt Creator Currency Detail Diff Ended EndedAt Epoch Error ErrorCode EvidenceTabs Errors Estimated Executions FirstOutput Format FullContext Generation Generations Guides HTTPStatus HasChildren HiddenInternal ID Images Index Input InputComplete IsZero Items Key Kind KnownUsage Label List MaxOutputTokens Message Messages Metrics Micros Model ModelOutput More Name Next Note Observation Operation Output OutputRate Parameters Parent ParentOutside Phase Previous Provider ProviderTimings Query ReaderRevision ReasoningEffort Ref Relations Role Rows RunID Running Search Selected Session ShowInternal Since Source Started Status Step TTFTMS Tab Task Tasks Text Texts TimelineEnd TimelineStart Timing Title ToolCalls ToolOutput Total Trace Tree Type Until Width Wire WorkbenchManaged X"
const traceViewFunctions = " nonblank trimPrefix unixMicro intSub floatSub fraction gt traceTr identifier help taskURL money milliseconds traceRangeURL traceSpan traceURL traceListURL traceClearURL tracePresetURL prettyBody duration elapsed tokenRate totalInput obsJSON contextURL traceTime observedTime observedDateTime observedAt timeInput printf eq ne and or not len index "

func compileTraceView(source string, r *http.Request, copy []observability.TraceTranslation) (*template.Template, error) {
	return compileInspectionView(source, traceViewBindings(r, copy), requestLayout(r), inspectionTemplateSpec{root: "trace-view", names: traceViewNames, shared: traceSharedNames, fields: traceViewFields, functions: traceViewFunctions, variables: true, privateName: func(name string) bool {
		return strings.HasPrefix(name, "trace-") || strings.HasPrefix(name, "observation-") || name == "generation-metrics" || name == "provider-timing"
	}, unavailable: observability.ErrInvalid})
}

// Remaining navigation, geometry and reading helpers are explicit private
// migration boundaries. Finite choices/labels/titles now live in the template.
func traceViewBindings(r *http.Request, copy []observability.TraceTranslation) template.FuncMap {
	f := template.FuncMap{
		"printf": func(format, first, second string) (string, error) {
			if format != "%s %s" {
				return "", observability.ErrInvalid
			}
			return first + " " + second, nil
		},
		"trimPrefix": strings.TrimPrefix, "unixMicro": traceTemplateUnixMicro, "intSub": traceTemplateIntSub, "floatSub": traceTemplateFloatSub, "fraction": traceTemplateFraction,
		"traceTr":    func(text string) string { return traceTranslate(copy, requestLanguage(r), text) },
		"nonblank":   func(text string) bool { return strings.TrimSpace(text) != "" },
		"tr":         func(text string) string { return translate(requestLanguage(r), text) },
		"identifier": identifier, "help": help, "taskURL": taskURL, "money": money, "milliseconds": milliseconds,
		"traceRangeURL": func(v *traceTemplateView, since, until string) string { return traceRangeURL(v.source, since, until) }, "traceSpan": traceSpan, "traceURL": func(v *traceTemplateView, field string, value any) string { return traceURL(v.source, field, value) }, "traceListURL": func(v *traceTemplateView) string { return traceListURL(v.source) }, "traceClearURL": func(v *traceTemplateView, field string) string { return traceClearURL(v.source, field) }, "tracePresetURL": func(v *traceTemplateView, hours int) string { return tracePresetURL(v.source, hours) },
		"prettyBody": prettyBody, "duration": duration, "elapsed": elapsed,
		"tokenRate": tokenRate, "totalInput": totalInput, "obsJSON": viewJSON,
		"contextURL": contextURL,
		// Trusted context-reader is shared, and its helpers are not candidate syntax.
		"contextSections": func() []string { return []string{"messages", "tools", "response", "provenance", "recall"} }, "contextLink": contextLink,
	}
	for name, value := range timeFunctions(requestLocation(r), requestLanguage(r)) {
		f[name] = value
	}
	return f
}

type traceViewBuffer struct {
	bytes.Buffer
	remaining int
}

func (w *traceViewBuffer) Write(p []byte) (int, error) {
	if len(p) > w.remaining {
		return 0, observability.ErrInvalid
	}
	w.remaining -= len(p)
	return w.Buffer.Write(p)
}
func (w *traceViewBuffer) WriteString(p string) (int, error) { return w.Write([]byte(p)) }

func renderTraceView(t *template.Template, name string, v *traceView) (template.HTML, error) {
	if name != "observability-page" && name != "trace-list-time-controls" {
		return "", observability.ErrInvalid
	}
	// Authorized source bodies may be duplicated for formatted/raw controls.
	// The buffer prevents amplification without truncating original evidence.
	raw, err := json.Marshal(traceTemplateData(v))
	if err != nil {
		return "", err
	}
	out := traceViewBuffer{remaining: 65536 + 16*len(raw)}
	if err = t.ExecuteTemplate(&out, name, traceTemplateData(v)); err != nil {
		return "", err
	}
	if !safeTraceViewDOM(out.String(), name, v) {
		return "", observability.ErrInvalid
	}
	return template.HTML(out.String()), nil
}

func tracePageLayout(r *http.Request, v *traceView) (*template.Template, error) {
	if !v.WorkbenchManaged || v.ReaderRevision == "" || !observability.ValidReaderRevision(v.ReaderRevision) {
		return nil, observability.ErrInvalid
	}
	candidate, err := compileTraceView(v.ViewSource, r, v.Copy)
	if err != nil {
		return nil, err
	}
	content, err := renderTraceView(candidate, "observability-page", v)
	if err != nil {
		return nil, err
	}
	controls, err := renderTraceView(candidate, "trace-list-time-controls", v)
	if err != nil {
		return nil, err
	}
	outer := requestLayout(r).Funcs(template.FuncMap{"tracePageView": func() template.HTML { return content }, "traceTimeControlsView": func() template.HTML { return controls }})
	_, err = outer.Parse(`{{define "observability-page"}}{{tracePageView}}{{end}}{{define "trace-list-time-controls"}}{{traceTimeControlsView}}{{end}}`)
	return outer, err
}

// Passive markup may compose approved shared controls and issue protected GET
// reads. It cannot introduce scripts, CSS, external destinations or effect APIs.
func safeTraceViewDOM(source, name string, v *traceView) bool {
	nodes, err := html.ParseFragment(strings.NewReader(source), &html.Node{Type: html.ElementNode, Data: "div", DataAtom: atom.Div})
	if err != nil {
		return false
	}
	roots := 0
	allowedTags := " section div form input button nav a span table colgroup col thead tbody tr th td details summary label fieldset legend h1 h2 h3 p strong small dl dt dd header time svg path circle rect line polyline polygon g title ol ul li pre article output "
	allowedAttrs := " action aria-current aria-describedby aria-expanded aria-hidden aria-label aria-live aria-pressed aria-sort checked class colspan cx cy d data-activity-explore data-activity-hint data-activity-max data-activity-selection data-auto-filter data-bucket data-change data-context-help data-context-index data-context-item data-context-offset data-context-run data-context-section data-context-step data-continue data-control-disclosure data-copy-json data-copy-value data-end data-execution-id data-filter-submit data-first-output data-formatted-only data-help-fallback data-help-tooltip data-help-trigger data-json-mode data-json-only data-json-source data-kind data-list-filter-toggle data-local-input data-local-time data-observation-id data-observation-kind data-observation-parent data-observation-toggle data-observation-type data-parent-observation data-parent-run data-readonly-json data-role data-root-run data-run-id data-running data-split-storage data-split-view data-start data-status data-time-end data-time-start data-timeline-action data-trace-activity data-trace-column data-trace-id data-trace-program-revision data-trace-reader-revision data-trace-required data-trace-title data-wire-reader data-wire-source datetime disabled fill form height hidden href id maxlength method name open placeholder preserveaspectratio r role rx scope step stroke stroke-linecap stroke-linejoin stroke-width tabindex title type value viewbox width x x1 x2 y y1 y2 "
	classes := " back-link button compact content-format context-content context-continue context-diff context-heading context-index context-item-link context-items context-range context-reader context-tabs control-disclosure control-disclosure-panel control-filter-chip control-help control-help-line control-help-trigger control-icon-button control-identifier control-identifier-button control-identifier-link control-identifier-value control-kind-badge control-list-footer control-list-heading control-list-search control-list-table control-list-toolbar control-metric-badge control-search control-segmented control-status-badge control-toolbar-button control-tooltip control-tree-toggle creator-run empty-state error feedback frozen-content icon inspect-task muted object-list object-row obs-active-filters obs-activity obs-activity-axis obs-activity-bars obs-activity-brush obs-activity-bucket obs-activity-chart obs-activity-empty obs-activity-footer obs-activity-heading obs-activity-plot obs-activity-y obs-bar obs-call-card obs-cell-text obs-col-cost obs-col-created obs-col-duration obs-col-id obs-col-model obs-col-preview obs-col-role obs-col-status obs-col-tokens obs-column-options obs-cost obs-detail-note obs-detail-pane obs-detail-properties obs-detail-tabs obs-detail-toolbar obs-diagnostic-identity obs-empty obs-evidence-links obs-facet obs-filter-actions obs-filter-fields obs-filter-rail obs-filter-rail-content obs-filters obs-full-heading obs-generation-config obs-generation-metrics obs-internal-filter obs-io-heading obs-io-input obs-io-output obs-io-section obs-json obs-list-footer obs-list-heading obs-list-toolbar obs-message obs-message-excerpt obs-message-role obs-message-text obs-messages obs-metadata obs-model-name obs-more obs-node-content obs-node-duration obs-node-facts obs-node-header obs-node-label obs-node-name obs-node-status obs-node-time obs-number obs-pane-heading obs-point obs-provider-timing obs-results-layout obs-results-main obs-run-errors obs-run-overview obs-search obs-select obs-selected-heading obs-selected-icon obs-selected-id obs-selected-name obs-started-time obs-status-dot obs-table-scroll obs-table-toolbar obs-task-epochs obs-task-link obs-time-presets obs-timeline-actions obs-timeline-axis obs-timeline-controls obs-timeline-heading obs-timing-events obs-trace-list obs-track obs-tree obs-tree-guides obs-tree-pane obs-tree-row obs-tree-search obs-tree-toggle-space obs-type-icon obs-workbench observability observation-node panel role-assistant role-function role-system role-tool role-user round-links row-heading run-node secondary section-tabs sr-only status-pill task-timeline technical-details trace-open tree-heading wire-body "
	ids := map[string]bool{}
	for _, item := range v.List.Items {
		ids[item.ID] = true
	}
	seen := map[string]bool{}
	bodies := map[string]int{}
	normalize := strings.NewReplacer("\r\n", "\n", "\r", "\n", "\x00", "\ufffd").Replace
	var text func(*html.Node) string
	text = func(n *html.Node) string {
		if n.Type == html.TextNode {
			return n.Data
		}
		var b strings.Builder
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			b.WriteString(text(c))
		}
		return b.String()
	}

	ownedID := func(id string) bool {
		return id == "observability-page" || id == "observability-workbench" || strings.HasPrefix(id, "trace-") || strings.HasPrefix(id, "context-") || strings.Contains(" generation-metrics-help root-boundary-help wire-request-help missing-wire-help observation-output-help missing-output-help observation-input-help ", " "+id+" ")
	}
	visible := func(n *html.Node) bool {
		for at := n; at != nil; at = at.Parent {
			for _, a := range at.Attr {
				if a.Key == "hidden" || a.Key == "aria-hidden" && a.Val == "true" || a.Key == "class" && strings.Contains(" "+a.Val+" ", " sr-only ") {
					return false
				}
			}
		}
		return true
	}
	var visit func(*html.Node) bool
	visit = func(n *html.Node) bool {
		if n.Type == html.ElementNode {
			if !strings.Contains(allowedTags, " "+n.Data+" ") {
				return false
			}
			attrs := map[string]string{}
			for _, a := range n.Attr {
				key := strings.ToLower(a.Key)
				if _, ok := attrs[key]; ok || a.Namespace != "" || !strings.Contains(allowedAttrs, " "+key+" ") {
					return false
				}
				attrs[key] = a.Val
				if (key == "fill" || key == "stroke") && a.Val != "none" && a.Val != "currentColor" {
					return false
				}
				if key == "class" {
					for _, class := range strings.Fields(a.Val) {
						if !strings.Contains(classes, " "+class+" ") {
							return false
						}
					}
				}
				if (key == "id" || key == "aria-describedby") && !ownedID(a.Val) {
					return false
				}
				if key == "form" && a.Val != "trace-filters" {
					return false
				}
				if key == "href" {
					u, e := url.Parse(a.Val)
					if e != nil || strings.ContainsAny(a.Val, "\\\r\n") || strings.Contains(u.Path, "/../") || strings.Contains(u.Path, "/./") || u.IsAbs() || u.Host != "" || strings.HasPrefix(a.Val, "//") || u.Fragment != "" {
						return false
					}
					if u.Path != "/developer/observability" && !strings.HasPrefix(u.Path, "/developer/observability/") && !strings.HasPrefix(u.Path, "/tasks/") {
						return false
					}
					if ref := u.Query().Get("reader_revision"); ref != "" && ref != v.ReaderRevision {
						return false
					}
				}
				if key == "action" && (n.Data != "form" || a.Val != "/developer/observability") {
					return false
				}
				if key == "method" && (n.Data != "form" || !strings.EqualFold(a.Val, "get")) {
					return false
				}
				if key == "name" && !strings.Contains(" search role status session since until ", " "+a.Val+" ") {
					return false
				}
				if key == "data-list-filter-toggle" && a.Val != "trace-filter-rail" {
					return false
				}
				if key == "data-split-storage" && a.Val != "steward.trace.tree-ratio.v1" {
					return false
				}
				if (key == "data-trace-reader-revision" || key == "data-trace-program-revision") && a.Val != v.ReaderRevision {
					return false
				}
			}
			if n.Data == "form" && (attrs["action"] != "/developer/observability" || !strings.EqualFold(attrs["method"], "get")) {
				return false
			}
			if n.Data == "input" && !strings.Contains(" search radio checkbox hidden datetime-local ", " "+attrs["type"]+" ") {
				return false
			}
			if n.Data == "button" && !strings.Contains(" button submit ", " "+attrs["type"]+" ") {
				return false
			}
			if attrs["id"] == "observability-page" {
				roots++
				if !visible(n) {
					return false
				}
				if attrs["data-trace-reader-revision"] != v.ReaderRevision || attrs["data-trace-program-revision"] != v.ReaderRevision {
					return false
				}
			}
			if field, ok := attrs["data-json-source"]; ok {
				body := v.Input
				if field == "output" {
					body = v.Output
				} else if field != "input" {
					return false
				}
				if n.Data != "pre" || !body.Available || text(n) != normalize(prettyBody(body.Content)) {
					return false
				}
				bodies[field]++
			}
			if _, ok := attrs["data-wire-source"]; ok {
				if n.Data != "pre" || !v.Wire.Available || text(n) != normalize(prettyBody(v.Wire.Content)) {
					return false
				}
				bodies["wire"]++
			}
			if id, ok := attrs["data-execution-id"]; ok {
				if n.Data != "tr" || !ids[id] || seen[id] || !visible(n) {
					return false
				}
				seen[id] = true
			}
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			if !visit(c) {
				return false
			}
		}
		return true
	}
	for _, node := range nodes {
		if !visit(node) {
			return false
		}
	}
	if name == "observability-page" {
		if v.Tree != nil {
			switch v.Tab {
			case "overview":
				if v.Input.Available && bodies["input"] == 0 || v.Output.Available && bodies["output"] == 0 {
					return false
				}
			case "wire":
				if v.Wire.Available && bodies["wire"] != 1 {
					return false
				}
			}
		}
		return roots == 1 && (v.Tree != nil || v.Task != "" || len(seen) == len(ids))
	}
	return roots == 0
}

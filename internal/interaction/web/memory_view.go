package web

import (
	"bytes"
	"encoding/json"
	"html/template"
	"io"
	"net/http"
	"net/url"
	"strings"

	"github.com/z-chenhao/assistant/internal/memory"
	"golang.org/x/net/html"
	"golang.org/x/net/html/atom"
)

// Candidate templates receive only this owner's already-authorized read model.
// The root cannot reach the Server, other pages, SQL or effect methods. The
// template syntax is private/experimental; it is not a public domain-model API.
type memoryTemplateModel struct{ Memory *memoryTemplateView }
type memoryTemplateView struct {
	Query               memory.InspectionQuery
	Page                memory.InspectionPage
	Selected            *memory.InspectionRecord
	MaintenanceSelected *memory.MaintenanceRecord
	Presentation        *memoryTemplateSelection
}
type memoryTemplateSelection struct{ ReaderRevision string }

var memoryViewNames = map[string]bool{"memory-page": true, "memory-workbench": true, "memory-document-detail": true, "memory-maintenance-cost": true, "memory-maintenance-detail": true}
var memorySharedNames = map[string]bool{"help": true, "identifier": true, "icon-arrow-right": true, "icon-chevron": true, "icon-filter": true, "icon-memory": true, "icon-search": true, "icon-source": true}

const memoryViewFields = "Attempts CacheReadTokens Content ContentState Costs CreatedAt Currency DocumentsRetired DocumentsWritten Error Failure Format Generations ID InputTokens IsZero Items Kind KnownUsageGenerations LatencyMS LaterInjections LaterToolReads Layer Maintenance MaintenanceSelected Memory Metrics Model Next NextOffset NextSourcesOffset Operation Origin OutputTokens Page Presentation Purpose Query ReaderRevision Ref ReferenceMicros Related ReplacedBy Revision RunID Search Selected Since Source SourceCount Sources Status Supersedes Text Tier Title Total UnchangedWrites Until View"
const memoryViewClasses = " back-link button compact control-help control-help-trigger control-icon-button control-identifier control-identifier-button control-identifier-link control-identifier-value control-kind-badge control-list-footer control-list-heading control-list-search control-list-table control-list-toolbar control-metric-badge control-search control-status-badge control-toolbar-button control-tooltip empty-state error feedback icon memory-col-attempts memory-col-content memory-col-created memory-col-error memory-col-execution memory-col-operation memory-col-origin memory-col-sources memory-col-status memory-col-tier memory-content memory-content-section memory-detail memory-generation-history memory-lineage memory-list-toolbar memory-maintenance memory-metadata memory-open memory-page memory-preview-tab memory-record-list memory-select memory-source memory-source-content memory-source-label memory-source-list memory-source-reference memory-sources memory-workbench muted obs-active-filters obs-cell-text obs-col-cost obs-col-created obs-col-id obs-col-tokens obs-cost obs-detail-header obs-detail-note obs-detail-pane obs-detail-properties obs-detail-tabs obs-detail-toolbar obs-empty obs-facet obs-filter-actions obs-filter-fields obs-filter-rail obs-filter-rail-content obs-filters obs-list-footer obs-list-heading obs-metrics obs-node-facts obs-node-header obs-pane-heading obs-results-layout obs-results-main obs-search obs-selected-heading obs-selected-icon obs-selected-name obs-started-time obs-table-scroll obs-table-toolbar obs-trace-list obs-tree-pane obs-workbench observability secondary section-tabs sr-only status-pill technical-details text-link "

const memoryViewFunctions = " tr identifier help taskURL money milliseconds memoryURL memorySourceLabel memoryOriginLabel memoryLayerLabel memoryStatus memoryOffset memorySourcesOffset traceTime localTime timeInput eq ne and or not len index "

func compileMemoryView(source string, functions template.FuncMap, host *template.Template) (*template.Template, error) {
	return compileInspectionView(source, functions, host, inspectionTemplateSpec{root: "memory-view", names: memoryViewNames, shared: memorySharedNames, fields: memoryViewFields, functions: memoryViewFunctions, unavailable: memory.ErrInspectionUnavailable})
}

type memoryViewBuffer struct {
	bytes.Buffer
	remaining int
}

func (w *memoryViewBuffer) Write(p []byte) (int, error) {
	if len(p) > w.remaining {
		return 0, memory.ErrInspectionUnavailable
	}
	w.remaining -= len(p)
	return w.Buffer.Write(p)
}
func (w *memoryViewBuffer) WriteString(s string) (int, error) { return w.Write([]byte(s)) }

var _ io.Writer = (*memoryViewBuffer)(nil)

func renderMemoryView(t *template.Template, v *memoryView) (template.HTML, error) {
	raw, err := json.Marshal(v.Page)
	if err != nil {
		return "", memory.ErrInspectionUnavailable
	}
	// Original list content can occur in both text and tooltip, with HTML escaping.
	// Bound candidate amplification using actual authorized data, not a new record cap.
	out := memoryViewBuffer{remaining: 65536 + 16*len(raw)}
	model := memoryTemplateModel{Memory: &memoryTemplateView{Query: v.Query, Page: v.Page, Selected: v.Selected, MaintenanceSelected: v.MaintenanceSelected, Presentation: &memoryTemplateSelection{ReaderRevision: v.Presentation.ReaderRevision}}}
	if err = t.ExecuteTemplate(&out, "memory-page", model); err != nil {
		return "", memory.ErrInspectionUnavailable
	}
	if !safeMemoryViewDOM(out.String(), v) {
		return "", memory.ErrInspectionUnavailable
	}
	return template.HTML(out.String()), nil
}

func safeMemoryViewDOM(source string, v *memoryView) bool {
	nodes, err := html.ParseFragment(strings.NewReader(source), &html.Node{Type: html.ElementNode, Data: "div", DataAtom: atom.Div})
	if err != nil {
		return false
	}
	urls := map[string]bool{"/developer/memory": true}
	for _, link := range v.Presentation.Links {
		urls[link.URL] = true
	}
	addRun := func(id string) {
		if id != "" {
			urls["/developer/observability?"+url.Values{"run": []string{id}}.Encode()] = true
		}
	}
	for _, item := range v.Page.Items {
		for _, s := range item.Sources {
			addRun(s.RunID)
			if s.Kind == "task" {
				urls[taskURL(s.Ref)] = true
			}
		}
	}
	for _, m := range v.Page.Maintenance {
		addRun(m.RunID)
	}
	roots, selected, contents, sources := 0, 0, 0, 0
	frozenInputs := 0
	expectedRows := map[string]bool{}
	seenRows := map[string]bool{}
	if v.Selected == nil && v.MaintenanceSelected == nil {
		for _, item := range v.Page.Items {
			expectedRows[item.Ref] = true
		}
		for _, m := range v.Page.Maintenance {
			expectedRows[m.Ref] = true
		}
	}
	text := func(n *html.Node) string {
		var b strings.Builder
		var walk func(*html.Node)
		walk = func(n *html.Node) {
			if n.Type == html.TextNode {
				b.WriteString(n.Data)
			}
			for c := n.FirstChild; c != nil; c = c.NextSibling {
				walk(c)
			}
		}
		walk(n)
		return b.String()
	}
	// Match the original browser text semantics, including NUL replacement and
	// CR/LF normalization performed by Go escaping and HTML parsing. Facts and
	// API bytes themselves remain unchanged.
	expectedText := func(value string) string {
		nodes, e := html.ParseFragment(strings.NewReader("<div>"+template.HTMLEscapeString(value)+"</div>"), &html.Node{Type: html.ElementNode, Data: "div", DataAtom: atom.Div})
		if e != nil {
			return ""
		}
		var b strings.Builder
		for _, n := range nodes {
			b.WriteString(text(n))
		}
		return b.String()
	}
	allowedTags := " section div form input button nav a span table colgroup col thead tbody tr th td details summary label fieldset legend h1 h2 h3 p strong small dl dt dd header time svg path circle rect line polyline polygon g title "
	visible := func(n *html.Node) bool {
		for at := n; at != nil; at = at.Parent {
			if at.Type != html.ElementNode {
				continue
			}
			opened := false
			for _, a := range at.Attr {
				key := strings.ToLower(a.Key)
				if key == "hidden" || key == "aria-hidden" && strings.EqualFold(a.Val, "true") {
					return false
				}
				if key == "open" {
					opened = true
				}
				if key == "class" {
					for _, class := range strings.Fields(a.Val) {
						if class == "control-tooltip" || class == "obs-filter-rail" || class == "obs-filter-fields" {
							return false
						}
					}
				}
			}
			if at.Data == "details" && !opened {
				return false
			}
		}
		return true
	}
	var visit func(*html.Node) bool
	visit = func(n *html.Node) bool {
		if n.Type == html.ElementNode {
			attrs := map[string]string{}
			for _, a := range n.Attr {
				if _, duplicate := attrs[strings.ToLower(a.Key)]; duplicate {
					return false
				}
				attrs[strings.ToLower(a.Key)] = a.Val
			}
			if n.Data == "input" && !strings.Contains(" search radio hidden datetime-local ", " "+attrs["type"]+" ") {
				return false
			}
			if n.Data == "button" && !strings.Contains(" button submit ", " "+attrs["type"]+" ") {
				return false
			}
			if n.Data == "input" && attrs["name"] == "inspector_revision" {
				frozenInputs++
				if attrs["type"] != "hidden" || attrs["value"] != v.Presentation.ReaderRevision {
					return false
				}
			}
			if ref, ok := attrs["data-memory-ref"]; ok {
				if n.Data != "tr" || !expectedRows[ref] || seenRows[ref] || !visible(n) {
					return false
				}
				wanted := ""
				for _, link := range v.Presentation.Links {
					if link.Key == "ref" && link.Value == ref {
						wanted = link.URL
						break
					}
				}
				links := 0
				valid := true
				var checkLink func(*html.Node)
				checkLink = func(node *html.Node) {
					if node.Type == html.ElementNode && node.Data == "a" {
						class, href := "", ""
						for _, a := range node.Attr {
							if a.Key == "class" {
								class = a.Val
							}
							if a.Key == "href" {
								href = a.Val
							}
						}
						if strings.Contains(" "+class+" ", " memory-open ") {
							links++
							if href != wanted {
								valid = false
							}
						}
					}
					for c := node.FirstChild; c != nil; c = c.NextSibling {
						checkLink(c)
					}
				}
				checkLink(n)
				if wanted == "" || links != 1 || !valid {
					return false
				}
				seenRows[ref] = true
			}

			if !strings.Contains(allowedTags, " "+n.Data+" ") {
				return false
			}
			for _, a := range n.Attr {
				key := strings.ToLower(a.Key)

				allowedAttrs := " id class title href action method type name value maxlength placeholder checked selected disabled hidden open for role tabindex autocomplete colspan datetime viewbox width height fill stroke stroke-width stroke-linecap stroke-linejoin d cx cy r x y x1 x2 y1 y2 rx ry points xmlns aria-label aria-current aria-describedby aria-hidden aria-expanded data-auto-filter data-filter-submit data-kind data-list-filter-toggle data-local-input data-memory-content data-memory-id data-memory-ref data-memory-source-content data-source-kind data-split-storage data-split-view data-status data-copy-value data-context-help data-help-fallback data-help-trigger data-help-tooltip data-local-time "
				if !strings.Contains(allowedAttrs, " "+key+" ") {
					return false
				}
				if key == "id" && !strings.HasPrefix(a.Val, "memory-") {
					return false
				}
				if (key == "aria-describedby" || key == "for") && !strings.HasPrefix(a.Val, "memory-") {
					return false
				}
				if key == "class" {
					for _, class := range strings.Fields(a.Val) {
						if !strings.HasPrefix(class, "memory-") && !strings.Contains(memoryViewClasses, " "+class+" ") {
							return false
						}
					}
				}
				if key == "data-list-filter-toggle" && a.Val != "memory-filter-rail" {
					return false
				}
				if key == "data-split-storage" && a.Val != "steward.memory.sources-ratio.v1" {
					return false
				}
				if key == "name" && !strings.Contains(" search tier origin status layer since until inspector_revision ", " "+a.Val+" ") {
					return false
				}
				if a.Namespace != "" || strings.HasPrefix(key, "on") || key == "style" || key == "src" || key == "srcdoc" || key == "formaction" || key == "formmethod" || key == "target" || key == "download" {
					return false
				}
				if key == "href" && !urls[a.Val] {
					return false
				}
				if key == "action" && (n.Data != "form" || a.Val != "/developer/memory") {
					return false
				}
				if key == "method" && (n.Data != "form" || strings.ToLower(a.Val) != "get") {
					return false
				}
				if key == "id" && a.Val == "memory-page" {
					if !visible(n) {
						return false
					}
					roots++
				}
				if key == "data-memory-id" {
					if !visible(n) {
						return false
					}
					selected++
					expected := ""
					if v.Selected != nil {
						expected = v.Selected.Ref
					}
					if v.MaintenanceSelected != nil {
						expected = v.MaintenanceSelected.Ref
					}
					if a.Val != expected || expected == "" {
						return false
					}
				}
				if key == "data-memory-content" {
					if !visible(n) {
						return false
					}
					contents++
					if v.Selected == nil || v.Selected.ContentState != "available" || text(n) != expectedText(v.Selected.Content) {
						return false
					}
				}
				if key == "data-memory-source-content" {
					if !visible(n) {
						return false
					}
					sources++
					found := false
					if v.Selected != nil {
						for _, s := range v.Selected.Sources {
							if s.Ref == v.Query.Source && text(n) == expectedText(s.Text) {
								found = true
							}
						}
					}
					if !found {
						return false
					}
				}
			}
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			if !visit(c) {
				return false
			}
		}
		return true
	}
	for _, n := range nodes {
		if !visit(n) {
			return false
		}
	}
	expectedSelected, expectedContent, expectedSources := 0, 0, 0
	if v.Selected != nil || v.MaintenanceSelected != nil {
		expectedSelected = 1
	}
	if v.Selected != nil {
		if v.Selected.ContentState == "available" {
			expectedContent = 1
		}
		for _, s := range v.Selected.Sources {
			if s.Ref == v.Query.Source {
				expectedSources++
			}
		}
	}
	expectedFrozen := 0
	if v.Selected == nil && v.MaintenanceSelected == nil {
		expectedFrozen = 1
	}
	return frozenInputs == expectedFrozen && len(seenRows) == len(expectedRows) && roots == 1 && selected == expectedSelected && contents == expectedContent && sources == expectedSources
}

func memoryViewBindings(v *memoryView, r *http.Request) template.FuncMap {
	f := memoryPresentationFunctions(v)
	for name, value := range timeFunctions(requestLocation(r), requestLanguage(r)) {
		f[name] = value
	}
	f["tr"] = func(text string) string { return memoryTranslate(v, requestLanguage(r), text) }
	f["identifier"], f["help"], f["taskURL"], f["money"], f["milliseconds"] = identifier, help, taskURL, money, milliseconds
	f["memoryOffset"], f["memorySourcesOffset"] = memoryOffset, memorySourcesOffset
	return f
}

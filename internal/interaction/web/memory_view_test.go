package web

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/z-chenhao/assistant/internal/memory"
)

func originalMemoryViewFixture(t *testing.T, kind string) *memoryView {
	t.Helper()
	v := &memoryView{Presentation: &memory.InspectionPresentation{ReaderRevision: "sha256:original", Source: memory.InspectionSourceLabels{Conversation: "Source message", Task: "Source task", Tool: "memory_manage", Control: "Local control", Other: "Source"}, Origin: memory.InspectionOriginLabels{Written: "Written note", Dream: "Dreaming", Original: "Original record", Automatic: "Automatically extracted", Explicit: "Explicitly remembered", Other: "Unknown origin"}, Layer: memory.InspectionLayerLabels{Transcripts: "Transcripts", Representations: "Memory representations", Injected: "Injected context", Notes: "Memory notes"}, DefaultStatus: "active"}}
	if strings.HasPrefix(kind, "maintenance") {
		v.Query.View = "maintenance"
		if kind == "maintenance-detail" {
			v.Query.Ref = "memory:maintenance-priced"
		}
		var err error
		v.Page, err = (maintenanceMemoryReader{}).InspectMemory(context.Background(), v.Query)
		if err != nil {
			t.Fatal(err)
		}
		if v.Query.Ref != "" {
			v.MaintenanceSelected = &v.Page.Maintenance[0]
		}
	} else {
		r := memory.InspectionRecord{Ref: "memory:original", Layer: memory.Notes, ContentState: "available", Content: "PRIVATE original <tag> & 中文 body\x00line\r\nnext", Title: "Original owner note", Origin: "written", Status: "active", Tier: "recall", CreatedAt: time.Date(2026, 11, 1, 1, 2, 3, 0, time.UTC), SourceCount: 1, Sources: []memory.InspectionSource{{Kind: "conversation", Ref: "conversation:original", Text: "PRIVATE original source <tag> & 中文\x00line\r\nnext"}}}
		v.Page = memory.InspectionPage{Items: []memory.InspectionRecord{r}, Total: 1}
		if kind == "document-detail" {
			v.Query.Ref = r.Ref
			v.Query.Source = r.Sources[0].Ref
			v.Selected = &v.Page.Items[0]
		}
	}
	copy, err := os.ReadFile("../../plugin/memoryinspector/view/copy.json")
	if err != nil {
		t.Fatal(err)
	}
	if err = json.Unmarshal(copy, &v.Presentation.Copy); err != nil {
		t.Fatal(err)
	}
	add := func(key, value string) {
		raw := legacyMemoryURLV1(v.Query, key, value)
		u, e := url.Parse(raw)
		if e != nil {
			t.Fatal(e)
		}
		q := u.Query()
		q.Set("inspector_revision", v.Presentation.ReaderRevision)
		u.RawQuery = q.Encode()
		v.Presentation.Links = append(v.Presentation.Links, memory.InspectionLink{Key: key, Value: value, URL: u.String()})
	}
	add("ref", "")
	add("cursor", "")
	add("view", "maintenance")
	for _, layer := range []string{"transcripts", "notes", "representations", "injected"} {
		add("layer", layer)
	}
	for _, item := range v.Page.Items {
		add("ref", item.Ref)
		for _, source := range item.Sources {
			add("source", source.Ref)
		}
	}
	for _, m := range v.Page.Maintenance {
		add("ref", m.Ref)
	}
	return v
}

func TestMemoryViewOwnedSourceMatchesOriginalPrivateDisplayAndSharedControls(t *testing.T) {
	source, e := os.ReadFile("../../plugin/memoryinspector/view/memory.html")
	if e != nil {
		t.Fatal(e)
	}
	baseline, e := os.ReadFile("../../../tests/fixtures/plugins/memory-inspector-original-v1/managed-memory-v1.html.txt")
	if e != nil {
		t.Fatal(e)
	}
	copyBytes, e := os.ReadFile("../../../tests/fixtures/plugins/memory-inspector-original-v1/localization-v1.json")
	if e != nil {
		t.Fatal(e)
	}
	var captured []memory.InspectionTranslation
	if e = json.Unmarshal(copyBytes, &captured); e != nil {
		t.Fatal(e)
	}
	for _, kind := range []string{"document-list", "document-detail", "maintenance-list", "maintenance-detail"} {
		for _, language := range []string{"en", "zh-CN"} {
			t.Run(kind+"/"+language, func(t *testing.T) {
				v := originalMemoryViewFixture(t, kind)
				r := httptest.NewRequest("GET", "http://localhost/developer/memory", nil)
				if language == "zh-CN" {
					r.AddCookie(&http.Cookie{Name: languageCookie, Value: "zh-CN"})
				}
				if requestLanguage(r) != language {
					t.Fatal("fixture did not select actual language")
				}
				funcs := memoryViewBindings(v, r)
				host := requestLayout(r)
				candidate, e := compileMemoryView(string(source), funcs, host)
				if e != nil {
					t.Fatal("original owned view did not compile", e)
				}
				got, e := renderMemoryView(candidate, v)
				if e != nil {
					t.Fatal("original owned view did not pass Host acceptance", e)
				}
				baselineFunctions := memoryViewBindings(v, r)
				baselineFunctions["tr"] = func(text string) string {
					if language == "zh-CN" {
						for _, entry := range captured {
							if entry.English == text {
								return entry.Chinese
							}
						}
					}
					return text
				}
				original, e := requestLayout(r).Funcs(baselineFunctions).Parse(string(baseline))
				if e != nil {
					t.Fatal(e)
				}
				var want bytes.Buffer
				if e = original.ExecuteTemplate(&want, "memory-page", page{Memory: v}); e != nil {
					t.Fatal(e)
				}
				rendered := string(got)
				if kind == "maintenance-detail" && v.MaintenanceSelected.Operation == "dream" {
					title, empty := "Changed documents", "No visible document changes."
					if language == "zh-CN" {
						title, empty = "变更的记忆文档", "暂无可查看的文档变更。"
					}
					addition := `<section class="memory-generation-history"><h3>` + title + `</h3><p class="muted">` + empty + `</p></section>` + "\n"
					if !strings.Contains(rendered, addition) {
						t.Fatal("missing newly added change list")
					}
					rendered = strings.Replace(rendered, addition, "", 1)
				}
				if rendered != want.String() {
					t.Fatal("owned view changed captured original display")
				}
			})
		}
	}
}

func TestMemoryViewRejectsEffectsForeignResourcesRecursionAndForgedPrivateContent(t *testing.T) {
	source, e := os.ReadFile("../../plugin/memoryinspector/view/memory.html")
	if e != nil {
		t.Fatal(e)
	}
	v := originalMemoryViewFixture(t, "document-detail")
	r := httptest.NewRequest("GET", "http://localhost/developer/memory", nil)
	for name, change := range map[string][2]string{
		"script":             {"<section class=", "<script>fetch('/private')</script><section class="},
		"foreign-link":       {"<section class=", "<a href=\"https://outside.invalid/?private={{.Memory.Selected.Content}}\">outside</a><section class="},
		"image-effect":       {"<section class=", "<img src=\"/private/delete\"><section class="},
		"effect-attribute":   {"<section class=", "<section data-api-delete=\"private\" class="},
		"event-handler":      {"<section class=", "<section onclick=\"fetch('/private')\" class="},
		"foreign-controller": {"<section class=", "<section class=\"conversation-send\"></section><section class="},
		"style":              {"<section class=", "<section style=\"display:none\" class="},
		"hidden facts":       {"data-memory-content>", "hidden data-memory-content>"},
		"hidden ancestor":    {`<section class="memory-content-section">`, `<section hidden class="memory-content-section">`},
		"recursion":          {"{{define \"memory-page\"}}", "{{define \"memory-page\"}}{{template \"memory-page\" .}}"},
		"outside-model":      {"{{.Content}}", "{{.Server.Secrets}}"},
		"forged-body":        {"data-memory-content>{{.Content}}", "data-memory-content>FORGED PRIVATE BODY"},
	} {
		t.Run(name, func(t *testing.T) {
			altered := strings.Replace(string(source), change[0], change[1], 1)
			if altered == string(source) {
				t.Fatal("mutation did not change original source")
			}
			candidate, e := compileMemoryView(altered, memoryViewBindings(v, r), requestLayout(r))
			if e == nil {
				out, err := renderMemoryView(candidate, v)
				if err == nil || out != "" {
					t.Fatal("unsafe view accepted or leaked partial private output", out, err)
				}
			}
		})
	}
}

func TestMemoryViewIndependentCopyPreservesFactsAndRejectsRowAndGraphForgery(t *testing.T) {
	raw, e := os.ReadFile("../../plugin/memoryinspector/view/memory.html")
	if e != nil {
		t.Fatal(e)
	}
	source := string(raw)
	r := httptest.NewRequest("GET", "http://localhost/developer/memory", nil)
	t.Run("independent actual content heading", func(t *testing.T) {
		v := originalMemoryViewFixture(t, "document-detail")
		variant := strings.Replace(source, `<h3>{{tr "Memory content"}}</h3>`, `<h3>{{tr "Remembered content"}}</h3>`, 1)
		if variant == source {
			t.Fatal("variant unchanged")
		}
		view, e := compileMemoryView(variant, memoryViewBindings(v, r), requestLayout(r))
		if e != nil {
			t.Fatal(e)
		}
		out, e := renderMemoryView(view, v)
		if e != nil || !strings.Contains(string(out), "Remembered content") || !strings.Contains(string(out), "PRIVATE original") {
			t.Fatal("candidate did not own real heading or lost original facts", out, e)
		}
	})
	for name, change := range map[string][2]string{
		"remapped row link":        {`href="{{memoryURL $.Memory.Query "ref" .Ref}}"`, `href="/developer/memory?inspector_revision=sha256%3Aoriginal&amp;layer=transcripts"`},
		"forged row":               {"data-memory-ref=\"{{.Ref}}\"", "data-memory-ref=\"memory:foreign\""},
		"forged version":           {"value=\"{{.Presentation.ReaderRevision}}\"", "value=\"sha256:foreign\""},
		"foreign page id":          {"id=\"memory-page\"", "id=\"other-page\""},
		"cross-page filter target": {"data-list-filter-toggle=\"memory-filter-rail\"", "data-list-filter-toggle=\"model-secrets\""},
		"file control":             {"type=\"search\"", "type=\"file\""},
	} {
		t.Run(name, func(t *testing.T) {
			v := originalMemoryViewFixture(t, "document-list")
			variant := strings.ReplaceAll(source, change[0], change[1])
			if variant == source {
				t.Fatal("mutation unchanged")
			}
			view, e := compileMemoryView(variant, memoryViewBindings(v, r), requestLayout(r))
			if e == nil {
				out, e := renderMemoryView(view, v)
				if e == nil || out != "" {
					t.Fatal("forged control or identity accepted", out, e)
				}
			}
		})
	}
}

func TestMemoryViewShowsMaintenanceDocumentLinks(t *testing.T) {
	v := originalMemoryViewFixture(t, "maintenance-detail")
	v.MaintenanceSelected.Operation = "dream"
	v.MaintenanceSelected.Changes = []memory.MaintenanceChange{{Ref: "memory:note", Operation: "created"}, {Ref: "memory:new-revision", Operation: "replaced"}, {Ref: "memory:retired-record", Operation: "retired"}}
	for _, c := range v.MaintenanceSelected.Changes {
		url := "/developer/memory?inspector_revision=sha256%3Aoriginal&ref=" + url.QueryEscape(c.Ref)
		v.Presentation.Links = append(v.Presentation.Links, memory.InspectionLink{Key: "ref", Value: c.Ref, URL: url})
	}
	raw, err := os.ReadFile("../../plugin/memoryinspector/view/memory.html")
	if err != nil {
		t.Fatal(err)
	}
	for _, lang := range []string{"en", "zh-CN"} {
		r := languageRequest("GET", "/developer/memory", lang, "")
		view, err := compileMemoryView(string(raw), memoryViewBindings(v, r), requestLayout(r))
		if err != nil {
			t.Fatal(err)
		}
		typed, err := renderMemoryView(view, v)
		if err != nil {
			t.Fatal(err)
		}
		html := string(typed)
		labels := []string{"Created document", "Replaced document", "Retired document"}
		if lang == "zh-CN" {
			labels = []string{"新增", "替代", "撤回"}
		}
		for i, c := range v.MaintenanceSelected.Changes {
			if !strings.Contains(html, labels[i]) || !strings.Contains(html, "ref="+url.QueryEscape(c.Ref)+`">`+c.Ref+`</a>`) {
				t.Fatal("missing navigable change", lang, c)
			}
		}
	}
}

package ui

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/z-chenhao/assistant/internal/tool"
	"github.com/z-chenhao/assistant/internal/tool/codemode"
)

func TestPresentationToolDescribesAndValidatesActualComponentProps(t *testing.T) {
	definition := ShowDefinition()
	if _, err := tool.NewRegistry([]tool.Definition{definition}); err != nil {
		t.Fatal(err)
	}
	declaration := codemode.Describe(definition.Spec).Declaration
	for _, field := range []string{`"name": "Table"`, `"columns": ReadonlyArray<string>`, `"name": "generateSandboxedUi"`, `"jsFunctions": string`, `"jsExpressions": ReadonlyArray<string> | ""`} {
		if !strings.Contains(declaration, field) {
			t.Fatalf("Code Mode lost the actual component shape: %s", field)
		}
	}
	for index, props := range []string{
		`{"title":"Values","columns":["Name"],"rows":[["A"]],"source":"User"}`,
		`{"title":"Values","columns":"Name","rows":[["A"]],"source":"User"}`,
		`{"title":"Values","columns":["Name"],"rows":[["A"]]}`,
		`{"title":"Values","columns":["Name"],"rows":[["A"]],"source":"User","html":"<script>"}`,
	} {
		raw := `{"title":"Values","fallback":"A","components":[{"name":"Table","props":` + props + `}]}`
		want := index == 0
		if got := tool.Validate(definition.Spec.InputSchema, raw); got != want {
			t.Fatalf("tool schema accepted=%v, want %v: %s", got, want, props)
		}
	}
}

func TestSharedAxisLineSeriesAndFallback(t *testing.T) {
	props := `{"title":"Growth","description":"User data","unit":"万人","labels":["1月","2月"],"series":[{"name":"A","values":[12,15]},{"name":"B","values":[10,13]}]}`
	raw := `{"title":"Growth","fallback":"A and B","components":[{"name":"steward.lineChart","props":` + props + `}]}`
	w, ok := ParseWidget(raw)
	if !ok || !strings.Contains(nativeSummary(w.Components[0]), "B / 2月: 13 万人") {
		t.Fatal("multi-series lost values or unit", w)
	}
	for _, invalid := range []string{
		strings.Replace(raw, `[10,13]`, `[10]`, 1),
		strings.Replace(raw, `"name":"B"`, `"name":"A"`, 1),
		strings.Replace(raw, `"values":[10,13]`, `"values":[10,"13"]`, 1),
		strings.Replace(raw, `"labels":`, `"data":[],"labels":`, 1),
		strings.Replace(raw, `"series":[`, `"series":[],"invalid":[`, 1),
	} {
		if _, accepted := ParseWidget(invalid); accepted {
			t.Fatal("ambiguous or misaligned series accepted", invalid)
		}
	}
	if _, ok := ParsePreview(raw); !ok {
		t.Fatal("multi-series cannot stream a scriptless preview")
	}
}

func TestUpstreamNativeCatalogAndBoundMeetingResponse(t *testing.T) {
	c := Component{Name: "scheduleTime", Props: json.RawMessage(`{"reasonForScheduling":"Review","meetingDuration":30,"timeSlots":[{"date":"2026-11-07","time":"10:00"},{"date":"2026-11-08","time":"14:00"}]}`)}
	s := nativeSpec(t, []Component{c, c})
	if s.Components[0].FormID == s.Components[1].FormID {
		t.Fatal("distinct pickers share identity")
	}
	c = s.Components[0]
	content, err := Answer(c, map[string]string{"choice": "0"})
	if err != nil || !strings.Contains(content[0].Text, "2026-11-07 at 10:00") || !strings.Contains(content[0].Text, "does not book") {
		t.Fatal(content, err)
	}
	if err = Restore(&c, content); err != nil || !c.Submitted || c.Response != "0" {
		t.Fatal(c, err)
	}
	for _, choice := range []string{"01", "2", "-1", "forged"} {
		if _, err := Answer(c, map[string]string{"choice": choice}); err == nil {
			t.Fatal("unknown choice accepted", choice)
		}
	}
	if _, err := Answer(c, map[string]string{"choice": "0", "approval": "yes"}); err == nil {
		t.Fatal("extra authority accepted")
	}
}

func TestRetiredAndMalformedPresentationInputsAreRejected(t *testing.T) {
	for _, raw := range []string{
		`{"title":"T","fallback":"F","html":"<p>legacy</p>"}`,
		`{"title":"T","fallback":"F","components":[{"type":"markdown","text":"old"}]}`,
		`{"title":"T","fallback":"F","components":[{"name":"Table","props":{"title":"T","columns":["A"],"rows":[["a","b"]],"source":"User"}}]}`,
		`{"title":"T","fallback":"F","components":[{"name":"pieChart","props":{"title":"T","description":"D","data":[{"label":"A","value":-1}]}}]}`,
		`{"title":"T","fallback":"F","components":[{"name":"scheduleTime","props":{"reasonForScheduling":"R","meetingDuration":0,"timeSlots":[{"date":"tomorrow","time":"10"}]}}]}`,
	} {
		if _, ok := ParseWidget(raw); ok {
			t.Fatal("invalid presentation accepted", raw)
		}
	}
	if _, ok := StoredWidget(`{"title":"T","fallback":"Historical text","html":"<p>old</p>"}`); !ok {
		t.Fatal("immutable history became unreadable")
	}
}

func TestSandboxEmptyExpressionsNormalizeWithoutCoercingCode(t *testing.T) {
	for _, expressions := range []string{`[]`, `""`, `["initialize()"]`, `"initialize()"`, `null`, `{}`, `false`} {
		t.Run(expressions, func(t *testing.T) {
			raw := `{"title":"Roadmap","fallback":"Interview, build, pilot, productize.","components":[{"name":"generateSandboxedUi","props":{"initialHeight":460,"placeholderMessages":[],"css":"","html":"<p>Week 1–2: interview customers</p>","jsFunctions":"","jsExpressions":` + expressions + `}}]}`
			w, ok := ParseWidget(raw)
			want := expressions == `[]` || expressions == `""` || expressions == `["initialize()"]`
			if accepted := tool.Validate(ShowDefinition().Spec.InputSchema, raw); accepted != want {
				t.Fatalf("model-visible schema accepted=%v, want %v", accepted, want)
			}
			if ok != want {
				t.Fatalf("accepted=%v, want %v", ok, want)
			}
			if !ok {
				return
			}
			sandbox, ok := Sandbox(w)
			if !ok || sandbox.HTML != "<p>Week 1–2: interview customers</p>" {
				t.Fatal("normalized visual cannot be delivered", sandbox)
			}
			if expressions == `""` && !strings.Contains(string(w.Components[0].Props), `"jsExpressions":[]`) {
				t.Fatal("empty string was not normalized to the array contract")
			}
		})
	}
}

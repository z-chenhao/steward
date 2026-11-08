package ui

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestUIPreviewJSONPrefixes(t *testing.T) {
	raw := `{"title":"比较","fallback":"Evidence","components":[{"name":"Table","props":{"title":"Options","columns":["Name","Value"],"rows":[["A","10"],["B","20"]],"source":"User"}}]}`
	boundary := strings.Index(raw, `,["B"`)
	for _, tc := range []struct {
		raw  string
		rows int
	}{{raw[:boundary+1], 1}, {raw[:boundary+7], 1}, {raw, 2}} {
		preview, ok := ParsePreview(tc.raw)
		if !ok || len(preview.Components) != 1 {
			t.Fatalf("prefix lost table: %s %#v", tc.raw, preview)
		}
		var props nativeProps
		_ = json.Unmarshal(preview.Components[0].Props, &props)
		if len(props.Rows) != tc.rows || props.Rows[0][0] != "A" {
			t.Fatal(props)
		}
	}
	// Every byte boundary, including escapes and Unicode, is safe to inspect.
	for i := 0; i <= len(raw); i++ {
		ParsePreview(raw[:i])
	}
}
func TestUIPreviewStreamsHTMLWithoutExecutableFields(t *testing.T) {
	prefix := `{"title":"Visual","components":[{"name":"generateSandboxedUi","props":{"initialHeight":240,"css":"svg{width:100%}","html":"<svg><text>hello\n界`
	preview, ok := ParsePreview(prefix)
	if !ok || len(preview.Components) != 1 {
		t.Fatalf("HTML string prefix lost: %#v %v", preview, ok)
	}
	var props map[string]any
	_ = json.Unmarshal(preview.Components[0].Props, &props)
	if props["html"] != "<svg><text>hello\n界" {
		t.Fatal(props)
	}
	full := prefix + `</text></svg>","jsFunctions":"SECRET_EXECUTABLE","jsExpressions":["SECRET_EXECUTABLE()"]}}]}`
	preview, ok = ParsePreview(full)
	data, _ := json.Marshal(preview)
	if !ok || strings.Contains(string(data), "SECRET_EXECUTABLE") {
		t.Fatal(string(data), ok)
	}
	for _, tail := range []string{`\`, `\u`, `\u2`, `\u20`, `\u202`} {
		if _, ok := ParsePreview(prefix + tail); !ok {
			t.Fatal("unfinished escape rejected", tail)
		}
	}
}
func TestUIPreviewRejectsInvalidDataAndAuthority(t *testing.T) {
	for _, tc := range []struct {
		raw         string
		validPrefix bool
	}{
		{`{"title":"a","title":"b"}`, false},
		{`{"title":"a"} trailing`, false},
		{strings.Repeat(" ", 128<<10) + "x", false},
		{`{"components":[{"name":"Table","props":{"columns":["A"],"rows":[["A","B"]]}}]}`, true},
	} {
		p, ok := ParsePreview(tc.raw)
		if ok != tc.validPrefix || len(p.Components) != 0 {
			t.Fatal("invalid preview accepted", tc.raw, ok)
		}
	}
	raw := `{"title":"Actions","components":[{"name":"steward.action","props":{"id":"danger","tool":"external","arguments":{}}},{"name":"Table","props":{"columns":["Name"],"rows":[["A"]],"source":"User","widget_ref":"ui:forged"}}]}`
	p, ok := ParsePreview(raw)
	data, _ := json.Marshal(p)
	if !ok || len(p.Components) != 0 || strings.Contains(string(data), "forged") {
		t.Fatal(string(data), ok)
	}
}

func TestPreviewWaitsForCompleteCSSAndPreservesPublicText(t *testing.T) {
	prefix := `{"text":"Main receives the request.","title":"Roles","components":[{"name":"generateSandboxedUi","props":{"initialHeight":240,"css":"#roles{color:var(--color-text-primary)}`
	for _, test := range []struct {
		raw   string
		ready bool
	}{
		{prefix, false},
		{prefix + `}","html":"<svg><text>Main`, true},
	} {
		p, ok := ParsePreview(test.raw)
		if !ok || p.Text != "Main receives the request." || len(p.Components) != 1 {
			t.Fatal(p, ok)
		}
		var props struct {
			CSSComplete bool `json:"cssComplete"`
		}
		if err := json.Unmarshal(p.Components[0].Props, &props); err != nil || props.CSSComplete != test.ready {
			t.Fatal("partial CSS became visible", string(p.Components[0].Props), err)
		}
	}
}

func TestUIPreviewUsesCatalogCharacterLimitsForPublicText(t *testing.T) {
	for _, character := range []string{"中", "😀"} {
		for _, count := range []int{8192, 8193} {
			text := strings.Repeat(character, count)
			raw, _ := json.Marshal(map[string]any{"title": strings.Repeat(character, 200), "text": text, "components": []any{}})
			preview, ok := ParsePreview(string(raw))
			if ok != (count == 8192) || ok && preview.Text != text {
				t.Fatal("preview disagrees with catalog character limits", character, count, ok)
			}
		}
	}
}

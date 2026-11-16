package intelligentui

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/z-chenhao/assistant/pkg/rsiplugin"
	"golang.org/x/net/html"
)

// Expected DOM comes from the original React component in real Chrome, before
// this producer existed. Entity spelling is normalized through a real parser.
func TestUIPlanViewMatchesOriginalReactDOM(t *testing.T) {
	raw, err := os.ReadFile("testdata/original-plan.json")
	if err != nil {
		t.Fatal(err)
	}
	var cases []struct {
		Name     string          `json:"name"`
		Input    json.RawMessage `json:"input"`
		Expected struct {
			HTML   string `json:"html"`
			Closed bool   `json:"closed"`
		} `json:"expected"`
	}
	if err = json.Unmarshal(raw, &cases); err != nil {
		t.Fatal(err)
	}
	if len(cases) != 4 {
		t.Fatal("lost captured original plans")
	}
	canonical := func(raw string) string {
		t.Helper()
		node, err := html.Parse(strings.NewReader(raw))
		if err != nil {
			t.Fatal(err)
		}
		var out bytes.Buffer
		if err = html.Render(&out, node); err != nil {
			t.Fatal(err)
		}
		return out.String()
	}
	css, err := os.ReadFile("testdata/original-plan.css")
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range cases {
		t.Run(c.Name, func(t *testing.T) {
			view, err := renderPlanView(c.Input)
			if err != nil {
				t.Fatal(err)
			}
			if !c.Expected.Closed || canonical(view.HTML) != canonical(c.Expected.HTML) {
				t.Fatalf("original DOM changed: %s", view.HTML)
			}
			if view.CSS != strings.Replace(string(css), "@media(max-width:760px){.native-plan summary{min-height:44px}}", "html[data-host-compact] .native-plan summary{min-height:44px}", 1) {
				t.Fatal("original plan style changed")
			}
		})
	}
	for _, bad := range []string{`{}`, `{"approach":"A","technology":"Go","key_elements":[],"message_id":"forged"}`, `{"approach":"A","technology":"Go","key_elements":[42]}`} {
		if view, err := renderPlanView(json.RawMessage(bad)); err == nil || view.HTML != "" || view.CSS != "" {
			t.Fatal("invalid props produced a view", bad, view, err)
		}
	}
}

func TestUIPlanViewServiceRequiresImmutableFacts(t *testing.T) {
	for _, inputs := range [][]rsiplugin.Input{nil, {{ID: "ui-document", Size: 10}}, {{ID: "ui-plan", Size: (768 << 10) + 1}}} {
		if _, err := planViews(context.Background(), rsiplugin.Call{Input: json.RawMessage(`{}`), Inputs: inputs}); err == nil {
			t.Fatal("view service admitted wrong input scope", inputs)
		}
	}
}

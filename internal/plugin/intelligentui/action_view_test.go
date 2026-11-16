package intelligentui

import (
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/z-chenhao/assistant/internal/plugin/intelligentui/contract"
	"github.com/z-chenhao/assistant/pkg/rsiplugin"
)

func TestActionViewMatchesOriginalFeedbackAndKeepsAuthorityOut(t *testing.T) {
	raw, err := os.ReadFile("../../rsi/builtin/ui-action-original.json")
	if err != nil {
		t.Fatal(err)
	}
	var cases []struct {
		Document string          `json:"document"`
		Expected json.RawMessage `json:"expected"`
	}
	if json.Unmarshal(raw, &cases) != nil || len(cases) != 2 {
		t.Fatal("lost independent original action cases")
	}
	for _, sample := range cases {
		result := Normalize(sample.Document)
		encoded, _ := json.Marshal(result)
		got, _ := rsiplugin.Canonical(json.RawMessage(encoded))
		want, _ := rsiplugin.Canonical(sample.Expected)
		if string(got) != string(want) {
			t.Fatalf("original action feedback/identity changed\ngot %s\nwant %s", got, want)
		}
		views, _ := json.Marshal(result.ActionViews)
		for _, forbidden := range []string{`"tool"`, `"arguments"`, `"source"`, `"form_id"`, `"message_id"`, `"url"`, `"worker"`} {
			if strings.Contains(string(views), forbidden) {
				t.Fatal("display acquired authority", forbidden)
			}
		}
	}
	preview, ok := ParsePreview(cases[0].Document)
	if !ok || len(preview.Components) != 0 {
		t.Fatal("preview acquired an action before delivery")
	}
}

func TestActionViewReleaseBoundary(t *testing.T) {
	for _, version := range []string{"1.0.0", "1.23.0", "1.25.0", "1.26.0"} {
		if contract.ActionDisplayRequired(version) {
			t.Fatal("lost exact historical boundary", version)
		}
	}
	for _, version := range []string{"1.27.0", "1.28.0", "2.0.0", "invalid"} {
		if !contract.ActionDisplayRequired(version) {
			t.Fatal("unrecognized/new version allowed native policy", version)
		}
	}
}

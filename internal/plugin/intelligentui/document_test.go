package intelligentui

import (
	"encoding/json"
	"os"
	"reflect"
	"testing"
)

// Captured before this candidate existed from the real native implementation.
// No expected normalization or admission result is recomputed by the candidate.
func TestDocumentMatchesCapturedNativeBehavior(t *testing.T) {
	data, err := os.ReadFile("testdata/original-native.json")
	if err != nil {
		t.Fatal(err)
	}
	var cases []struct {
		Name       string          `json:"name"`
		Input      string          `json:"input"`
		Accepted   bool            `json:"accepted"`
		Normalized json.RawMessage `json:"normalized"`
		Readable   string          `json:"readable"`
		Components []struct {
			Text string `json:"text"`
		} `json:"components"`
	}
	if err = json.Unmarshal(data, &cases); err != nil {
		t.Fatal(err)
	}
	if len(cases) != 18 {
		t.Fatal("lost original cases")
	}
	for _, c := range cases {
		t.Run(c.Name, func(t *testing.T) {
			got := Normalize(c.Input)
			if (got.ErrorCode == "") != c.Accepted {
				t.Fatalf("admission=%+v want=%v", got, c.Accepted)
			}
			if !c.Accepted {
				if got.ErrorCode != "INVALID_ARGUMENT" || got.DocumentJSON != "" || got.Readable != "" {
					t.Fatal("invalid input produced a presentation", got)
				}
				return
			}
			expectedText := make([]string, len(c.Components))
			for i, v := range c.Components {
				expectedText[i] = v.Text
			}
			if !reflect.DeepEqual(got.ComponentText, expectedText) {
				t.Fatal("native component summaries changed", got.ComponentText, expectedText)
			}
			var actual, expected any
			if json.Unmarshal([]byte(got.DocumentJSON), &actual) != nil || json.Unmarshal(c.Normalized, &expected) != nil || !reflect.DeepEqual(actual, expected) || got.Readable != c.Readable {
				t.Fatalf("native document/text changed: %+v", got)
			}
		})
	}
}

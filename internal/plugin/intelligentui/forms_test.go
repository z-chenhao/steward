package intelligentui

import (
	"encoding/json"
	"os"
	"reflect"
	"testing"

	"github.com/z-chenhao/assistant/internal/plugin/intelligentui/contract"
)

// Expected results were captured from the native implementation before the
// form producer was written. The candidate never generates its own oracle.
func TestFormMatchesCapturedNativeBehavior(t *testing.T) {
	raw, err := os.ReadFile("testdata/original-form.json")
	if err != nil {
		t.Fatal(err)
	}
	var cases []struct {
		Name  string `json:"name"`
		Input struct {
			contract.FormFacts
			Operation string   `json:"operation"`
			Parts     []string `json:"parts"`
		} `json:"input"`
		Expected contract.FormResult `json:"expected"`
	}
	if err = json.Unmarshal(raw, &cases); err != nil {
		t.Fatal(err)
	}
	if len(cases) != 31 {
		t.Fatal("lost native form cases", len(cases))
	}
	for _, c := range cases {
		t.Run(c.Name, func(t *testing.T) {
			got := FormAnswer(c.Input.FormFacts)
			if c.Input.Operation == "restore" {
				response, err := restoreFormAnswer(Component{Name: c.Input.Name, Props: c.Input.Props}, c.Input.Parts)
				got = contract.FormResult{Parts: []string{}, ErrorCode: "CONFLICT"}
				if err == nil {
					got = contract.FormResult{Parts: c.Input.Parts, Response: response}
				}
			}
			if !reflect.DeepEqual(got, c.Expected) {
				t.Fatalf("got %#v; want %#v", got, c.Expected)
			}
		})
	}
}

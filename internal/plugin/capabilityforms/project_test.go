package capabilityforms

import (
	"encoding/json"
	"github.com/z-chenhao/assistant/internal/plugin/capabilityforms/contract"
	"os"
	"testing"
)

func TestCapabilityFormsMatchRetainedNativeOwnerCases(t *testing.T) {
	raw, err := os.ReadFile("../../rsi/builtin/capability-forms-owner-v1.json")
	if err != nil {
		t.Fatal(err)
	}
	var cases []struct {
		Input    contract.Request `json:"input"`
		Expected contract.Result  `json:"expected"`
	}
	if err = json.Unmarshal(raw, &cases); err != nil {
		t.Fatal(err)
	}
	if len(cases) != 2 {
		t.Fatal("native cases missing")
	}
	for _, c := range cases {
		got, _ := json.Marshal(Project(c.Input))
		want, _ := json.Marshal(c.Expected)
		if string(got) != string(want) {
			t.Fatal("native form behavior changed", string(got), string(want))
		}
	}
}

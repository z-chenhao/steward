package v1

import (
	"encoding/json"
	"github.com/z-chenhao/assistant/internal/plugin/capabilityforms/contract"
	"os"
	"testing"
)

func TestCapabilityPresentationMatchesNativeCatalogueAndProbeCopy(t *testing.T) {
	raw, err := os.ReadFile("../../../rsi/builtin/capability-presentation-owner-v1.json")
	if err != nil {
		t.Fatal(err)
	}
	var sample struct {
		Input    contract.PresentationFacts `json:"input"`
		Expected contract.Presentation      `json:"expected"`
	}
	if err = json.Unmarshal(raw, &sample); err != nil {
		t.Fatal(err)
	}
	got, _ := json.Marshal(ProjectPresentation(sample.Input))
	want, _ := json.Marshal(sample.Expected)
	if string(got) != string(want) {
		t.Fatal("native presentation changed")
	}
}

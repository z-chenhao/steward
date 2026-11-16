package traceinspector

import (
	"encoding/json"
	"os"
	"reflect"
	"testing"

	"github.com/z-chenhao/assistant/internal/plugin/traceinspector/contract"
)

func TestActivityPlanMatchesExecutedOriginalSource(t *testing.T) {
	raw, err := os.ReadFile("../../../tests/fixtures/plugins/trace-workbench-original-v1/activity-behavior.json")
	if err != nil {
		t.Fatal(err)
	}
	var cases []struct {
		Facts    contract.ActivityFacts `json:"facts"`
		Expected contract.ActivityPlan  `json:"expected"`
	}
	if err = json.Unmarshal(raw, &cases); err != nil {
		t.Fatal(err)
	}
	if len(cases) != 220 {
		t.Fatal("missing original activity cases", len(cases))
	}
	for i, c := range cases {
		got, err := ProjectActivity(c.Facts)
		if err != nil || !reflect.DeepEqual(got, c.Expected) {
			t.Fatalf("original case %d: got %#v want %#v error %v", i, got, c.Expected, err)
		}
	}
}

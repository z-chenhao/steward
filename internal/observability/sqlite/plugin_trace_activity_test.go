package sqlite

import (
	"encoding/json"
	"os"
	"reflect"
	"testing"

	"github.com/z-chenhao/assistant/internal/plugin/traceinspector/contract"
)

func TestTraceActivityPlanAcceptsOriginalRecipesAndRetainsNamedLegacy(t *testing.T) {
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
	for i, c := range cases {
		if err = acceptTraceActivityPlan(c.Facts, c.Expected); err != nil {
			t.Fatalf("original recipe %d rejected: %v", i, err)
		}
		if got := legacyTraceActivityPlanV1(c.Facts); !reflect.DeepEqual(got, c.Expected) {
			t.Fatalf("legacy boundary case %d changed", i)
		}
	}
}
func TestTraceActivityPlanCannotDiscardFactsOrChangeExplicitBounds(t *testing.T) {
	first, last, since, until := int64(1000000), int64(2000000), int64(500000), int64(2500000)
	facts := contract.ActivityFacts{First: &first, Last: &last, Since: &since, Until: &until}
	good := contract.ActivityPlan{Present: true, Start: since, End: until, Base: 0, Step: 1000000}
	if err := acceptTraceActivityPlan(facts, good); err != nil {
		t.Fatal(err)
	}
	for _, change := range []func(*contract.ActivityPlan){func(p *contract.ActivityPlan) { p.Present = false }, func(p *contract.ActivityPlan) { p.Step = 0 }, func(p *contract.ActivityPlan) { p.Step = -1 }, func(p *contract.ActivityPlan) { p.Start-- }, func(p *contract.ActivityPlan) { p.End++ }, func(p *contract.ActivityPlan) { p.End = p.Start }, func(p *contract.ActivityPlan) { p.Base -= p.Step }, func(p *contract.ActivityPlan) { p.Step = 1 }, func(p *contract.ActivityPlan) { p.Base = 1 << 62 }} {
		p := good
		change(&p)
		if acceptTraceActivityPlan(facts, p) == nil {
			t.Fatal("invalid counting recipe accepted", p)
		}
	}
	if acceptTraceActivityPlan(contract.ActivityFacts{}, good) == nil {
		t.Fatal("invented empty-source window accepted")
	}
	if acceptTraceActivityPlan(contract.ActivityFacts{}, contract.ActivityPlan{Step: 1}) == nil {
		t.Fatal("nonempty absent recipe accepted")
	}
}

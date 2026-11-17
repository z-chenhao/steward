package modelconnections

import (
	"encoding/json"
	"os"
	"reflect"
	"testing"

	"github.com/z-chenhao/assistant/internal/plugin/modelconnections/contract"
)

func TestMenuMatches96ExecutedNativeCases(t *testing.T) {
	raw, err := os.ReadFile("../../../tests/fixtures/plugins/model-connections-original-v1/menu-v1.json")
	if err != nil {
		t.Fatal(err)
	}
	var cases []struct {
		Facts    contract.Facts      `json:"facts"`
		Expected contract.Projection `json:"expected"`
	}
	if err = json.Unmarshal(raw, &cases); err != nil {
		t.Fatal(err)
	}
	if len(cases) != 96 {
		t.Fatal("missing original cases", len(cases))
	}
	for i, c := range cases {
		got := ProjectMenu(c.Facts)
		if !reflect.DeepEqual(got, c.Expected) {
			t.Errorf("case %d got %#v want %#v", i, got, c.Expected)
		}
	}
}

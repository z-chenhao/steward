package modelconnections

import (
	"encoding/json"
	"github.com/z-chenhao/assistant/internal/plugin/modelconnections/contract"
	"os"
	"reflect"
	"testing"
)

func TestSettingsMatches64ExecutedNativeCases(t *testing.T) {
	raw, err := os.ReadFile("../../../tests/fixtures/plugins/model-connections-original-v1/settings-v1.json")
	if err != nil {
		t.Fatal(err)
	}
	var cases []struct {
		Facts    contract.SettingsFacts      `json:"facts"`
		Expected contract.SettingsProjection `json:"expected"`
	}
	if err = json.Unmarshal(raw, &cases); err != nil {
		t.Fatal(err)
	}
	if len(cases) != 64 {
		t.Fatal(len(cases))
	}
	for i, c := range cases {
		got := ProjectSettings(c.Facts)
		if !reflect.DeepEqual(got, c.Expected) {
			t.Errorf("case %d got %#v want %#v", i, got, c.Expected)
		}
	}
}

func TestSettingsDraftMatches24ExecutedNativeCases(t *testing.T) {
	raw, err := os.ReadFile("../../../tests/fixtures/plugins/model-connections-original-v1/draft-v1.json")
	if err != nil {
		t.Fatal(err)
	}
	var cases []struct {
		Facts    contract.DraftFacts      `json:"facts"`
		Expected contract.DraftProjection `json:"expected"`
	}
	if err = json.Unmarshal(raw, &cases); err != nil {
		t.Fatal(err)
	}
	if len(cases) != 24 {
		t.Fatal(len(cases))
	}
	for i, c := range cases {
		out := ProjectSettings(contract.SettingsFacts{Rows: []contract.SettingsRow{}, Draft: &c.Facts})
		if !reflect.DeepEqual(out.Draft, &c.Expected) {
			t.Errorf("case %d: got %#v want %#v", i, out.Draft, c.Expected)
		}
	}
}

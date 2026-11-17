package modelconnections

import (
	"encoding/json"
	"github.com/z-chenhao/assistant/internal/plugin/modelconnections/contract"
	"os"
	"reflect"
	"testing"
)

func TestModelPickerMatches256ExecutedNativeStates(t *testing.T) {
	raw, err := os.ReadFile("../../../tests/fixtures/plugins/model-picker-native-v1/original.json")
	if err != nil {
		t.Fatal(err)
	}
	var expected contract.PickerPresentation
	if err = json.Unmarshal(raw, &expected); err != nil {
		t.Fatal(err)
	}
	actual := ProjectPicker()
	if !reflect.DeepEqual(actual, expected) {
		t.Fatal("original bilingual picker projection changed")
	}
	if len(actual.English.States) != 128 || len(actual.Chinese.States) != 128 {
		t.Fatal("state coverage incomplete")
	}
}

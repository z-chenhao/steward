package terminalinterface

import (
	"encoding/json"
	"github.com/z-chenhao/assistant/internal/plugin/terminalinterface/contract"
	"github.com/z-chenhao/assistant/internal/plugin/terminalinterface/legacyv1"
	"os"
	"reflect"
	"testing"
)

func TestTerminalOriginalVocabularyAndChoiceCases(t *testing.T) {
	if !reflect.DeepEqual(View(), legacyv1.View()) {
		t.Fatal("original terminal vocabulary drift")
	}
	raw, err := os.ReadFile("../../rsi/builtin/terminal-owner-v1.json")
	if err != nil {
		t.Fatal(err)
	}
	var cases []struct {
		Input    contract.Query
		Expected contract.Result
	}
	if err = json.Unmarshal(raw, &cases); err != nil {
		t.Fatal(err)
	}
	if len(cases) != 7 {
		t.Fatal("missing original owner cases")
	}
	for i, c := range cases {
		out, e := Project(c.Input)
		if e != nil || !reflect.DeepEqual(out, c.Expected) {
			t.Fatal(i, out, e)
		}
	}
	for _, f := range []contract.ChoiceFacts{{Count: -1, Exact: -1, Number: -1}, {Count: 1, Exact: 1, Number: -1}, {Count: 1, Exact: -1, Number: -2}} {
		if _, e := Project(contract.Query{Operation: "choose-model", Facts: &f}); e == nil {
			t.Fatal("invalid evidence accepted", f)
		}
	}
}

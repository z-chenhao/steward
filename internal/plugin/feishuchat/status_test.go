package feishuchat

import (
	"encoding/json"
	"github.com/z-chenhao/assistant/internal/plugin/feishuchat/contract"
	"os"
	"testing"
)

func TestFeishuStatusMatches12ExecutedNativeCases(t *testing.T) {
	raw, err := os.ReadFile("../../../tests/fixtures/plugins/feishu-native-v1/status.json")
	if err != nil {
		t.Fatal(err)
	}
	var cases []struct {
		State  string `json:"state"`
		Action string `json:"action"`
	}
	if err = json.Unmarshal(raw, &cases); err != nil {
		t.Fatal(err)
	}
	if len(cases) != 12 {
		t.Fatal("native cases missing", len(cases))
	}
	for _, c := range cases {
		if got := ProjectStatus(contract.StatusFacts{State: c.State}); got.Action != c.Action {
			t.Fatal(c.State, got, c.Action)
		}
	}
}

package schedule

import (
	"context"
	"encoding/json"
	"github.com/z-chenhao/assistant/internal/plugin/schedule/contract"
	"github.com/z-chenhao/assistant/pkg/rsiplugin"
	"testing"
)

func TestDefaultsPreserveOriginalDraftWithoutAuthority(t *testing.T) {
	out, err := Plugin().Call(context.Background(), rsiplugin.Call{Contract: contract.Defaults, Input: json.RawMessage(`{}`)})
	var value contract.DefaultsResult
	if err != nil || rsiplugin.Decode(out.Output, &value) != nil || value.Frequency != "daily" || value.Clock != "08:00" || value.Timezone != "UTC" || !value.Enabled || value.Weekday != "1" || value.Day != "1" || out.State != nil || out.OutputData != nil {
		t.Fatal(value, err)
	}
	if _, err = Plugin().Call(context.Background(), rsiplugin.Call{Contract: contract.Defaults, Input: json.RawMessage(`{"agent_id":"private"}`)}); err == nil {
		t.Fatal("internal authority accepted")
	}
}

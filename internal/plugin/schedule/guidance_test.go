package schedule

import (
	"context"
	"encoding/json"
	"github.com/z-chenhao/assistant/internal/plugin/schedule/contract"
	"github.com/z-chenhao/assistant/pkg/rsiplugin"
	"testing"
)

func TestGuidanceOnlyReturnsBoundedFeatureWording(t *testing.T) {
	out, err := Plugin().Call(context.Background(), rsiplugin.Call{Contract: contract.Guidance, Input: json.RawMessage(`{}`)})
	var value contract.GuidanceResult
	if err != nil || rsiplugin.Decode(out.Output, &value) != nil || value.Description == "" || value.Observation == nil || value.Observation.ToolDescription == "" || value.Observation.InstructionPrefix == "" || value.Observation.InstructionSuffix == "" || out.State != nil || out.OutputData != nil {
		t.Fatal(value, err)
	}
	if _, err = Plugin().Call(context.Background(), rsiplugin.Call{Contract: contract.Guidance, Input: json.RawMessage(`{"permission":"unsafe"}`)}); err == nil {
		t.Fatal("authority input accepted")
	}
}

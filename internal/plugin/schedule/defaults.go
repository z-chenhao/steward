package schedule

import (
	"context"
	"github.com/z-chenhao/assistant/internal/plugin/schedule/contract"
	"github.com/z-chenhao/assistant/pkg/rsiplugin"
)

func editorDefaults(ctx context.Context, call rsiplugin.Call) (rsiplugin.Result, error) {
	var input struct{}
	if err := rsiplugin.Decode(call.Input, &input); err != nil {
		return rsiplugin.Result{}, err
	}
	if len(call.Inputs) != 0 {
		return rsiplugin.Result{}, errInvalid
	}
	return rsiplugin.JSONResult(ctx, contract.DefaultsResult{MinBelow: 1, Frequency: "daily", Clock: "08:00", Timezone: "UTC", Enabled: true, Weekday: "1", Day: "1"})
}

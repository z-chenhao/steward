package intelligentui

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/z-chenhao/assistant/internal/plugin/intelligentui/contract"
	"github.com/z-chenhao/assistant/pkg/rsiplugin"
)

func Plugin() rsiplugin.Handlers {
	return rsiplugin.Handlers{PluginID: contract.PluginID, Version: "1.27.0", Services: map[string]func(context.Context, rsiplugin.Call) (rsiplugin.Result, error){contract.Normalize: normalize, contract.PreviewContract: preview, contract.FormContract: form, contract.PlanViews: planViews, contract.CardsViews: cardsViews, contract.TableViews: tableViews, contract.ChartViews: chartViews, contract.FormViews: formViews, contract.DefinitionContract: definition}}
}

// Normalize executes the actual catalog and normalization policy in the plugin.
// Reference identities, Main authorization and durable publication stay in Host.
func Normalize(raw string) contract.Result {
	widget, ok := ParseWidget(raw)
	if !ok {
		return contract.Result{ErrorCode: "INVALID_ARGUMENT"}
	}
	data, err := json.Marshal(widget)
	if err != nil {
		return contract.Result{ErrorCode: "INVALID_ARGUMENT"}
	}
	text := make([]string, len(widget.Components))
	for i, c := range widget.Components {
		if c.Name == "generateSandboxedUi" {
			text[i] = widget.Fallback
		} else {
			text[i] = nativeSummary(c)
		}
	}
	return contract.Result{DocumentJSON: string(data), Readable: widget.Readable(), ComponentText: text, MeetingViews: meetingViews(widget.Components), ActionViews: actionViews(widget.Components)}
}

func normalize(ctx context.Context, call rsiplugin.Call) (rsiplugin.Result, error) {
	var request struct{}
	if err := rsiplugin.Decode(call.Input, &request); err != nil {
		return rsiplugin.Result{}, err
	}
	if len(call.Inputs) != 1 || call.Inputs[0].ID != contract.DocumentInput || call.Inputs[0].Size < 0 || call.Inputs[0].Size > 128<<10 {
		return rsiplugin.Result{}, errors.New("invalid UI document input")
	}
	data, err := rsiplugin.ReadInput(ctx, call.Inputs[0])
	if err != nil {
		return rsiplugin.Result{}, err
	}
	return rsiplugin.JSONResult(ctx, Normalize(string(data)))
}

func preview(ctx context.Context, call rsiplugin.Call) (rsiplugin.Result, error) {
	var request struct{}
	if err := rsiplugin.Decode(call.Input, &request); err != nil {
		return rsiplugin.Result{}, err
	}
	if len(call.Inputs) != 1 || call.Inputs[0].ID != contract.DocumentInput || call.Inputs[0].Size < 0 || call.Inputs[0].Size > 128<<10 {
		return rsiplugin.Result{}, errors.New("invalid UI preview input")
	}
	data, err := rsiplugin.ReadInput(ctx, call.Inputs[0])
	if err != nil {
		return rsiplugin.Result{}, err
	}
	value, ok := ParsePreview(string(data))
	out := contract.PreviewResult{Valid: ok}
	if ok {
		encoded, err := json.Marshal(value)
		if err != nil {
			return rsiplugin.Result{}, err
		}
		out.PreviewJSON = string(encoded)
	}
	return rsiplugin.JSONResult(ctx, out)
}

func form(ctx context.Context, call rsiplugin.Call) (rsiplugin.Result, error) {
	var request struct {
		InputID string `json:"input_id"`
	}
	if err := rsiplugin.Decode(call.Input, &request); err != nil {
		return rsiplugin.Result{}, err
	}
	if request.InputID != contract.FormInput || len(call.Inputs) != 1 || call.Inputs[0].ID != contract.FormInput || call.Inputs[0].Size < 0 || call.Inputs[0].Size > 128<<10 {
		return rsiplugin.Result{}, errors.New("invalid UI form input")
	}
	data, err := rsiplugin.ReadInput(ctx, call.Inputs[0])
	if err != nil {
		return rsiplugin.Result{}, err
	}
	var facts contract.FormFacts
	if err = rsiplugin.DecodeResult(data, &facts); err != nil {
		return rsiplugin.Result{}, err
	}
	return rsiplugin.JSONResult(ctx, FormAnswer(facts))
}

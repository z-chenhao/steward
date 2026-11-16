package computeruse

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/z-chenhao/assistant/internal/plugin/computeruse/contract"
	"github.com/z-chenhao/assistant/pkg/rsiplugin"
)

func transferPresentation(ctx context.Context, call rsiplugin.Call) (rsiplugin.Result, error) {
	var facts contract.ActionFacts
	if err := rsiplugin.Decode(call.Input, &facts); err != nil {
		return rsiplugin.Result{}, err
	}
	if len(call.Inputs) != 1 || call.Inputs[0].ID != "arguments" || call.Inputs[0].Size > 1<<20 {
		return rsiplugin.Result{}, errors.New("missing bounded transfer display metadata")
	}
	raw, err := rsiplugin.ReadInput(ctx, call.Inputs[0])
	if err != nil {
		return rsiplugin.Result{}, err
	}
	var input contract.ActionArguments
	if err = json.Unmarshal(raw, &input); err != nil {
		return rsiplugin.Result{}, err
	}
	return rsiplugin.JSONResult(ctx, normalizeTransfer(facts, input.Args))
}
func normalizeTransfer(facts contract.ActionFacts, args []any) contract.ActionParameters {
	if facts.Method != "exportArtifact" {
		return contract.ActionParameters{Failure: "NOT_FOUND"}
	}
	var original map[string]any
	if len(args) > 0 {
		original, _ = args[0].(map[string]any)
	}
	text := func(v any) string { s, _ := v.(string); return s }
	p := contract.ActionParameters{Name: text(original["name"]), MediaType: text(original["mediaType"])}
	p.NameFromSource = p.Name == ""
	if p.MediaType == "" {
		p.MediaType = "application/octet-stream"
	}
	return p
}

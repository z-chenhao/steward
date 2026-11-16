package computeruse

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"unicode/utf8"

	"github.com/z-chenhao/assistant/internal/plugin/computeruse/contract"
	"github.com/z-chenhao/assistant/pkg/rsiplugin"
)

func Plugin() rsiplugin.Handlers {
	return rsiplugin.Handlers{PluginID: contract.PluginID, Version: "1.0.8", Services: map[string]func(context.Context, rsiplugin.Call) (rsiplugin.Result, error){
		contract.SelectionContract:            selectText,
		contract.InventoryOrderContract:       orderInventory,
		contract.ActionParametersContract:     actionParameters,
		contract.PlaywrightParametersContract: playwrightParameters,
		contract.TransferPresentationContract: transferPresentation,
		contract.BrowserUploadContract:        browserUploadParameters,
		contract.BrowserProjectionContract:    browserProjection,
		contract.TargetMatchContract:          matchTargets,
		contract.DocumentationContract: func(ctx context.Context, call rsiplugin.Call) (rsiplugin.Result, error) {
			var facts contract.DocumentationFacts
			if err := rsiplugin.Decode(call.Input, &facts); err != nil {
				return rsiplugin.Result{}, err
			}
			text := ""
			switch facts.Kind {
			case "core":
				text = coreDocumentation
			case "browser":
				text = browserDocumentation
			default:
				return rsiplugin.Result{}, errors.New("unknown documentation kind")
			}
			return rsiplugin.JSONResult(ctx, contract.Documentation{Text: text})
		},
		contract.WaitContract: func(ctx context.Context, call rsiplugin.Call) (rsiplugin.Result, error) {
			var facts contract.WaitFacts
			if err := rsiplugin.Decode(call.Input, &facts); err != nil {
				return rsiplugin.Result{}, err
			}
			timeout := min(1000, facts.Ceilings.TimeoutMS)
			return rsiplugin.JSONResult(ctx, contract.WaitRecipe{TimeoutMS: timeout, PollMS: facts.Ceilings.MinPollMS})
		},
		contract.ObservationContract: func(ctx context.Context, call rsiplugin.Call) (rsiplugin.Result, error) {
			var facts contract.ObservationFacts
			if err := rsiplugin.Decode(call.Input, &facts); err != nil {
				return rsiplugin.Result{}, err
			}
			if len(call.Inputs) != 1 || call.Inputs[0].ID != "observations" || call.Inputs[0].Size > 2<<20 {
				return rsiplugin.Result{}, errors.New("missing bounded observation snapshots")
			}
			raw, err := rsiplugin.ReadInput(ctx, call.Inputs[0])
			if err != nil {
				return rsiplugin.Result{}, err
			}
			var snapshots contract.ObservationSnapshots
			if json.Unmarshal(raw, &snapshots) != nil || len(snapshots.Before) > 128<<10 || len(snapshots.After) > 128<<10 || !utf8.ValidString(snapshots.Before) || !utf8.ValidString(snapshots.After) {
				return rsiplugin.Result{}, errors.New("invalid observation snapshots")
			}
			return rsiplugin.JSONResult(ctx, project(snapshots.Before, snapshots.After, facts))
		},
	}}
}

func project(before, after string, facts contract.ObservationFacts) contract.ObservationPlan {
	if !facts.DiffAllowed || facts.Full {
		return contract.ObservationPlan{Mode: "full"}
	}
	if before == after {
		return contract.ObservationPlan{Mode: "diff"}
	}
	a, b := strings.Split(before, "\n"), strings.Split(after, "\n")
	prefix := 0
	for prefix < len(a) && prefix < len(b) && a[prefix] == b[prefix] {
		prefix++
	}
	suffix := 0
	for suffix < len(a)-prefix && suffix < len(b)-prefix && a[len(a)-1-suffix] == b[len(b)-1-suffix] {
		suffix++
	}
	return contract.ObservationPlan{Mode: "diff", Start: prefix, Removed: len(a) - prefix - suffix, Added: len(b) - prefix - suffix}
}

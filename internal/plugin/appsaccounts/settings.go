package appsaccounts

import (
	"bytes"
	"context"
	"errors"
	"sort"

	"github.com/z-chenhao/assistant/internal/plugin/appsaccounts/contract"
	"github.com/z-chenhao/assistant/pkg/rsiplugin"
)

func settings(ctx context.Context, call rsiplugin.Call) (rsiplugin.Result, error) {
	var request contract.SettingsRequest
	if err := rsiplugin.Decode(call.Input, &request); err != nil {
		return rsiplugin.Result{}, err
	}
	var apps []string
	if request.Mode == "saved" {
		if len(call.Inputs) != 1 || call.Inputs[0].ID != contract.SettingsInputID {
			return rsiplugin.Result{}, errors.New("missing settings facts")
		}
		var buf bytes.Buffer
		if err := rsiplugin.ReadInputTo(ctx, call.Inputs[0], &buf); err != nil {
			return rsiplugin.Result{}, err
		}
		var err error
		apps, err = contract.DecodeSettingsApps(buf.Bytes(), request.Count)
		if err != nil {
			return rsiplugin.Result{}, err
		}
	} else if len(call.Inputs) != 0 {
		return rsiplugin.Result{}, errors.New("unexpected draft facts")
	}
	indices := chooseSettings(request, apps)
	if request.Offset < 0 || request.Offset > len(indices) {
		return rsiplugin.Result{}, errors.New("invalid settings window")
	}
	end := min(len(indices), request.Offset+contract.SettingsPageSize)
	return rsiplugin.JSONResult(ctx, contract.SettingsPlan{Indices: indices[request.Offset:end]})
}
func chooseSettings(request contract.SettingsRequest, apps []string) []int {
	indices := make([]int, 0, request.Count+1)
	for i := 0; i < request.Count; i++ {
		if request.Mode != "remove" || i != request.Remove {
			indices = append(indices, i)
		}
	}
	if request.Mode == "saved" {
		sort.Slice(indices, func(i, j int) bool { return apps[indices[i]] < apps[indices[j]] })
	}
	if request.Mode == "add" || len(indices) == 0 {
		indices = append(indices, -1)
	}
	return indices
}

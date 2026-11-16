package appsdirectory

import (
	"context"
	"encoding/binary"
	"errors"
	"github.com/z-chenhao/assistant/internal/plugin/appsdirectory/contract"
	"github.com/z-chenhao/assistant/pkg/rsiplugin"
)

func categories(ctx context.Context, call rsiplugin.Call) (rsiplugin.Result, error) {
	var request contract.CategoryRequest
	if err := rsiplugin.Decode(call.Input, &request); err != nil {
		return rsiplugin.Result{}, err
	}
	if request.Count < 0 || request.Count > rsiplugin.MaxInputBytes/4 || len(call.Inputs) != 1 || call.Inputs[0].ID != contract.CategoryInputID || call.Inputs[0].Size != int64(request.Count)*4 {
		return rsiplugin.Result{}, errors.New("invalid category facts")
	}
	raw, err := rsiplugin.ReadInput(ctx, call.Inputs[0])
	if err != nil {
		return rsiplugin.Result{}, err
	}
	keys := make([]uint32, request.Count)
	for i := range keys {
		keys[i] = binary.LittleEndian.Uint32(raw[i*4:])
	}
	plan := chooseCategories(keys, request)
	return rsiplugin.JSONResult(ctx, plan)
}

func chooseCategories(keys []uint32, request contract.CategoryRequest) contract.CategoryPlan {
	plan := contract.CategoryPlan{Keep: make([]byte, (len(keys)+7)/8)}
	seen := map[uint32]bool{}
	for i, key := range keys {
		if key == 0 || seen[key] {
			continue
		}
		seen[key] = true
		plan.Keep[i/8] |= 1 << uint(i%8)
	}
	plan.AppendRequested = request.HasRequested && !seen[request.RequestedKey]
	return plan
}

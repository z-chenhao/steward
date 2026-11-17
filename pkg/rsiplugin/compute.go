package rsiplugin

import (
	"context"
	"encoding/json"
	"errors"
)

type ComputeRequest struct {
	Contract string          `json:"contract"`
	Input    json.RawMessage `json:"input"`
}

type ComputeResult struct {
	Output json.RawMessage `json:"output"`
	Data   *Input          `json:"data,omitempty"`
}

type computeClientKey struct{}
type computeClient func(context.Context, ComputeRequest) (ComputeResult, error)

// Compute requests a Host-owned local, read-only computation over this call's
// authorized input. It grants no filesystem, credentials, network, activation
// or arbitrary process access. Host checks the contract and explicit permission.
// An optional bounded byte result is read through ReadInput in this same call.
func Compute(ctx context.Context, contract string, input, output any) (*Input, error) {
	client, ok := ctx.Value(computeClientKey{}).(computeClient)
	if !ok {
		return nil, errors.New("no computation context")
	}
	raw, err := json.Marshal(input)
	if err != nil {
		return nil, err
	}
	result, err := client(ctx, ComputeRequest{Contract: contract, Input: raw})
	if err != nil {
		return nil, err
	}
	if err = Decode(result.Output, output); err != nil {
		return nil, err
	}
	return result.Data, nil
}

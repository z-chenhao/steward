// Package webextract owns the private versioned HTML/PDF extraction service.
package webextract

import (
	"context"
	"errors"
	"github.com/z-chenhao/assistant/internal/plugin/webextract/contract"
	"github.com/z-chenhao/assistant/pkg/rsiplugin"
)

const Contract = contract.Contract
const EngineContract = contract.EngineContract
const ModuleFile = contract.ModuleFile

type Config = contract.Config
type Request = contract.Request
type Metadata = contract.Metadata
type Plan = contract.Plan

func Handlers(version string) rsiplugin.Handlers {
	return rsiplugin.Handlers{PluginID: "steward.web-extract", Version: version, Services: map[string]func(context.Context, rsiplugin.Call) (rsiplugin.Result, error){Contract: service}}
}
func service(ctx context.Context, call rsiplugin.Call) (rsiplugin.Result, error) {
	var request Request
	if err := rsiplugin.Decode(call.Input, &request); err != nil {
		return rsiplugin.Result{}, err
	}
	if len(call.Inputs) != 1 || request.InputID != "document" || call.Inputs[0].ID != request.InputID {
		return rsiplugin.Result{}, errors.New("missing original document")
	}
	var metadata Metadata
	data, err := rsiplugin.Compute(ctx, EngineContract, request, &metadata)
	if err != nil {
		return rsiplugin.Result{}, err
	}
	plan := Plan{Metadata: metadata}
	if data != nil {
		raw, err := rsiplugin.ReadInput(ctx, *data)
		if err != nil {
			return rsiplugin.Result{}, err
		}
		plan.Digest = rsiplugin.FileDigest(raw)
	}
	return rsiplugin.JSONResult(ctx, plan)
}

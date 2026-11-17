// Package imageocr owns recognition policy and normalized OCR documents in an
// independently built feature process. The Host owns scoped native computation.
package imageocr

import (
	"context"
	"errors"

	"github.com/z-chenhao/assistant/internal/plugin/imageocr/contract"
	"github.com/z-chenhao/assistant/pkg/rsiplugin"
)

func Plugin() rsiplugin.Handlers {
	layout := 3
	return rsiplugin.Handlers{PluginID: contract.PluginID, Version: "1.0.1",
		OnPrepare: func(_ context.Context, in rsiplugin.PrepareInput) error {
			var config struct {
				Layout int `json:"layout"`
			}
			if err := rsiplugin.Decode(in.Config, &config); err != nil {
				return err
			}
			if config.Layout != 0 {
				layout = config.Layout
			}
			return nil
		},
		Services: map[string]func(context.Context, rsiplugin.Call) (rsiplugin.Result, error){
			contract.Contract: func(ctx context.Context, call rsiplugin.Call) (rsiplugin.Result, error) {
				var req contract.Request
				if err := rsiplugin.Decode(call.Input, &req); err != nil {
					return rsiplugin.Result{}, err
				}
				if len(call.Inputs) != 1 || call.Inputs[0].ID != req.InputID {
					return rsiplugin.Result{}, errors.New("missing scoped image")
				}
				var meta contract.Metadata
				data, err := rsiplugin.Compute(ctx, contract.EngineContract, contract.EngineRequest{InputID: req.InputID, MediaType: req.MediaType, Languages: req.Languages, Layout: layout}, &meta)
				if err != nil {
					return rsiplugin.Result{}, err
				}
				if meta.ErrorCode != "" {
					return result(ctx, contract.Response{ErrorCode: meta.ErrorCode})
				}
				if data == nil {
					return rsiplugin.Result{}, errors.New("missing engine result")
				}
				return document(ctx, *data, meta.Width, meta.Height, meta.Languages)
			},
			contract.TSVContract: func(ctx context.Context, call rsiplugin.Call) (rsiplugin.Result, error) {
				var req contract.TSVRequest
				if err := rsiplugin.Decode(call.Input, &req); err != nil {
					return rsiplugin.Result{}, err
				}
				if len(call.Inputs) != 1 || call.Inputs[0].ID != req.InputID {
					return rsiplugin.Result{}, errors.New("missing scoped TSV")
				}
				return document(ctx, call.Inputs[0], req.Width, req.Height, req.Languages)
			},
		},
	}
}
func document(ctx context.Context, input rsiplugin.Input, width, height int, languages []string) (rsiplugin.Result, error) {
	raw, err := rsiplugin.ReadInput(ctx, input)
	if err != nil {
		return rsiplugin.Result{}, err
	}
	doc, err := parseTSV(raw, width, height, languages)
	if err != nil {
		return result(ctx, contract.Response{ErrorCode: "PROVIDER_UNAVAILABLE"})
	}
	return result(ctx, contract.Response{Document: &doc})
}
func result(ctx context.Context, value contract.Response) (rsiplugin.Result, error) {
	return rsiplugin.JSONResult(ctx, value)
}

// Package driver adapts a frozen native OCR engine to the Host compute port.
// It is linked into Host, never selected or implemented by a candidate plugin.
package driver

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/z-chenhao/assistant/internal/media"
	"github.com/z-chenhao/assistant/internal/media/ocr"
	"github.com/z-chenhao/assistant/internal/plugin/imageocr/contract"
	"github.com/z-chenhao/assistant/internal/rsi"
	"github.com/z-chenhao/assistant/pkg/rsiplugin"
)

type Engine interface {
	Raw(context.Context, string, []byte, ocr.Plan) (ocr.RawOutput, error)
	ResourceHash() string
	Check(context.Context) error
}

func New(engine Engine) *rsi.Compute {
	return &rsi.Compute{Identity: func(ctx context.Context) (string, error) {
		if engine == nil {
			return "image-ocr-v1:unavailable", nil
		}
		err := engine.Check(ctx)
		if errors.Is(err, ocr.ErrUnavailable) {
			err = nil
		}
		return engine.ResourceHash(), err
	}, Invoke: func(ctx context.Context, in rsiplugin.ComputeRequest, source rsi.InputData) (rsi.Computation, error) {
		if in.Contract != contract.EngineContract {
			return rsi.Computation{}, rsi.ErrRevoked
		}
		var req contract.EngineRequest
		if rsiplugin.Decode(in.Input, &req) != nil || req.InputID != source.ID {
			return rsi.Computation{}, rsi.ErrInvalid
		}
		if engine == nil {
			return rsi.Computation{Output: json.RawMessage(`{"width":0,"height":0,"languages":[],"error_code":"PROVIDER_UNAVAILABLE"}`)}, nil
		}
		value, err := engine.Raw(ctx, req.MediaType, source.Data, ocr.Plan{Languages: req.Languages, Layout: req.Layout})
		if ctx.Err() != nil {
			return rsi.Computation{}, ctx.Err()
		}
		meta := contract.Metadata{Width: value.Width, Height: value.Height, Languages: append([]string{}, value.Languages...)}
		if err != nil {
			meta.ErrorCode = "PROVIDER_UNAVAILABLE"
			switch {
			case errors.Is(err, media.ErrInvalid):
				meta.ErrorCode = "INVALID_ARGUMENT"
			case errors.Is(err, media.ErrLimit):
				meta.ErrorCode = "RESOURCE_LIMIT"
			case errors.Is(err, ocr.ErrChanged):
				meta.ErrorCode = "PERMISSION_DENIED"
			}
		}
		raw, err := json.Marshal(meta)
		if err != nil {
			return rsi.Computation{}, err
		}
		return rsi.Computation{Output: raw, Data: value.TSV}, nil
	}}
}

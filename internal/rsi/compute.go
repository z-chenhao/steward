package rsi

import (
	"bytes"
	"context"
	"encoding/json"

	webextract "github.com/z-chenhao/assistant/internal/plugin/webextract/contract"
	"github.com/z-chenhao/assistant/pkg/rsiplugin"
)

// Computation is a bounded, local read-only driver result. Implementations are
// trusted Host code; they receive only this invocation's already scoped bytes.
// This port cannot be registered by a package or used for external effects.
type Computation struct {
	Output json.RawMessage
	Data   []byte
}
type Compute struct {
	Identity func(context.Context) (string, error)
	Invoke   func(context.Context, rsiplugin.ComputeRequest, InputData) (Computation, error)
}

func computePermission(contract string) string {
	if contract == "steward.ocr-engine/1" {
		return "compute.ocr"
	}
	if contract == webextract.EngineContract {
		return "compute.web-extract"
	}
	return ""
}

func (h Host) withCompute(r Runner, scope *inputScope, check func(context.Context) error, binding rsiplugin.Binding, serviceContract string, original json.RawMessage) Runner {
	r.Compute = func(ctx context.Context, in rsiplugin.ComputeRequest) (rsiplugin.ComputeResult, error) {
		var result rsiplugin.ComputeResult
		if len(scope.descriptor) != 1 || computePermission(in.Contract) == "" {
			return result, ErrRevoked
		}
		if check != nil {
			if err := check(ctx); err != nil {
				return result, err
			}
		}
		source := InputData{ID: scope.descriptor[0].ID, Data: bytes.Clone(scope.data)}
		var value Computation
		var err error
		limit := 2 << 20
		if in.Contract == webextract.EngineContract {
			if serviceContract != webextract.Contract {
				return result, ErrRevoked
			}
			var expected, request webextract.Request
			if rsiplugin.Decode(original, &expected) != nil || rsiplugin.Decode(in.Input, &request) != nil || request != expected || request.InputID != source.ID {
				return result, ErrRevoked
			}
			scope.computedAttempted = true
			value, err = h.computeWebExtract(ctx, binding, request, source)
			limit = 8 << 20
		} else {
			if h.Compute == nil || h.Compute.Invoke == nil {
				return result, ErrRevoked
			}
			value, err = h.Compute.Invoke(ctx, in, source)
		}
		if err != nil {
			return result, err
		}
		if check != nil {
			if err = check(ctx); err != nil {
				return result, err
			}
		}
		if len(value.Output) > 65536 || !json.Valid(value.Output) || len(value.Data) > limit {
			return result, ErrInvalid
		}
		scope.computedOutput = bytes.Clone(value.Output)
		result.Output = value.Output
		if value.Data != nil {
			id := "computed-" + rsiplugin.FileDigest(value.Data)[7:]
			if id == scope.descriptor[0].ID {
				return result, ErrInvalid
			}
			scope.computed = bytes.Clone(value.Data)
			descriptor := rsiplugin.Input{ID: id, Size: int64(len(value.Data)), Digest: rsiplugin.FileDigest(value.Data)}
			scope.computedDescriptor = &descriptor
			result.Data = &descriptor
		}
		return result, nil
	}
	return r
}

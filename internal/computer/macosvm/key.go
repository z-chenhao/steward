package macosvm

import (
	"context"
	"errors"

	"github.com/z-chenhao/assistant/internal/computer"
)

func nativeKey(ctx context.Context, dispatch computer.KeyDispatch, base map[string]any, call func(string, map[string]any) (map[string]any, error)) (any, error) {
	if dispatch.BeforeInput == nil || len(dispatch.Recipe.Keys) < 1 || len(dispatch.Recipe.Keys) > 16385 {
		return nil, computer.ErrInvalid
	}
	if err := dispatch.BeforeInput(ctx); err != nil {
		return nil, err
	}
	args := make(map[string]any, len(base)+1)
	for k, v := range base {
		args[k] = v
	}
	method := "press_key"
	if len(dispatch.Recipe.Keys) == 1 {
		args["key"] = dispatch.Recipe.Keys[0]
	} else {
		method = "hotkey"
		args["keys"] = dispatch.Recipe.Keys
	}
	value, err := call(method, args)
	if err != nil {
		if errors.Is(err, computer.ErrUnknown) {
			return nil, computer.ErrUnknown
		}
		if errors.Is(err, computer.ErrInvalid) {
			return nil, computer.ErrInvalid
		}
		return nil, computer.ErrUnknown
	}
	return map[string]any{"effect": value["effect"], "verified": value["verified"]}, nil
}

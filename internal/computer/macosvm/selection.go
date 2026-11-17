package macosvm

import (
	"context"
	"errors"

	"github.com/z-chenhao/assistant/internal/computer"
)

// The isolated method can choose a caret program; only this fixed interpreter
// can send the two bounded navigation primitives to the original native target.
func nativeSelection(ctx context.Context, execution computer.SelectionDispatch, base map[string]any, call func(string, map[string]any) (map[string]any, error)) (any, error) {
	recipe := execution.Recipe
	if !recipe.Valid() || execution.BeforeInput == nil {
		return nil, computer.ErrInvalid
	}
	sent := 0
	dispatch := func(method string, key string, keys []string) error {
		if err := execution.BeforeInput(ctx); err != nil {
			if sent > 0 {
				return errors.Join(computer.ErrUnknown, err)
			}
			return err
		}
		if ctx.Err() != nil {
			return computer.ErrUnknown
		}
		args := make(map[string]any, len(base)+1)
		for name, value := range base {
			args[name] = value
		}
		if key != "" {
			args["key"] = key
		} else {
			args["keys"] = keys
		}
		if _, err := call(method, args); err != nil {
			return computer.ErrUnknown
		}
		sent++
		return nil
	}
	anchor := "up"
	if recipe.Anchor == "end" {
		anchor = "down"
	}
	if err := dispatch("hotkey", "", []string{"cmd", anchor}); err != nil {
		return nil, err
	}
	move, direction := recipe.Move, "right"
	if move < 0 {
		move, direction = -move, "left"
	}
	for n := 0; n < move; n++ {
		if err := dispatch("press_key", direction, nil); err != nil {
			return nil, err
		}
	}
	extend, direction := recipe.Extend, "right"
	if extend < 0 {
		extend, direction = -extend, "left"
	}
	for n := 0; n < extend; n++ {
		if err := dispatch("hotkey", "", []string{"shift", direction}); err != nil {
			return nil, err
		}
	}
	return map[string]any{"effect": "unverifiable"}, nil
}

package macosvm

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/z-chenhao/assistant/internal/computer"
)

func TestComputerNativeKeyUsesOriginalScopeAndOneFixedPrimitive(t *testing.T) {
	for _, keys := range [][]string{{"escape"}, {"cmd", "shift", "a"}, {"", ""}} {
		checked, sent := 0, 0
		result, err := nativeKey(context.Background(), computer.KeyDispatch{Recipe: computer.KeyRecipe{Keys: keys}, BeforeInput: func(context.Context) error { checked++; return nil }}, map[string]any{"pid": 100, "window_id": 10, "session": "original"}, func(method string, args map[string]any) (map[string]any, error) {
			sent++
			if checked != 1 || args["pid"] != 100 || args["window_id"] != 10 || args["session"] != "original" {
				t.Fatal("key input changed original authority", args)
			}
			if len(keys) == 1 {
				if method != "press_key" || args["key"] != keys[0] {
					t.Fatal(method, args)
				}
			} else if method != "hotkey" || !reflect.DeepEqual(args["keys"], keys) {
				t.Fatal(method, args)
			}
			return map[string]any{"effect": "confirmed", "private_native_id": "must not return"}, nil
		})
		if err != nil || sent != 1 || !reflect.DeepEqual(result, map[string]any{"effect": "confirmed", "verified": nil}) {
			t.Fatal(result, err, sent)
		}
	}
}
func TestComputerNativeKeyGuardAndUnknownNeverContinue(t *testing.T) {
	guard := errors.New("guard revoked")
	sent := 0
	invoke := func(string, map[string]any) (map[string]any, error) {
		sent++
		return nil, errors.New("PRIVATE_RPC_FAILURE")
	}
	_, err := nativeKey(context.Background(), computer.KeyDispatch{Recipe: computer.KeyRecipe{Keys: []string{"escape"}}, BeforeInput: func(context.Context) error { return guard }}, nil, invoke)
	if !errors.Is(err, guard) || sent != 0 {
		t.Fatal(err, sent)
	}
	_, err = nativeKey(context.Background(), computer.KeyDispatch{Recipe: computer.KeyRecipe{Keys: []string{"escape"}}, BeforeInput: func(context.Context) error { return nil }}, nil, invoke)
	if !errors.Is(err, computer.ErrUnknown) || sent != 1 {
		t.Fatal(err, sent)
	}
}

func TestComputerNativeKeyUnknownDominatesJoinedRefusal(t *testing.T) {
	_, err := nativeKey(context.Background(), computer.KeyDispatch{Recipe: computer.KeyRecipe{Keys: []string{"escape"}}, BeforeInput: func(context.Context) error { return nil }}, nil, func(string, map[string]any) (map[string]any, error) {
		return nil, errors.Join(computer.ErrUnknown, computer.ErrInvalid)
	})
	if !errors.Is(err, computer.ErrUnknown) || errors.Is(err, computer.ErrInvalid) {
		t.Fatal("uncertain native input downgraded to refusal", err)
	}
}

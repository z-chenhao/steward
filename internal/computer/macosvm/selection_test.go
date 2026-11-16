package macosvm

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/z-chenhao/assistant/internal/computer"
)

func TestComputerNativeSelectionInterpretsClosedProgramsOnOriginalTarget(t *testing.T) {
	for _, recipe := range []computer.SelectionRecipe{{Anchor: "start", Move: 4, Extend: 6}, {Anchor: "end", Move: -4, Extend: -6}} {
		t.Run(recipe.Anchor, func(t *testing.T) {
			base := map[string]any{"pid": 7, "window_id": 11, "session": "original-lease", "element_token": "accepted-element"}
			caret, anchor, count := 0, -1, 0
			var first []string
			value, err := nativeSelection(context.Background(), computer.SelectionDispatch{Recipe: recipe, BeforeInput: func(ctx context.Context) error { return ctx.Err() }}, base, func(method string, args map[string]any) (map[string]any, error) {
				count++
				for key, original := range base {
					if args[key] != original {
						t.Fatal("native scope changed", args)
					}
				}
				if len(args) != len(base)+1 {
					t.Fatal("unapproved native argument", args)
				}
				switch method {
				case "press_key":
					if args["key"] == "left" {
						caret--
					} else if args["key"] == "right" {
						caret++
					} else {
						t.Fatal("non-navigation input", args)
					}
				case "hotkey":
					keys := args["keys"].([]string)
					if count == 1 {
						first = keys
					}
					if reflect.DeepEqual(keys, []string{"cmd", "up"}) {
						caret = 0
					} else if reflect.DeepEqual(keys, []string{"cmd", "down"}) {
						caret = len("one target end")
					} else {
						if anchor == -1 {
							anchor = caret
						}
						if reflect.DeepEqual(keys, []string{"shift", "left"}) {
							caret--
						} else if reflect.DeepEqual(keys, []string{"shift", "right"}) {
							caret++
						} else {
							t.Fatal("arbitrary hotkey", args)
						}
					}
				default:
					t.Fatal("arbitrary driver method", method)
				}
				return map[string]any{}, nil
			})
			if err != nil || count != 11 || "one target end"[min(caret, anchor):max(caret, anchor)] != "target" || value.(map[string]any)["effect"] != "unverifiable" {
				t.Fatal("native program changed approved selection", caret, anchor, count, value, err)
			}
			want := []string{"cmd", "up"}
			if recipe.Anchor == "end" {
				want = []string{"cmd", "down"}
			}
			if !reflect.DeepEqual(first, want) {
				t.Fatal("independent end-anchor strategy did not reach native transport", first)
			}
		})
	}
}

func TestComputerNativeSelectionStopsOnUncertainInput(t *testing.T) {
	for _, failure := range []int{1, 4, 11} {
		calls := 0
		_, err := nativeSelection(context.Background(), computer.SelectionDispatch{Recipe: computer.SelectionRecipe{Anchor: "start", Move: 4, Extend: 6}, BeforeInput: func(ctx context.Context) error { return ctx.Err() }}, nil, func(string, map[string]any) (map[string]any, error) {
			calls++
			if calls == failure {
				return nil, context.DeadlineExceeded
			}
			return nil, nil
		})
		if !errors.Is(err, computer.ErrUnknown) || calls != failure {
			t.Fatal("uncertain key sequence continued or retried", failure, calls, err)
		}
	}
	calls := 0
	_, err := nativeSelection(context.Background(), computer.SelectionDispatch{Recipe: computer.SelectionRecipe{Anchor: "start", Move: 256, Extend: 1}, BeforeInput: func(ctx context.Context) error { return ctx.Err() }}, nil, func(string, map[string]any) (map[string]any, error) { calls++; return nil, nil })
	if !errors.Is(err, computer.ErrInvalid) || calls != 0 {
		t.Fatal("unbounded recipe reached native input", calls, err)
	}
}

func TestComputerNativeSelectionRechecksAuthorityBeforeEveryInput(t *testing.T) {
	revoked := errors.New("original native input authority revoked")
	for _, boundary := range []int{1, 4, 11} {
		guards, sent := 0, 0
		dispatch := computer.SelectionDispatch{Recipe: computer.SelectionRecipe{Anchor: "start", Move: 4, Extend: 6}, BeforeInput: func(context.Context) error {
			guards++
			if guards == boundary {
				return revoked
			}
			return nil
		}}
		_, err := nativeSelection(context.Background(), dispatch, nil, func(string, map[string]any) (map[string]any, error) {
			if guards != sent+1 {
				t.Fatal("native input preceded its current authority check")
			}
			sent++
			return nil, nil
		})
		if !errors.Is(err, revoked) || guards != boundary || sent != boundary-1 {
			t.Fatal("revocation sent or retried further keyboard input", boundary, guards, sent, err)
		}
		if errors.Is(err, computer.ErrUnknown) != (sent > 0) {
			t.Fatal("interrupted partial keyboard effect lost unknown outcome", boundary, sent, err)
		}
	}
}

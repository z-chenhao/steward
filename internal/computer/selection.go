package computer

import (
	"context"
	"strings"
)

type SelectionInput struct{ Value, Wanted, Role string }

// SelectionRecipe is a closed caret program, never arbitrary keystrokes.
// Host proves the selected source interval before any Driver input is sent.
type SelectionRecipe struct {
	Anchor string `json:"anchor"`
	Move   int    `json:"move"`
	Extend int    `json:"extend"`
}
type SelectionPlanner func(context.Context, SelectionInput) (SelectionRecipe, error)

// SelectionDispatch is process-local authority supplied only by UseRuntime.
// It is never a candidate DTO, persisted plan or browser/native RPC argument.
type SelectionDispatch struct {
	Recipe      SelectionRecipe             `json:"-"`
	BeforeInput func(context.Context) error `json:"-"`
}

func selectionSourceValid(in SelectionInput) bool {
	if in.Role != "AXTextField" && in.Role != "AXTextArea" || in.Wanted == "" || len(in.Wanted) > 256 || strings.Count(in.Value, in.Wanted) != 1 {
		return false
	}
	position := strings.Index(in.Value, in.Wanted)
	if position < 0 || position+len(in.Wanted) > 256 {
		return false
	}
	for _, char := range in.Value {
		if char > 127 || char == '\r' {
			return false
		}
	}
	return true
}

func (p SelectionRecipe) Valid() bool {
	return (p.Anchor == "start" || p.Anchor == "end") && p.Move >= -256 && p.Move <= 256 && p.Extend >= -256 && p.Extend <= 256 && p.Extend != 0 && absSelection(p.Move)+absSelection(p.Extend) <= 256
}

// ValidSelection proves the original approved substring, not a preferred recipe.
// Both directions and either anchor are valid if they select exactly that span.
func ValidSelection(in SelectionInput, p SelectionRecipe) bool {
	if !selectionSourceValid(in) || !p.Valid() {
		return false
	}
	caret := p.Move
	if p.Anchor == "end" {
		caret += len(in.Value)
	}
	end := caret + p.Extend
	if caret < 0 || caret > len(in.Value) || end < 0 || end > len(in.Value) {
		return false
	}
	start, finish := min(caret, end), max(caret, end)
	return start == strings.Index(in.Value, in.Wanted) && in.Value[start:finish] == in.Wanted
}

func absSelection(value int) int {
	if value < 0 {
		return -value
	}
	return value
}

func (r UseRuntime) selection(ctx context.Context, in SelectionInput) (SelectionRecipe, error) {
	if r.PlanSelection != nil {
		return r.PlanSelection(ctx, in)
	}
	// Exact facade-v1 compatibility for Runs without the managed selection
	// contract and explicit unbundled carriers. Managed dispatch always supplies
	// a planner. Remove when no retained native Run/carrier consumes this path.
	return SelectionRecipe{Anchor: "start", Move: strings.Index(in.Value, in.Wanted), Extend: len(in.Wanted)}, nil
}

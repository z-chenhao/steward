package computeruse

import (
	"context"
	"errors"
	"strings"

	"github.com/z-chenhao/assistant/internal/plugin/computeruse/contract"
	"github.com/z-chenhao/assistant/pkg/rsiplugin"
)

func selectText(ctx context.Context, call rsiplugin.Call) (rsiplugin.Result, error) {
	var text contract.SelectionFacts
	if err := rsiplugin.Decode(call.Input, &text); err != nil {
		return rsiplugin.Result{}, err
	}
	position := strings.Index(text.Prefix, text.Wanted)
	if len(text.Prefix) > 256 || text.Length < len(text.Prefix) || text.Wanted == "" || position < 0 || strings.Count(text.Prefix, text.Wanted) != 1 || position+len(text.Wanted) > 256 {
		return rsiplugin.Result{}, errors.New("unsupported selection")
	}
	return rsiplugin.JSONResult(ctx, contract.SelectionRecipe{Anchor: "start", Move: position, Extend: len(text.Wanted)})
}

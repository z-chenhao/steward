package schedule

import (
	"context"
	"github.com/z-chenhao/assistant/internal/plugin/schedule/contract"
	"github.com/z-chenhao/assistant/pkg/rsiplugin"
	"strings"
	"unicode/utf8"
)

func instructionLabel(ctx context.Context, call rsiplugin.Call) (rsiplugin.Result, error) {
	var facts contract.InstructionLabelFacts
	if err := rsiplugin.Decode(call.Input, &facts); err != nil {
		return rsiplugin.Result{}, err
	}
	if len(call.Inputs) != 0 || !utf8.ValidString(facts.Text) || len(facts.Text) > 8192 || strings.TrimSpace(facts.Text) == "" {
		return rsiplugin.Result{}, errInvalid
	}
	value := []rune(strings.Join(strings.Fields(facts.Text), " "))
	if len(value) > 80 {
		value = value[:80]
	}
	return rsiplugin.JSONResult(ctx, contract.InstructionLabelResult{Label: string(value)})
}

package mcpbridge

import (
	"context"
	"errors"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/z-chenhao/assistant/internal/plugin/mcpbridge/contract"
	"github.com/z-chenhao/assistant/pkg/rsiplugin"
)

func namespace(ctx context.Context, call rsiplugin.Call) (rsiplugin.Result, error) {
	var input contract.NamespaceRequest
	if err := rsiplugin.Decode(call.Input, &input); err != nil {
		return rsiplugin.Result{}, err
	}
	if input.InputID != "source" || len(call.Inputs) != 1 || call.Inputs[0].ID != input.InputID {
		return rsiplugin.Result{}, errors.New("missing original namespace source")
	}
	raw, err := rsiplugin.ReadInput(ctx, call.Inputs[0])
	if err != nil {
		return rsiplugin.Result{}, err
	}
	if !utf8.Valid(raw) {
		return rsiplugin.Result{}, errors.New("invalid namespace source encoding")
	}
	return rsiplugin.JSONResult(ctx, namespacePlan(string(raw)))
}

func namespacePlan(source string) contract.NamespacePlan {
	start := len(source) - len(strings.TrimLeftFunc(source, unicode.IsSpace))
	line := strings.SplitN(strings.TrimSpace(source), "\n", 2)[0]
	runes := []rune(line)
	truncated := len(runes) > 512
	if truncated {
		line = string(runes[:512])
	}
	return contract.NamespacePlan{Start: start, End: start + len(line), Truncated: truncated}
}

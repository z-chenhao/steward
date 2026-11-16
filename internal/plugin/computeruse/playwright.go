package computeruse

import (
	"context"
	"encoding/json"
	"errors"
	"math"
	"strings"
	"unicode/utf16"

	"github.com/z-chenhao/assistant/internal/plugin/computeruse/contract"
	"github.com/z-chenhao/assistant/pkg/rsiplugin"
)

func playwrightParameters(ctx context.Context, call rsiplugin.Call) (rsiplugin.Result, error) {
	facts, args, err := readPlaywrightArguments(ctx, call)
	if err != nil {
		return rsiplugin.Result{}, err
	}
	return rsiplugin.JSONResult(ctx, normalizePlaywright(facts, args))
}

func readPlaywrightArguments(ctx context.Context, call rsiplugin.Call) (contract.ActionFacts, []any, error) {
	var facts contract.ActionFacts
	if err := rsiplugin.Decode(call.Input, &facts); err != nil {
		return facts, nil, err
	}
	if len(call.Inputs) != 1 || call.Inputs[0].ID != "arguments" || call.Inputs[0].Size > 1<<20 {
		return facts, nil, errors.New("missing bounded Playwright arguments")
	}
	raw, err := rsiplugin.ReadInput(ctx, call.Inputs[0])
	if err != nil {
		return facts, nil, err
	}
	var input contract.ActionArguments
	if err = json.Unmarshal(raw, &input); err != nil {
		return facts, nil, err
	}
	return facts, input.Args, nil
}

func normalizePlaywright(facts contract.ActionFacts, args []any) contract.ActionParameters {
	invalid := func() contract.ActionParameters { return contract.ActionParameters{Failure: "INVALID_ARGUMENT"} }
	var original map[string]any
	if len(args) > 0 {
		original, _ = args[0].(map[string]any)
	}
	p := contract.ActionParameters{}
	timeout := 3000
	if explicit, ok := original["timeoutMs"]; ok {
		n, ok := explicit.(float64)
		if !ok || math.IsNaN(n) || math.IsInf(n, 0) || math.Trunc(n) != n || n < 1 || n > 10000 {
			return invalid()
		}
		timeout = int(n)
	}
	p.TimeoutMS = &timeout
	switch facts.Method {
	case "playwright.count", "playwright.textContent", "playwright.click", "playwright.fill", "playwright.waitForSelector", "playwright.download", "playwright.upload":
		var ok bool
		p.Selector, ok = original["selector"].(string)
		if !ok || p.Selector == "" || len(utf16.Encode([]rune(p.Selector))) > 2048 || strings.Contains(p.Selector, ">>") {
			return invalid()
		}
		if facts.Method == "playwright.fill" {
			p.Value, ok = original["value"].(string)
			if !ok {
				return invalid()
			}
		}
	case "playwright.waitForURL":
		var ok bool
		p.URL, ok = original["url"].(string)
		if !ok {
			return invalid()
		}
	default:
		return contract.ActionParameters{Failure: "NOT_FOUND"}
	}
	return p
}

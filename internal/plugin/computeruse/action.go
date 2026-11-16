package computeruse

import (
	"context"
	"encoding/json"
	"errors"
	"regexp"
	"strings"

	"github.com/z-chenhao/assistant/internal/plugin/computeruse/contract"
	"github.com/z-chenhao/assistant/pkg/rsiplugin"
)

var actionBundleID = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9.-]{0,254}$`)

func actionParameters(ctx context.Context, call rsiplugin.Call) (rsiplugin.Result, error) {
	var facts contract.ActionFacts
	if err := rsiplugin.Decode(call.Input, &facts); err != nil {
		return rsiplugin.Result{}, err
	}
	var input contract.ActionArguments
	if len(call.Inputs) != 1 || call.Inputs[0].ID != "arguments" || call.Inputs[0].Size > 1<<20 {
		return rsiplugin.Result{}, errors.New("missing bounded action arguments")
	}
	raw, err := rsiplugin.ReadInput(ctx, call.Inputs[0])
	if err != nil {
		return rsiplugin.Result{}, err
	}
	if err = json.Unmarshal(raw, &input); err != nil {
		return rsiplugin.Result{}, err
	}
	return rsiplugin.JSONResult(ctx, normalizeAction(facts, input.Args))
}
func normalizeAction(facts contract.ActionFacts, args []any) contract.ActionParameters {
	p := contract.ActionParameters{}
	get := func(i int) any {
		if i >= len(args) {
			return nil
		}
		return args[i]
	}
	text := func(v any) string { s, _ := v.(string); return s }
	object := func(v any) map[string]any { m, _ := v.(map[string]any); return m }
	invalid := func() contract.ActionParameters { return contract.ActionParameters{Failure: "INVALID_ARGUMENT"} }
	switch facts.Method {
	case "launchApp":
		p.App = text(object(get(0))["app"])
		if !actionBundleID.MatchString(p.App) {
			return invalid()
		}
	case "createBrowserTab":
		p.Browser, p.URL = text(get(0)), text(get(1))
		if p.Browser == "" {
			return invalid()
		}
	case "click":
		opts := object(get(1))
		for k := range opts {
			if k != "button" && k != "count" {
				return invalid()
			}
		}
		p.Button = text(opts["button"])
		if p.Button != "" && p.Button != "left" && p.Button != "right" {
			return invalid()
		}
		if n, ok := opts["count"]; ok {
			v, ok := n.(float64)
			if !ok || v != 1 && v != 2 {
				return invalid()
			}
			count := int(v)
			p.Count = &count
		}
	case "scroll":
		p.Direction = text(get(1))
		if p.Direction == "" || !strings.Contains("|up|down|left|right|", "|"+p.Direction+"|") {
			return invalid()
		}
		if amount := get(2); amount != nil {
			v, ok := amount.(float64)
			if !ok || v <= 0 || v > 10000 {
				return invalid()
			}
			p.Amount = &v
		}
	case "drag":
	case "setValue", "selectText", "performSecondaryAction":
		p.Value = text(get(1))
	case "typeText", "paste", "pressKey":
		offset := 0
		if facts.Surface == "tab" {
			offset = 1
		}
		if facts.Method == "paste" {
			format := text(object(get(offset + 1))["format"])
			if format != "" && format != "text" {
				return invalid()
			}
		}
		p.Text = text(get(offset))
		if p.Text == "" || len(p.Text) > 16384 {
			return invalid()
		}
		if facts.Method == "pressKey" && facts.Surface != "tab" {
			p.KeyInput = &contract.KeyRecipe{Keys: strings.Split(p.Text, "+")}
		}
	case "goto", "back", "forward", "reload", "close":
		if facts.Surface != "tab" {
			return contract.ActionParameters{Failure: "NOT_FOUND"}
		}
		p.URL = text(get(0))
	default:
		return contract.ActionParameters{Failure: "NOT_FOUND"}
	}
	return p
}

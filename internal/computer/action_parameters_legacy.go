package computer

import "strings"

// Only pre-parameters-contract Runs (including old three/four/six-service
// packages) and explicit unbundled carriers consume this facade-v1 parser.
// Remove it once retained executions/carriers no longer depend on this boundary.
func nativeActionParametersV1(method, surface string, args []any) ActionParameters {
	p := ActionParameters{}
	get := func(i int) any {
		if i >= len(args) {
			return nil
		}
		return args[i]
	}
	text := func(v any) string { s, _ := v.(string); return s }
	object := func(v any) map[string]any { m, _ := v.(map[string]any); return m }
	invalid := func() ActionParameters { return ActionParameters{Failure: "INVALID_ARGUMENT"} }
	switch method {
	case "launchApp":
		p.App = text(object(get(0))["app"])
		if !bundleID.MatchString(p.App) {
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
		if surface == "tab" {
			offset = 1
		}
		if method == "paste" {
			format := text(object(get(offset + 1))["format"])
			if format != "" && format != "text" {
				return invalid()
			}
		}
		p.Text = text(get(offset))
		if p.Text == "" || len(p.Text) > 16384 {
			return invalid()
		}
		if method == "pressKey" && surface != "tab" {
			p.KeyInput = &KeyRecipe{Keys: strings.Split(p.Text, "+")}
		}
	case "goto", "back", "forward", "reload", "close":
		if surface != "tab" {
			return ActionParameters{Failure: "NOT_FOUND"}
		}
		p.URL = text(get(0))
	default:
		return ActionParameters{Failure: "NOT_FOUND"}
	}
	return p
}

package computer

import (
	"context"
	"encoding/json"
	"math"
	"strings"
)

// ActionParameters contains only business parameters. Original handles,
// pointer/element resolution, revisions, authority and effects remain in Host.
type ActionParameters struct {
	NameFromSource bool       `json:"name_from_source,omitempty"`
	Name           string     `json:"name,omitempty"`
	MediaType      string     `json:"media_type,omitempty"`
	Selector       string     `json:"selector,omitempty"`
	TimeoutMS      *int       `json:"timeout_ms,omitempty"`
	KeyInput       *KeyRecipe `json:"key_input,omitempty"`
	Button         string     `json:"button,omitempty"`
	Count          *int       `json:"count,omitempty"`
	Direction      string     `json:"direction,omitempty"`
	Amount         *float64   `json:"amount,omitempty"`
	Text           string     `json:"text,omitempty"`
	Value          string     `json:"value,omitempty"`
	URL            string     `json:"url,omitempty"`
	App            string     `json:"app,omitempty"`
	Browser        string     `json:"browser,omitempty"`
	Failure        string     `json:"failure,omitempty"`
}
type ActionParameterReader func(context.Context, string, string) (ActionParameters, error)

func ActionParameterMethod(operation, method string) bool {
	switch operation {
	case "builtin__computer_discover":
		return method == "launchApp" || method == "exportArtifact"
	case "builtin__computer_bind":
		return method == "createBrowserTab"
	case "builtin__computer_invoke":
		if PlaywrightParameterMethod(method) {
			return true
		}
		switch method {
		case "click", "scroll", "drag", "setValue", "selectText", "performSecondaryAction", "typeText", "pressKey", "paste", "goto", "back", "forward", "reload", "close":
			return true
		}
	}
	return false
}

// ValidActionParameters enforces the original explicitly supplied values and
// the closed action bounds. It does not choose defaults or repeat the plugin's
// argument parser. Only missing click/scroll options are replaceable policy;
// their resolved values are part of the immutable approval preview.
func ValidActionParameters(method, surface string, args []any, p ActionParameters) bool {
	if p.Failure != "" {
		q := p
		q.Failure = ""
		return (p.Failure == "INVALID_ARGUMENT" || p.Failure == "NOT_FOUND") && emptyActionParameters(q)
	}
	q := p
	switch method {
	case "exportArtifact":
		return validTransferPresentation(args, p)
	case "playwright.count", "playwright.textContent", "playwright.click", "playwright.fill", "playwright.waitForSelector", "playwright.waitForURL", "playwright.download", "playwright.upload":
		return validPlaywrightParameters(method, args, p)
	case "launchApp":
		if p.App != str(object(arg(args, 0))["app"]) || !bundleID.MatchString(p.App) {
			return false
		}
		q.App = ""
	case "createBrowserTab":
		if p.Browser == "" || p.Browser != str(arg(args, 0)) || p.URL != str(arg(args, 1)) {
			return false
		}
		q.Browser, q.URL = "", ""
	case "click":
		if p.Button != "" && p.Button != "left" && p.Button != "right" || p.Count != nil && *p.Count != 1 && *p.Count != 2 {
			return false
		}
		opts := object(arg(args, 1))
		if explicit := str(opts["button"]); explicit != "" && explicit != p.Button {
			return false
		}
		if n, ok := opts["count"]; ok {
			v, ok := n.(float64)
			if !ok || p.Count == nil || float64(*p.Count) != v {
				return false
			}
		}
		q.Button = ""
		q.Count = nil
	case "scroll":
		if p.Direction != str(arg(args, 1)) || p.Direction != "up" && p.Direction != "down" && p.Direction != "left" && p.Direction != "right" {
			return false
		}
		if p.Amount != nil && (math.IsNaN(*p.Amount) || math.IsInf(*p.Amount, 0) || *p.Amount <= 0 || *p.Amount > 10000) {
			return false
		}
		if explicit := arg(args, 2); explicit != nil {
			v, ok := explicit.(float64)
			if !ok || p.Amount == nil || v != *p.Amount {
				return false
			}
		}
		q.Direction = ""
		q.Amount = nil
	case "drag":
	case "setValue", "selectText", "performSecondaryAction":
		if p.Value != str(arg(args, 1)) {
			return false
		}
		q.Value = ""
	case "typeText", "pressKey", "paste":
		offset := 0
		if surface == "tab" {
			offset = 1
		}
		if p.Text != str(arg(args, offset)) || len(p.Text) == 0 || len(p.Text) > 16384 {
			return false
		}
		if method == "pressKey" && surface != "tab" {
			if p.KeyInput == nil || len(p.KeyInput.Keys) < 1 || len(p.KeyInput.Keys) > 16385 || strings.Join(p.KeyInput.Keys, "+") != p.Text {
				return false
			}
			for _, key := range p.KeyInput.Keys {
				if strings.Contains(key, "+") {
					return false
				}
			}
			q.KeyInput = nil
		}
		q.Text = ""
	case "goto", "back", "forward", "reload", "close":
		if surface != "tab" || p.URL != str(arg(args, 0)) {
			return false
		}
		q.URL = ""
	default:
		return false
	}
	return emptyActionParameters(q)
}
func emptyActionParameters(p ActionParameters) bool {
	raw, _ := json.Marshal(p)
	return string(raw) == "{}"
}

func (r UseRuntime) actionParameters(ctx context.Context, method, surface string, args []any) (ActionParameters, error) {
	if r.ReadActionParameters != nil {
		return r.ReadActionParameters(ctx, method, surface)
	}
	return nativeActionParametersV1(method, surface, args), nil
}

// KeyRecipe is a lossless decomposition of the original approved key string.
type KeyRecipe struct {
	Keys []string `json:"keys"`
}

package computer

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestComputerPlaywrightParametersPreserveOriginalIntentAndExcludeAuthority(t *testing.T) {
	budget := 4500
	for _, selector := range []string{strings.Repeat("字", 1024), strings.Repeat("😀", 1024)} {
		if !ValidActionParameters("playwright.count", "tab", []any{map[string]any{"selector": selector}}, ActionParameters{Selector: selector, TimeoutMS: &budget}) {
			t.Fatal("original JS UTF-16 selector budget narrowed")
		}
	}
	tooLong := strings.Repeat("😀", 1025)
	if ValidActionParameters("playwright.count", "tab", []any{map[string]any{"selector": tooLong}}, ActionParameters{Selector: tooLong, TimeoutMS: &budget}) {
		t.Fatal("JS UTF-16 selector budget expanded")
	}
	args := []any{map[string]any{"selector": "#field", "value": "original", "timeoutMs": 7.0, "file": map[string]any{"computerId": "PRIVATE_COMPUTER", "path": "imports/PRIVATE_PATH"}, "element_token": "PRIVATE_TOKEN", "expectedNavigation": "PRIVATE_NAV", "tabId": "PRIVATE_TAB", "x": 1}}
	raw, _ := json.Marshal(PlaywrightParameterArguments("playwright.fill", args))
	if strings.Contains(string(raw), "PRIVATE_") || string(raw) != `[{"selector":"#field","timeoutMs":7,"value":"original"}]` {
		t.Fatal("descriptor exposed authority", string(raw))
	}
	for _, sample := range []struct {
		method   string
		original map[string]any
		plan     ActionParameters
		valid    bool
	}{
		{"playwright.fill", map[string]any{"selector": "#field", "value": "original"}, ActionParameters{Selector: "#field", Value: "original", TimeoutMS: &budget}, true},
		{"playwright.fill", map[string]any{"selector": "#field", "value": "original"}, ActionParameters{Selector: "#other", Value: "original", TimeoutMS: &budget}, false},
		{"playwright.fill", map[string]any{"selector": "#field", "value": "original"}, ActionParameters{Selector: "#field", Value: "replacement", TimeoutMS: &budget}, false},
		{"playwright.count", map[string]any{"selector": "#field", "timeoutMs": 7.0}, ActionParameters{Selector: "#field", TimeoutMS: &budget}, false},
		{"playwright.count", map[string]any{"selector": "#field"}, ActionParameters{Selector: "#field", TimeoutMS: &budget, Text: "injected"}, false},
		{"playwright.waitForURL", map[string]any{"url": "https://original"}, ActionParameters{URL: "https://other", TimeoutMS: &budget}, false},
		{"playwright.upload", map[string]any{"selector": "#upload"}, ActionParameters{Selector: "#upload", TimeoutMS: &budget, URL: "https://injected"}, false},
		{"playwright.fill", map[string]any{"selector": "#field", "value": ""}, ActionParameters{Selector: "#field", TimeoutMS: &budget}, true},
	} {
		if got := ValidActionParameters(sample.method, "tab", []any{sample.original}, sample.plan); got != sample.valid {
			t.Fatal(sample, got)
		}
	}
}

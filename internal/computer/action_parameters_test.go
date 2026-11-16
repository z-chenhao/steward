package computer

import "testing"

func TestComputerActionParametersPreserveIntentAndClosedFields(t *testing.T) {
	count := 2
	amount := 25.5
	for _, sample := range []struct {
		method, surface string
		args            []any
		p               ActionParameters
		valid           bool
	}{
		{"click", "native", []any{nil}, ActionParameters{Button: "right", Count: &count}, true},
		{"click", "native", []any{nil, map[string]any{"count": float64(1)}}, ActionParameters{Count: &count}, false},
		{"scroll", "native", []any{nil, "down"}, ActionParameters{Direction: "down", Amount: &amount}, true},
		{"scroll", "native", []any{nil, "down", float64(1)}, ActionParameters{Direction: "down", Amount: &amount}, false},
		{"typeText", "native", []any{"original"}, ActionParameters{Text: "replacement"}, false},
		{"goto", "tab", []any{"https://original"}, ActionParameters{URL: "https://other"}, false},
		{"createBrowserTab", "", []any{"browser", "https://original"}, ActionParameters{Browser: "other", URL: "https://original"}, false},
		{"drag", "native", []any{nil, nil}, ActionParameters{Text: "injection"}, false},
		{"pressKey", "native", []any{"cmd+a"}, ActionParameters{Text: "cmd+a", KeyInput: &KeyRecipe{Keys: []string{"cmd", "a"}}}, true},
		{"pressKey", "native", []any{"cmd+a"}, ActionParameters{Text: "cmd+a", KeyInput: &KeyRecipe{Keys: []string{"cmd+a"}}}, false},
		{"pressKey", "native", []any{"cmd+a"}, ActionParameters{Text: "cmd+a", KeyInput: &KeyRecipe{Keys: []string{"cmd", "b"}}}, false},
		{"pressKey", "tab", []any{nil, "cmd+a"}, ActionParameters{Text: "cmd+a", KeyInput: &KeyRecipe{Keys: []string{"cmd", "a"}}}, false},
		{"pressKey", "native", []any{"+"}, ActionParameters{Text: "+", KeyInput: &KeyRecipe{Keys: []string{"", ""}}}, true},
	} {
		if got := ValidActionParameters(sample.method, sample.surface, sample.args, sample.p); got != sample.valid {
			t.Fatal(sample, got)
		}
	}
}

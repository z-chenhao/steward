package computeruse

import (
	"strings"
	"testing"

	"github.com/z-chenhao/assistant/internal/plugin/computeruse/contract"
)

func TestPlaywrightSelectorPreservesJavaScriptUTF16Budget(t *testing.T) {
	for _, sample := range []struct {
		selector string
		valid    bool
	}{
		{strings.Repeat("字", 1024), true},
		{strings.Repeat("😀", 1024), true},
		{strings.Repeat("😀", 1025), false},
	} {
		p := normalizePlaywright(contract.ActionFacts{Method: "playwright.count", Surface: "tab"}, []any{map[string]any{"selector": sample.selector}})
		if sample.valid && (p.Selector != sample.selector || p.TimeoutMS == nil || *p.TimeoutMS != 3000 || p.Failure != "") || !sample.valid && p.Failure != "INVALID_ARGUMENT" {
			t.Fatal("selector no longer obeys original browser string budget", len(sample.selector), p.Failure)
		}
	}
}

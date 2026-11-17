package web

import (
	"github.com/z-chenhao/assistant/internal/plugin/modelconnections/contract"
	"sort"
	"strings"
	"unicode/utf16"
)

// Only selection/retention/order policy lives here. Host binds each reference
// back to its exact input snapshot, and never accepts activation authority.
func legacyBrowserOrderV1(f contract.BrowserFacts) []contract.ChoiceRef {
	out := []contract.ChoiceRef{}
	choices := map[string]int{}
	appendChoice := func(source string, index int, c contract.BrowserChoice) {
		ref := contract.ChoiceRef{Source: source, Index: index}
		if at, ok := choices[c.Key]; ok {
			out[at] = ref
		} else {
			choices[c.Key] = len(out)
			out = append(out, ref)
		}
	}
	if f.Reason == "ack" || f.Unavailable {
		for i, c := range f.Previous {
			if strings.HasPrefix(c.Key, "account:") {
				appendChoice("previous", i, c)
			}
		}
	}
	if f.Reason == "refresh" && !f.Unavailable {
		for i := range f.Received {
			out = append(out, contract.ChoiceRef{Source: "received", Index: i})
		}
		return out
	}
	for i, c := range f.Received {
		appendChoice("received", i, c)
	}
	choice := func(ref contract.ChoiceRef) contract.BrowserChoice {
		if ref.Source == "previous" {
			return f.Previous[ref.Index]
		}
		return f.Received[ref.Index]
	}
	sort.SliceStable(out, func(i, j int) bool {
		return legacyBrowserStringGreaterV1(choice(out[i]).SortKey, choice(out[j]).SortKey)
	})
	return out
}

// The browser supplies its own ECMAScript lowercase strings as display facts.
// Comparing UTF-16 units preserves native JS order for supplementary characters.
func legacyBrowserStringGreaterV1(a, b string) bool {
	x, y := utf16.Encode([]rune(a)), utf16.Encode([]rune(b))
	for i := 0; i < len(x) && i < len(y); i++ {
		if x[i] != y[i] {
			return x[i] > y[i]
		}
	}
	return len(x) > len(y)
}

func legacyBrowserMenuV1(f contract.BrowserFacts) contract.Projection {
	order := legacyBrowserOrderV1(f)
	return contract.Projection{Choices: []contract.Choice{}, Order: &order}
}

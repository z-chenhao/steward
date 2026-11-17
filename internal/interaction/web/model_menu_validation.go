package web

import (
	"github.com/z-chenhao/assistant/internal/llm"
	"github.com/z-chenhao/assistant/internal/plugin/modelconnections/contract"
	"strings"
	"unicode/utf8"
)

// Output references bind to this request's facts. Display policy cannot invent
// profiles, account models, or an authoritative current selection.
func validateModelMenu(f contract.Facts, p contract.Projection) error {
	allowed := map[string]bool{}
	current := map[string]bool{"": true}
	for _, c := range f.Candidates {
		if c.Connected {
			allowed[c.Key] = true
			if c.Current {
				current[c.Key] = true
			}
		}
	}
	for _, c := range f.Catalogues {
		if c.Available {
			for _, m := range c.Models {
				allowed[m.Key] = true
			}
		}
	}
	if !current[p.Current] {
		return llm.ErrConflict
	}
	seen := map[string]bool{}
	for _, c := range p.Choices {
		if !allowed[c.Key] || seen[c.Key] || !utf8.ValidString(c.Label) || strings.ContainsRune(c.Label, 0) {
			return llm.ErrConflict
		}
		seen[c.Key] = true
	}
	return nil
}

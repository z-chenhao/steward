package web

import (
	"github.com/z-chenhao/assistant/internal/plugin/modelconnections/contract"
	"sort"
	"strings"
)

// legacyModelMenuV1 serves installations which have never activated the model
// connection service. Remove when those historical embeddings are retired.
func legacyModelMenuV1(f contract.Facts) contract.Projection {
	if f.Browser != nil {
		return legacyBrowserMenuV1(*f.Browser)
	}
	out := contract.Projection{Choices: []contract.Choice{}}
	seen := map[string]bool{}
	accounts := map[string]string{}
	for _, p := range f.Candidates {
		if !p.Connected {
			continue
		}
		if !p.AccountBacked && !p.Verified && !p.Current {
			continue
		}
		label := p.Name
		if p.AccountBacked {
			label = p.Model
			accounts[p.Key] = p.Account
		}
		if p.Current {
			out.Current = p.Key
		}
		if !seen[p.Key] {
			out.Choices = append(out.Choices, contract.Choice{Key: p.Key, Label: label})
			seen[p.Key] = true
		}
	}
	for _, c := range f.Catalogues {
		if !c.Available {
			out.Unavailable = true
			continue
		}
		available := map[string]bool{}
		for _, m := range c.Models {
			available[m.Key] = true
			accounts[m.Key] = c.Account
		}
		kept := out.Choices[:0]
		for _, choice := range out.Choices {
			if accounts[choice.Key] == c.Account && !available[choice.Key] {
				if choice.Key != out.Current {
					continue
				}
				choice.Disabled = true
			}
			kept = append(kept, choice)
		}
		out.Choices = kept
		for _, m := range c.Models {
			if !seen[m.Key] {
				out.Choices = append(out.Choices, contract.Choice{Key: m.Key, Label: m.Name})
				seen[m.Key] = true
			}
		}
	}
	sort.SliceStable(out.Choices, func(i, j int) bool {
		return strings.ToLower(out.Choices[i].Label) > strings.ToLower(out.Choices[j].Label)
	})
	return out
}

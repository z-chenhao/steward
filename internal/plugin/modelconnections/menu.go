package modelconnections

import (
	"context"
	"sort"
	"strings"

	"github.com/z-chenhao/assistant/internal/plugin/modelconnections/contract"
	"github.com/z-chenhao/assistant/pkg/rsiplugin"
)

func Plugin() rsiplugin.Handlers {
	return rsiplugin.Handlers{PluginID: contract.PluginID, Version: "1.1.0", Services: map[string]func(context.Context, rsiplugin.Call) (rsiplugin.Result, error){
		contract.Picker: func(ctx context.Context, call rsiplugin.Call) (rsiplugin.Result, error) {
			var in struct{}
			if err := rsiplugin.Decode(call.Input, &in); err != nil {
				return rsiplugin.Result{}, err
			}
			return rsiplugin.JSONResult(ctx, ProjectPicker())
		},
		contract.ConnectionPolicy: func(ctx context.Context, call rsiplugin.Call) (rsiplugin.Result, error) {
			var facts contract.ConnectionFacts
			if err := rsiplugin.Decode(call.Input, &facts); err != nil {
				return rsiplugin.Result{}, err
			}
			return rsiplugin.JSONResult(ctx, ProjectConnection(facts))
		},
		contract.Settings: func(ctx context.Context, call rsiplugin.Call) (rsiplugin.Result, error) {
			var facts contract.SettingsFacts
			if err := rsiplugin.Decode(call.Input, &facts); err != nil {
				return rsiplugin.Result{}, err
			}
			return rsiplugin.JSONResult(ctx, ProjectSettings(facts))
		},
		contract.Menu: func(ctx context.Context, call rsiplugin.Call) (rsiplugin.Result, error) {
			var facts contract.Facts
			if err := rsiplugin.Decode(call.Input, &facts); err != nil {
				return rsiplugin.Result{}, err
			}
			return rsiplugin.JSONResult(ctx, ProjectMenu(facts))
		},
	}}
}

// ProjectMenu owns the existing menu policy. It receives no credentials, URLs,
// provider operations, configuration versions or activation authority.
func ProjectMenu(f contract.Facts) contract.Projection {
	if f.Browser != nil {
		order := projectBrowserMenu(*f.Browser)
		return contract.Projection{Choices: []contract.Choice{}, Order: &order}
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

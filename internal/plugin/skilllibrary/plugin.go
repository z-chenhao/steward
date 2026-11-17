// Package skilllibrary owns independent method discovery and default entry policy.
package skilllibrary

import (
	"context"
	"errors"
	"github.com/z-chenhao/assistant/internal/plugin/skilllibrary/contract"
	"github.com/z-chenhao/assistant/internal/plugin/tooldiscovery"
	discovery "github.com/z-chenhao/assistant/internal/plugin/tooldiscovery/contract"
	"github.com/z-chenhao/assistant/pkg/rsiplugin"
	"sort"
	"strings"
)

func Plugin() rsiplugin.Handlers {
	return rsiplugin.Handlers{PluginID: contract.PluginID, Version: "1.0.1", Services: map[string]func(context.Context, rsiplugin.Call) (rsiplugin.Result, error){
		contract.SearchContract: func(ctx context.Context, c rsiplugin.Call) (rsiplugin.Result, error) {
			var f contract.SearchFacts
			if err := rsiplugin.Decode(c.Input, &f); err != nil {
				return rsiplugin.Result{}, err
			}
			if f.Limit < 1 || f.Limit > 128 || len(f.Entries) > 129 {
				return rsiplugin.Result{}, errors.New("invalid catalog metadata")
			}
			return rsiplugin.JSONResult(ctx, Search(f))
		},
		contract.ResourceContract: func(ctx context.Context, c rsiplugin.Call) (rsiplugin.Result, error) {
			var f contract.ResourceFacts
			if err := rsiplugin.Decode(c.Input, &f); err != nil {
				return rsiplugin.Result{}, err
			}
			path := f.Path
			if path == "" {
				path = "SKILL.md"
			}
			return rsiplugin.JSONResult(ctx, contract.ResourcePlan{Path: path})
		},
	}}
}
func Search(f contract.SearchFacts) contract.SearchPlan {
	indices := make([]int, len(f.Entries))
	for i := range indices {
		indices[i] = i
	}
	sort.Slice(indices, func(i, j int) bool { return f.Entries[indices[i]].Name < f.Entries[indices[j]].Name })
	if strings.TrimSpace(f.Query) != "" {
		docs := []discovery.Candidate{}
		byName := map[string]int{}
		exact := []int{}
		for _, i := range indices {
			entry := f.Entries[i]
			docs = append(docs, discovery.Candidate{Name: entry.Name, Description: entry.Description})
			byName[entry.Name] = i
			if strings.EqualFold(entry.Name, strings.TrimSpace(f.Query)) {
				exact = append(exact, i)
			}
		}
		indices = exact
		for _, match := range tooldiscovery.Rank(discovery.Facts{Query: f.Query, Candidates: docs}, f.Limit).Entries {
			name := docs[match.Index].Name
			if !strings.EqualFold(name, strings.TrimSpace(f.Query)) {
				indices = append(indices, byName[name])
			}
		}
	}
	return contract.SearchPlan{Indices: indices[:min(f.Limit, len(indices))]}
}

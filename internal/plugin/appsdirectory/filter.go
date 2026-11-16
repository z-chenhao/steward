package appsdirectory

import (
	"github.com/z-chenhao/assistant/internal/plugin/appsdirectory/contract"
	"strings"
)

func filter(items []contract.Entry, query contract.Query) *contract.Filter {
	result := &contract.Filter{Slugs: []string{}, Categories: []contract.Category{}}
	search := strings.ToLower(strings.TrimSpace(query.Search))
	categories := map[string]bool{}
	for _, item := range items {
		if !categories[item.CategoryID] {
			result.Categories = append(result.Categories, contract.Category{ID: item.CategoryID, Name: item.CategoryName})
			categories[item.CategoryID] = true
		}
		if query.Category != "" && item.CategoryID != query.Category || search != "" && !strings.Contains(strings.ToLower(item.Name+" "+item.Slug), search) {
			continue
		}
		result.Slugs = append(result.Slugs, item.Slug)
	}
	return result
}

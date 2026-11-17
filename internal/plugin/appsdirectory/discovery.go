package appsdirectory

import (
	"github.com/z-chenhao/assistant/internal/plugin/appsdirectory/contract"
	"sort"
	"strings"
)

func discovery(items []contract.Entry) *contract.Discovery {
	categories := []struct{ id, name string }{
		{"communication", "Communication and calendars"},
		{"storage", "Documents and knowledge"},
		{"productivity", "Tasks and planning"},
		{"development", "Development tools"},
		{"design", "Design and creation"},
		{"business", "Business tools"},
		{"personal", "Personal life"},
	}
	groups := map[string][]contract.Entry{}
	bySlug := map[string]contract.Entry{}
	for _, item := range items {
		category := item.CategoryID
		switch item.Slug {
		case "googlecalendar", "calendly":
			category = "communication"
		case "notion", "airtable":
			category = "storage"
		}
		groups[category] = append(groups[category], item)
		bySlug[item.Slug] = item
	}
	result := &contract.Discovery{Home: []contract.Group{}, Collections: []contract.Group{}}
	featured := contract.Group{Title: "Featured apps", Slugs: []string{}}
	for _, slug := range []string{"gmail", "googledrive", "googlecalendar", "github", "slack", "notion"} {
		if _, exists := bySlug[slug]; exists {
			featured.Slugs = append(featured.Slugs, slug)
		}
		delete(bySlug, slug)
	}
	result.Home = append(result.Home, featured)
	for _, category := range categories {
		entries := groups[category.id]
		sort.Slice(entries, func(i, j int) bool {
			a, b := strings.ToLower(entries[i].Name), strings.ToLower(entries[j].Name)
			if a == b {
				return entries[i].Slug < entries[j].Slug
			}
			return a < b
		})
		collection := contract.Group{ID: category.id, Title: category.name, Slugs: []string{}}
		preview := contract.Group{ID: category.id, Title: category.name, Slugs: []string{}}
		for _, item := range entries {
			collection.Slugs = append(collection.Slugs, item.Slug)
			if _, exists := bySlug[item.Slug]; exists && len(preview.Slugs) < 6 {
				preview.Slugs = append(preview.Slugs, item.Slug)
			}
		}
		result.Collections = append(result.Collections, collection)
		if len(preview.Slugs) > 0 {
			result.Home = append(result.Home, preview)
		}
	}
	return result
}

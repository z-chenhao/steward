package connectedapps

import "github.com/z-chenhao/assistant/internal/connector/composio"

// Exact original source deduplication and selected-category fallback, only for
// pre-directory/direct/unbundled compositions. Never a managed-error fallback;
// delete when those named consumers no longer require the original policy.
func categoryOptionsLegacyV1(items []composio.Toolkit, selected string) []composio.Category {
	value := categoriesLegacyV1(items)
	if selected != "" {
		found := false
		for _, item := range value {
			if item.ID == selected {
				found = true
			}
		}
		if !found {
			value = append(value, composio.Category{ID: selected, Name: selected})
		}
	}
	return value
}

func categoriesLegacyV1(items []composio.Toolkit) []composio.Category {
	var result []composio.Category
	seen := map[string]bool{}
	for _, item := range items {
		for _, c := range item.Meta.Categories {
			if c.ID != "" && !seen[c.ID] {
				result = append(result, c)
				seen[c.ID] = true
			}
		}
	}
	return result
}

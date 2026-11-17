package connectedapps

import (
	"github.com/z-chenhao/assistant/internal/connector/composio"
	"sort"
	"strings"
)

// Exact original public-catalogue interpretation for explicit pre-directory,
// native/default23 and direct/unbundled compositions. Never managed fallback.
// Delete after these real migration-boundary consumers migrate and no original
// interpretation remains necessary. Account ordering remains separate native business.
func (s DirectorySelection) orderLegacyV1(items []composio.Toolkit) ([]int, error) {
	indices := make([]int, len(items))
	names := make([]string, len(items))
	for i, item := range items {
		label, err := s.Label(item.Name, item.Slug)
		if err != nil {
			return nil, err
		}
		names[i] = label.Name
		indices[i] = i
	}
	sort.Slice(indices, func(i, j int) bool {
		a, b := indices[i], indices[j]
		left, right := strings.ToLower(names[a]), strings.ToLower(names[b])
		if left == right {
			return items[a].Slug < items[b].Slug
		}
		return left < right
	})
	return indices, nil
}

package connectedapps

import (
	"sort"
	"strings"

	"github.com/z-chenhao/assistant/internal/plugin/appsdirectory/contract"
)

// Original pre-directory/default23/direct interpretation. Independent account
// views use this only before their inventory service's durable activation;
// current managed errors never enter it. Retain for named old consumers and
// delete when their graphs/references no longer require original semantics.
func AccountInventoryOrderFacts(facts []contract.InventoryOrderFact, connected bool) []int {
	indices := make([]int, len(facts))
	for i := range facts {
		indices[i] = i
	}
	sort.Slice(indices, func(i, j int) bool {
		a, b := indices[i], indices[j]
		x, y := strings.ToLower(facts[a].Name), strings.ToLower(facts[b].Name)
		if connected && x == y {
			return facts[a].Slug < facts[b].Slug
		}
		return x < y
	})
	return indices
}

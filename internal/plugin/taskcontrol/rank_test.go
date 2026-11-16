package taskcontrol

import (
	"github.com/z-chenhao/assistant/internal/plugin/taskcontrol/contract"
	"reflect"
	"testing"
)

func TestTaskRankingPreservesExactHitsCJKAndDeterministicTies(t *testing.T) {
	f := contract.SearchFacts{Limit: 10, Candidates: []contract.Candidate{{Text: "renewal vendor contract", Status: "active", UpdatedAt: "2026-01-02"}, {Text: "renewal vendor contract", Status: "active", UpdatedAt: "2026-01-02"}, {Text: "coffee beans espresso", Status: "active", UpdatedAt: "2026-01-03"}, {Text: "原始职业规划与求职资料", Status: "paused", UpdatedAt: "2026-01-01"}}}
	for query, want := range map[string][]int{"renewal": {0, 1}, "职业规划": {3}, "unrelatedabsenttoken": {}} {
		f.Query = query
		first := Rank(f)
		if !reflect.DeepEqual(first.Indices, want) {
			t.Fatal(query, first, want)
		}
		for i := 0; i < 50; i++ {
			if got := Rank(f); !reflect.DeepEqual(got, first) {
				t.Fatal("map iteration changed accepted rank", got, first)
			}
		}
	}
}

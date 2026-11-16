package skilllibrary

import (
	"encoding/json"
	"github.com/z-chenhao/assistant/internal/plugin/skilllibrary/contract"
	"github.com/z-chenhao/assistant/internal/skill"
	"reflect"
	"testing"
)

func TestSearchPreservesFrozenNativeDiscovery(t *testing.T) {
	entries := []skill.Summary{{Name: "research", Description: "Gather reliable sources and research evidence", Scope: "agent", Version: "private-a"}, {Name: "coding", Description: "Implement and test Go changes", Scope: "workspace", Version: "private-b"}, {Name: "diagram", Description: "架构图 SVG 时序图", Version: "private-c"}}
	for _, query := range []string{"", "research", "RESEARCH", "reliable evidence", "Go test", "架构图", "missing term"} {
		for _, limit := range []int{1, 2, 8, 128} {
			args, _ := json.Marshal(skill.Query{Query: query, Limit: limit})
			result := skill.ListFrozen(entries, string(args))
			var want []contract.Entry
			if result.Error != nil || json.Unmarshal([]byte(result.Text), &want) != nil {
				t.Fatal(result)
			}
			facts := contract.SearchFacts{Query: query, Limit: limit, Entries: make([]contract.Entry, len(entries))}
			for i, e := range entries {
				facts.Entries[i] = contract.Entry{Name: e.Name, Description: e.Description}
			}
			p := Search(facts)
			got := []contract.Entry{}
			for _, i := range p.Indices {
				got = append(got, facts.Entries[i])
			}
			if !reflect.DeepEqual(got, want) {
				t.Fatal(query, limit, got, want)
			}
		}
	}
}

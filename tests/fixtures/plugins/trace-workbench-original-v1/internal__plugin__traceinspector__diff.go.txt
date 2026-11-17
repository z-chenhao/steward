package traceinspector

import (
	"sort"
	"strconv"

	"github.com/z-chenhao/assistant/internal/plugin/traceinspector/contract"
)

type alignedRecord struct {
	record     contract.Record
	key        string
	valid      bool
	occurrence int
}
type difference struct {
	change     contract.Change
	index      int
	key        string
	valid      bool
	occurrence int
}

// Diff compares stored identities and values, never text similarity. Duplicate
// keys pair by occurrence; missing tool identities remain separate records.
func Diff(f contract.DiffFacts) contract.Diff {
	align := func(records []contract.Record) []alignedRecord {
		counts := map[string]int{}
		out := make([]alignedRecord, 0, len(records))
		for _, r := range records {
			key, valid := "", true
			switch {
			case f.Section == "tools":
				if r.ToolName == nil {
					valid = false
				} else {
					key = "tool:" + *r.ToolName
				}
			case r.SourceRef != "":
				key = "ref:" + r.SourceRef
			case r.CallID != "":
				key = "call:" + r.CallID
			default:
				key = "position:" + strconv.Itoa(r.Index)
			}
			counts[key]++
			out = append(out, alignedRecord{r, key, valid, counts[key]})
		}
		return out
	}
	before, after := align(f.Before), align(f.AfterRecords)
	type identity struct {
		key        string
		occurrence int
	}
	old := map[identity]int{}
	for i, r := range before {
		if r.valid {
			old[identity{r.key, r.occurrence}] = i
		}
	}
	used := map[int]bool{}
	changes := []difference{}
	for _, r := range after {
		i, found := old[identity{r.key, r.occurrence}]
		if !r.valid {
			found = false
		}
		c := contract.Change{After: &r.record.Index, Label: r.record.Label}
		if found {
			used[i] = true
			b := before[i].record
			c.Before = &b.Index
			changed := b.ValueKnown && r.record.ValueKnown && b.Digest != r.record.Digest
			if !changed && b.Index == r.record.Index {
				continue
			}
			c.Kind = "moved"
			if changed {
				c.Kind = "changed"
			}
		} else {
			c.Kind = "added"
		}
		changes = append(changes, difference{c, r.record.Index, r.key, r.valid, r.occurrence})
	}
	for i, r := range before {
		if !used[i] {
			changes = append(changes, difference{contract.Change{Kind: "removed", Before: &r.record.Index, Label: r.record.Label}, r.record.Index, r.key, r.valid, r.occurrence})
		}
	}
	sort.SliceStable(changes, func(i, j int) bool {
		a, b := changes[i], changes[j]
		if a.index != b.index {
			return a.index < b.index
		}
		if a.valid != b.valid {
			return !a.valid
		}
		if a.key != b.key {
			return a.key < b.key
		}
		return a.occurrence < b.occurrence
	})
	out := contract.Diff{Items: []contract.Change{}}
	if f.After >= len(changes) {
		return out
	}
	end := min(len(changes), f.After+40)
	for _, c := range changes[f.After:end] {
		out.Items = append(out.Items, c.change)
	}
	if end < len(changes) {
		out.Next = &end
	}
	return out
}

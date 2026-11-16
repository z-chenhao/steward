package traceinspector

import (
	"testing"

	"github.com/z-chenhao/assistant/internal/plugin/traceinspector/contract"
)

func TestDiffKeepsOccurrenceAlignmentAndExactPagination(t *testing.T) {
	record := func(index int, ref, digest string) contract.Record {
		return contract.Record{Index: index, SourceRef: ref, Label: "user", ValueKnown: true, Digest: digest}
	}
	f := contract.DiffFacts{Section: "messages", Before: []contract.Record{record(0, "kept", "same"), record(1, "removed", "gone"), record(2, "changed", "before"), record(3, "moved", "same")}, AfterRecords: []contract.Record{record(0, "moved", "same"), record(1, "kept", "same"), record(2, "changed", "after"), record(3, "added", "new")}}
	diff := Diff(f)
	counts := map[string]int{}
	for _, item := range diff.Items {
		counts[item.Kind]++
	}
	if len(diff.Items) != 5 || counts["added"] != 1 || counts["removed"] != 1 || counts["changed"] != 1 || counts["moved"] != 2 {
		t.Fatal(diff)
	}
	f.AfterRecords = nil
	for i := range 83 {
		f.AfterRecords = append(f.AfterRecords, record(i, "duplicate", "changed"))
	}
	seen := 0
	for {
		page := Diff(f)
		seen += len(page.Items)
		if page.Next == nil {
			break
		}
		if *page.Next != f.After+40 {
			t.Fatal("diff skipped a page", page)
		}
		f.After = *page.Next
	}
	if seen != 87 {
		t.Fatal("duplicate identities multiplied or dropped records", seen)
	}
}

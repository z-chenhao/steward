package computer

import (
	"strings"
	"testing"
)

func TestComputerSelectionProofAcceptsEquivalentCaretPrograms(t *testing.T) {
	in := SelectionInput{Value: "one target end", Wanted: "target", Role: "AXTextField"}
	for _, recipe := range []SelectionRecipe{
		{Anchor: "start", Move: 4, Extend: 6},
		{Anchor: "end", Move: -4, Extend: -6},
		{Anchor: "start", Move: 10, Extend: -6},
		{Anchor: "end", Move: -10, Extend: 6},
	} {
		if !ValidSelection(in, recipe) {
			t.Fatal("Host imposed one default algorithm", recipe)
		}
	}
	for _, recipe := range []SelectionRecipe{
		{Anchor: "arbitrary-key", Move: 4, Extend: 6},
		{Anchor: "start", Move: 3, Extend: 6},
		{Anchor: "start", Move: 4, Extend: 5},
		{Anchor: "start", Move: -4, Extend: 6},
		{Anchor: "end", Move: 1, Extend: -6},
		{Anchor: "end", Move: -4},
		{Anchor: "start", Move: 256, Extend: 1},
	} {
		if ValidSelection(in, recipe) {
			t.Fatal("incorrect or unbounded native input accepted", recipe)
		}
	}
	for _, invalid := range []SelectionInput{
		{Value: "target target", Wanted: "target", Role: "AXTextField"},
		{Value: "target", Role: "AXTextField"},
		{Value: "target", Wanted: "missing", Role: "AXTextField"},
		{Value: "target", Wanted: "target", Role: "AXButton"},
		{Value: "target\r", Wanted: "target", Role: "AXTextField"},
		{Value: "target中文", Wanted: "target", Role: "AXTextField"},
		{Value: strings.Repeat("x", 256) + "target", Wanted: "target", Role: "AXTextArea"},
	} {
		if ValidSelection(invalid, SelectionRecipe{Anchor: "start", Extend: 6}) {
			t.Fatal("unsupported or ambiguous source became actionable", invalid)
		}
	}
	// The existing action allows a selected prefix in a long ASCII value. The
	// method's bounded view must not impose a new whole-document byte ceiling.
	long := SelectionInput{Value: "target" + strings.Repeat("x", 1<<20), Wanted: "target", Role: "AXTextArea"}
	if !ValidSelection(long, SelectionRecipe{Anchor: "start", Extend: 6}) {
		t.Fatal("new plugin input budget truncated an existing supported action")
	}
	// Keep facade-v1's first occurrence even for overlapping matches, whose
	// non-overlapping strings.Count is one. Equal text at another span is not
	// the originally resolved selection.
	overlap := SelectionInput{Value: "aaa", Wanted: "aa", Role: "AXTextField"}
	if !ValidSelection(overlap, SelectionRecipe{Anchor: "start", Extend: 2}) || ValidSelection(overlap, SelectionRecipe{Anchor: "end", Extend: -2}) {
		t.Fatal("equivalent bytes at a different source span were accepted")
	}
}

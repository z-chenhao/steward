package memory

import "testing"

func TestMemoryCorrectionSourceLimitsLegacyAndOrdinaryInputs(t *testing.T) {
	ref := "memory:one"
	legacy := CorrectionSource("form-id", ref, 0)
	ordinary := "plain-web-message"
	for _, tc := range []struct {
		source   string
		mutation Mutation
		want     bool
	}{
		{legacy, Mutation{Operation: "remember", Supersedes: ref}, true},
		{legacy, Mutation{Operation: "remember", Supersedes: "memory:two"}, false},
		{legacy, Mutation{Operation: "forget", MemoryRef: ref}, false},
		{legacy, Mutation{Operation: "write", MemoryRef: ref, Revision: 1}, false},
		{legacy, Mutation{Operation: "remember"}, false},
		{ordinary, Mutation{Operation: "remember"}, true},
		{ordinary, Mutation{Operation: "forget", MemoryRef: ref}, true},
		{correctionPrefix, Mutation{Operation: "write", MemoryRef: ref, Revision: 1}, false},
	} {
		if got := MatchesCorrection(tc.source, tc.mutation); got != tc.want {
			t.Errorf("%s, %v: got %v, want %v", tc.source, tc.mutation, got, tc.want)
		}
	}
}

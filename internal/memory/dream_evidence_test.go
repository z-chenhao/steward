package memory

import (
	"encoding/json"
	"testing"
	"time"
)

func TestDreamEvidenceSelectsOriginalObservationAndRejectsInventedProof(t *testing.T) {
	in := CurationInput{Version: 2, Mode: "dream", Observations: []Observation{
		{Source: SourceRef{Type: "conversation", Ref: "conversation:one"}, Author: "user", At: time.Unix(1, 0), Text: "My workshop presentation was in Oslo."},
		{Source: SourceRef{Type: "conversation", Ref: "conversation:two"}, Author: "user", At: time.Unix(2, 0), Text: "I visited a school in Bergen."},
	}}
	w := DocumentWrite{Layer: Notes, Title: "Workshop", Content: "Reported workshop in Oslo.", Support: []SupportQuote{{Observation: 0, Quote: "My workshop presentation was in Oslo."}}}
	parse := func(w DocumentWrite, version int) error {
		v := in
		v.Version = version
		raw, _ := json.Marshal(Proposal{Facts: []Candidate{}, Documents: []DocumentWrite{w}, Done: true})
		_, err := ParseProposal(string(raw), v)
		return err
	}
	if err := parse(w, 2); err != nil {
		t.Fatal(err)
	}
	sources, proof, err := DreamEvidence(in, w)
	if err != nil || len(sources) != 1 || sources[0].Ref != "conversation:one" || len(proof) != 1 || proof[0].Author != "user" {
		t.Fatal(sources, proof, err)
	}
	for _, support := range [][]SupportQuote{nil, {{Observation: 2, Quote: "Invented"}}, {{Observation: 1, Quote: "My workshop presentation was in Oslo."}}} {
		bad := w
		bad.Support = support
		if parse(bad, 2) == nil {
			t.Fatal("accepted missing or forged support", support)
		}
	}
	legacy := w
	legacy.Support = nil
	if parse(legacy, 0) != nil {
		t.Fatal("historical frozen proposal became invalid")
	}
}

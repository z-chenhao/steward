package memory

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"github.com/z-chenhao/assistant/internal/tool"
	"strings"
	"testing"
)

func TestLayeredMemoryPreviousToolSnapshotKeepsExactSchema(t *testing.T) {
	d := Definition()
	registry, err := tool.NewRegistry([]tool.Definition{d})
	if err != nil {
		t.Fatal(err)
	}
	old := d.PreviousSpecs[0]
	selected, err := registry.Select([]tool.Spec{old})
	if err != nil {
		t.Fatal("old memory control no longer executable", err)
	}
	got, _ := selected.Resolve(ManageName)
	if got.Spec.Version != 1 || got.Spec.InputSchema != old.InputSchema || tool.Validate(got.Spec.InputSchema, `{"operation":"write","layer":"notes","title":"new","content":"new"}`) {
		t.Fatal("legacy snapshot acquired new operations")
	}
	old.InputSchema = ManageSchema
	if _, err = registry.Select([]tool.Spec{old}); err == nil {
		t.Fatal("unrecognized historical schema accepted")
	}
}

func TestLayeredMemoryDeployedV2SnapshotKeepsExactSchema(t *testing.T) {
	d := Definition()
	registry, err := tool.NewRegistry([]tool.Definition{d})
	if err != nil {
		t.Fatal(err)
	}
	old := d.PreviousSpecs[1]
	selected, err := registry.Select([]tool.Spec{old})
	if err != nil {
		t.Fatal(err)
	}
	got, _ := selected.Resolve(ManageName)
	if got.Spec.Version != 2 || got.Spec.InputSchema != layeredManageSchemaV2 || tool.Validate(got.Spec.InputSchema, `{"operation":"list","limit":0}`) {
		t.Fatal("deployed v2 schema or validation changed")
	}
	old.InputSchema = ManageSchema
	if _, err := registry.Select([]tool.Spec{old}); err == nil {
		t.Fatal("unknown v2 schema accepted")
	}
	for _, raw := range []string{`{"operation":"list","limit":-1}`, `{"operation":"read","memory_ref":"one","limit":4097}`, `{"operation":"read","memory_ref":"one","offset":-1}`, `{"operation":"write","layer":"notes","title":"bad","content":"text","revision":-1}`} {
		if _, ok := Parse(raw); ok {
			t.Fatal("Host numeric bounds bypassed", raw)
		}
	}
}

func TestLayeredMemoryManageUsesExistingToolAndValidatesOperations(t *testing.T) {
	// Advertise the same page bounds that Host validation enforces, so a model
	// need not discover the maximum through failed reads.
	for _, raw := range []string{`{"operation":"read","memory_ref":"one","limit":6000}`, `{"operation":"read","layer":"transcripts","memory_ref":"one","offset":-1}`} {
		if tool.Validate(Definition().Spec.InputSchema, raw) {
			t.Fatal("invalid page accepted by advertised schema", raw)
		}
	}
	for _, raw := range []string{`{"operation":"list","layer":"notes"}`, `{"operation":"read","memory_ref":"one"}`, `{"operation":"write","layer":"notes","title":"Plan","content":"Possibly useful, not verified."}`, `{"operation":"write","layer":"injected","title":"Session memory","content":"Important context","memory_ref":"one","revision":1}`, `{"operation":"retire","layer":"representations","memory_ref":"one","revision":1}`, `{"operation":"dream"}`} {
		if _, ok := Parse(raw); !ok {
			t.Fatal("valid operation rejected", raw)
		}
	}
	for _, raw := range []string{`{"operation":"write","layer":"transcripts","title":"rewrite","content":"bad"}`, `{"operation":"write","layer":"notes","title":"bad","content":"text","memory_ref":"one"}`, `{"operation":"forget","memory_ref":"one","layer":"notes"}`, `{"operation":"read","memory_ref":"one","owner":"other"}`, `{"operation":"write","layer":"notes","title":"bad","content":"password=secret"}`} {
		if _, ok := Parse(raw); ok {
			t.Fatal("invalid operation accepted", raw)
		}
	}
	if Definition().Spec.Name != ManageName {
		t.Fatal("parallel management tool")
	}
}

func TestLayeredMemoryDreamPreservesTentativeNotesAndRejectsUnseenTargets(t *testing.T) {
	in := CurationInput{Mode: "dream", Documents: []Document{{Ref: "memory:one", Layer: Notes, Revision: 2}}}
	p := Proposal{Facts: []Candidate{}, Documents: []DocumentWrite{{Ref: "one", Layer: Notes, Revision: 2, Title: "A hypothesis", Content: "Tentative: validate the timing hypothesis."}}, Done: true}
	raw, _ := json.Marshal(p)
	parsed, err := ParseProposal(string(raw), in)
	if err != nil || parsed.Documents[0].Ref != "memory:one" {
		t.Fatal(parsed, err)
	}
	p.Documents[0].Revision = 1
	raw, _ = json.Marshal(p)
	if _, err = ParseProposal(string(raw), in); err == nil {
		t.Fatal("stale target accepted")
	}
	if got := InjectedText(strings.Repeat("中", 10000)); len(got) > 25000 || !strings.HasSuffix(got, "中") {
		t.Fatal("UTF-8 injection boundary")
	}
	if got := InjectedText(strings.Repeat("line\n", 300)); strings.Count(got, "\n") != 200 {
		t.Fatal("line bound")
	}
}

func TestLayeredMemoryDreamRejectsCyclicAndRetiredDependencies(t *testing.T) {
	in := CurationInput{Mode: "dream", Documents: []Document{{Ref: "memory:a", Layer: Notes, Revision: 1}, {Ref: "memory:b", Layer: Representations, Revision: 1}}}
	for _, kind := range []string{"cycle", "self", "retired"} {
		t.Run(kind, func(t *testing.T) {
			a := DocumentWrite{Ref: "memory:a", Layer: Notes, Revision: 1, Title: "Note", Content: "Evidence"}
			b := DocumentWrite{Ref: "memory:b", Layer: Representations, Revision: 1, Title: "Wiki", Content: "Evidence", Links: []string{"memory:a"}}
			switch kind {
			case "cycle":
				a.Links = []string{"memory:b"}
			case "self":
				a.Links = []string{"memory:a"}
			case "retired":
				a = DocumentWrite{Ref: "memory:a", Layer: Notes, Revision: 1, Retire: true}
			}
			raw, _ := json.Marshal(Proposal{Facts: []Candidate{}, Documents: []DocumentWrite{b, a}, Done: true})
			if _, err := ParseProposal(string(raw), in); err == nil {
				t.Fatal("unsafe dependency accepted before writes")
			}
		})
	}
	order, err := DreamWriteOrder([]DocumentWrite{{Links: []string{"memory:a"}}, {Ref: "memory:a"}})
	if err != nil || len(order) != 2 || order[0] != 1 || order[1] != 0 {
		t.Fatal(order, err)
	}
}

func TestLayeredMemoryDeployedV4ReadSnapshotIsUnchanged(t *testing.T) {
	d := Definition()
	registry, err := tool.NewRegistry([]tool.Definition{d})
	if err != nil {
		t.Fatal(err)
	}
	spec := d.PreviousSpecs[3]
	if fmt.Sprintf("%x", sha256.Sum256([]byte(spec.Description))) != "bff33df5b9f5484df305622211258feb84b122973153090c3172965ab58cd48c" || fmt.Sprintf("%x", sha256.Sum256([]byte(spec.InputSchema))) != "346ff3ac0fb345e99988091e81bf15f7d6446d46f4a9ff59a19ab886f6f80167" {
		t.Fatal("deployed v4 bytes changed")
	}

	if spec.Version != 4 || spec.InputSchema != layeredManageSchemaV4 {
		t.Fatal("lost deployed v4")
	}
	selected, err := registry.Select([]tool.Spec{spec})
	if err != nil {
		t.Fatal(err)
	}
	got, _ := selected.Resolve(ManageName)
	if got.Spec.Description != spec.Description || got.Spec.InputSchema != spec.InputSchema {
		t.Fatal("old read contract rewritten")
	}
	m, ok := Parse(`{"operation":"read","memory_ref":"memory:semantic:opaque-event:2","limit":4096}`)
	if !ok || m.MemoryRef != "memory:semantic:opaque-event:2" {
		t.Fatal("compound reference truncated", m)
	}
}

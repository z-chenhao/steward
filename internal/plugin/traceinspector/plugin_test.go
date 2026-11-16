package traceinspector

import (
	"testing"

	"github.com/z-chenhao/assistant/internal/plugin/traceinspector/contract"
)

func TestProjectionPreservesFragmentsCyclesAndMissingTiming(t *testing.T) {
	f := contract.Facts{Started: 100, Now: 1100, Items: []contract.Observation{{ID: "a", Parent: "b", Started: 100}, {ID: "b", Parent: "a", Started: 200}, {ID: "fragment", Parent: "outside", Started: 300}}, SelectedType: "generation", Timings: []contract.Timing{{Attempt: "absent", Phase: "first_text", At: 500}, {Attempt: "later", Phase: "started", At: 600}, {Attempt: "later", Phase: "headers", At: 700}}}
	l := Project(f)
	if len(l.Nodes) != 3 || l.Nodes[0].ID != "fragment" || !l.Nodes[0].ParentOutside || l.TTFTMS != nil {
		t.Fatal("missing/cyclic evidence fabricated", l)
	}
	for _, n := range l.Nodes {
		if n.X < 0 || n.X+n.Width > 1000 || n.Depth > 16 {
			t.Fatal(n)
		}
	}
	f.Timings = append(f.Timings, contract.Timing{Attempt: "later", Phase: "first_text", At: 500})
	if Project(f).TTFTMS != nil {
		t.Fatal("pre-dispatch text accepted")
	}
}

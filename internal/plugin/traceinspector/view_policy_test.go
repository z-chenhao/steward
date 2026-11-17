package traceinspector

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"github.com/z-chenhao/assistant/internal/observability"
	"github.com/z-chenhao/assistant/internal/plugin/traceinspector/contract"
	"os"
	"reflect"
	"testing"
	"time"
)

// These expectations were executed from the pre-migration native functions,
// with only the wall clock frozen. They are not generated from Project.
func TestViewPolicyMatches384ExecutedNativeLayouts(t *testing.T) {
	const base = "../../../tests/fixtures/plugins/trace-layout-original-v1/"
	raw, err := os.ReadFile(base + "view-policy-v1.json")
	if err != nil {
		t.Fatal(err)
	}
	provenance, err := os.ReadFile(base + "provenance.json")
	if err != nil {
		t.Fatal(err)
	}
	var evidence struct {
		ResultsSHA string `json:"results_sha256"`
	}
	if err = json.Unmarshal(provenance, &evidence); err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256(raw)
	if hex.EncodeToString(digest[:]) != evidence.ResultsSHA {
		t.Fatal("original evidence changed")
	}
	var cases []struct {
		Tree    observability.ObservationTree `json:"tree"`
		Primary []string                      `json:"primary"`
		Rows    []observability.TraceRow      `json:"rows"`
	}
	if err = json.Unmarshal(raw, &cases); err != nil {
		t.Fatal(err)
	}
	if len(cases) != 384 {
		t.Fatal(len(cases))
	}
	for index, c := range cases {
		start, err := time.Parse(time.RFC3339Nano, c.Tree.Trace.CreatedAt)
		if err != nil {
			t.Fatal(err)
		}
		facts := contract.Facts{Started: start.UnixMicro(), Now: 1_000_000_000, SelectedID: c.Tree.Selected.ID}
		if c.Tree.Trace.EndedAt != "" {
			end, err := time.Parse(time.RFC3339Nano, c.Tree.Trace.EndedAt)
			if err != nil {
				t.Fatal(err)
			}
			n := end.UnixMicro()
			facts.Ended = &n
		}
		primary := map[string]bool{}
		visibleParents := map[string]bool{}
		for _, id := range c.Primary {
			primary[id] = true
		}
		for _, o := range c.Tree.Items {
			facts.Items = append(facts.Items, contract.Observation{ID: o.ID, Parent: o.Parent, Started: o.Started, Ended: o.Ended, Type: o.Type, Name: o.Name, Status: o.Status})
			if primary[o.ID] {
				visibleParents[o.Parent] = true
			}
		}
		got := Project(facts)
		if len(got.Nodes) != len(c.Rows) {
			t.Fatalf("case%d rows", index)
		}
		for i, row := range c.Rows {
			n := got.Nodes[i]
			if n.ID != row.ID || n.Depth != row.Depth || n.ParentOutside != row.ParentOutside || n.HasChildren != row.HasChildren || !reflect.DeepEqual(n.Guides, row.Guides) || n.X != row.X || n.Width != row.Width || n.Running != row.Running || n.Primary != primary[row.ID] || n.PrimaryHasChildren != (row.HasChildren && visibleParents[row.ID]) {
				t.Fatalf("case%d row%d: got%+v want%+v", index, i, n, row)
			}
		}
		end := facts.Now
		if facts.Ended != nil {
			end = *facts.Ended
		}
		if got.Timeline == nil || got.Timeline.Start != facts.Started || got.Timeline.End != max(facts.Started+1, end) {
			t.Fatalf("case%d range", index)
		}
	}
}

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

func TestMetricsPolicyMatches384ExecutedNativeCases(t *testing.T) {
	const base = "../../../tests/fixtures/plugins/trace-metrics-original-v1/"
	raw, err := os.ReadFile(base + "metrics-v1.json")
	if err != nil {
		t.Fatal(err)
	}
	provenance, err := os.ReadFile(base + "provenance.json")
	if err != nil {
		t.Fatal(err)
	}
	var p struct {
		SHA string `json:"results_sha256"`
	}
	if err = json.Unmarshal(provenance, &p); err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256(raw)
	if p.SHA != hex.EncodeToString(digest[:]) {
		t.Fatal("original metric evidence changed")
	}
	var cases []struct {
		Tree  observability.ObservationTree `json:"tree"`
		TTFT  *int64                        `json:"ttft_ms"`
		First *int64                        `json:"first_output_at"`
		Rate  *float64                      `json:"output_rate"`
	}
	if err = json.Unmarshal(raw, &cases); err != nil {
		t.Fatal(err)
	}
	if len(cases) != 384 {
		t.Fatal(len(cases))
	}
	unknownFirst, unknownRate := 0, 0
	for index, c := range cases {
		start, err := time.Parse(time.RFC3339Nano, c.Tree.Trace.CreatedAt)
		if err != nil {
			t.Fatal(err)
		}
		end, err := time.Parse(time.RFC3339Nano, c.Tree.Trace.EndedAt)
		if err != nil {
			t.Fatal(err)
		}
		at := end.UnixMicro()
		f := contract.Facts{Started: start.UnixMicro(), Ended: &at, Now: at, SelectedID: c.Tree.Selected.ID, SelectedType: c.Tree.Selected.Type, SelectedName: c.Tree.Selected.Name, SelectedStatus: c.Tree.Selected.Status}
		if c.Tree.Generation != nil {
			f.Usage = &contract.Usage{KnownUsage: c.Tree.Generation.Metrics.KnownUsage, OutputTokens: c.Tree.Generation.Metrics.Output}
		}
		for _, o := range c.Tree.Items {
			f.Items = append(f.Items, contract.Observation{ID: o.ID, Parent: o.Parent, Started: o.Started, Ended: o.Ended, Type: o.Type, Name: o.Name, Status: o.Status})
		}
		for _, m := range c.Tree.ProviderTimings {
			f.Timings = append(f.Timings, contract.Timing{Attempt: m.Attempt, Phase: m.Phase, At: m.At})
		}
		got := Project(f)
		if !reflect.DeepEqual(got.TTFTMS, c.TTFT) || !reflect.DeepEqual(got.FirstOutputAt, c.First) || !reflect.DeepEqual(got.OutputRate, c.Rate) {
			t.Fatalf("case%d gotTTFT%v first%v rate%v; want%v %v %v", index, got.TTFTMS, got.FirstOutputAt, got.OutputRate, c.TTFT, c.First, c.Rate)
		}
		// Selected evidence can be outside the current observation page. Its explicit
		// status/usage facts still describe the selected generation, not an array row.
		f.Items = nil
		paged := Project(f)
		if !reflect.DeepEqual(paged.OutputRate, c.Rate) || !reflect.DeepEqual(paged.FirstOutputAt, c.First) || !reflect.DeepEqual(paged.TTFTMS, c.TTFT) {
			t.Fatalf("case%d paged selected evidence lost", index)
		}
		if c.First == nil {
			unknownFirst++
		}
		if c.Rate == nil {
			unknownRate++
		}
	}
	if unknownFirst != 48 || unknownRate != 336 {
		t.Fatal("missing unknown evidence cases", unknownFirst, unknownRate)
	}
}

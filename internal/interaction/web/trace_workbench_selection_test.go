package web

import (
	"context"
	"errors"
	"github.com/z-chenhao/assistant/internal/plugin/traceinspector"
	"github.com/z-chenhao/assistant/internal/plugin/traceinspector/contract"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/z-chenhao/assistant/internal/observability"
)

type selectedTraceWorkbenchReader struct {
	traceReader
	revision  string
	requested string
	order     []string
	err       error
}

func (r *selectedTraceWorkbenchReader) TraceWorkbenchSelection(_ context.Context, reference string) (observability.TraceWorkbench, error) {
	r.requested = reference
	r.order = append(r.order, "selection")
	source, err := os.ReadFile("../../plugin/traceinspector/view/trace.html")
	if err != nil {
		return observability.TraceWorkbench{}, err
	}
	return observability.TraceWorkbench{Copy: traceFixtureCopy(), ReaderRevision: r.revision, Managed: true, ViewSource: string(source)}, r.err
}
func (r *selectedTraceWorkbenchReader) Traces(ctx context.Context, q observability.TraceQuery) (observability.Traces, error) {
	r.order = append(r.order, "list")
	if q.ReaderRevision != r.revision {
		return observability.Traces{}, errors.New("list lost selected graph")
	}
	return r.traceReader.Traces(ctx, q)
}
func (r *selectedTraceWorkbenchReader) Observations(ctx context.Context, q observability.ObservationQuery) (observability.ObservationTree, error) {
	r.order = append(r.order, "observations")
	if q.ReaderRevision != r.revision {
		return observability.ObservationTree{}, errors.New("detail lost selected graph")
	}
	return r.traceReader.Observations(ctx, q)
}
func TestTraceWorkbenchSelectionPrecedesFactsAndPinsRenderedLinks(t *testing.T) {
	ref := "sha256:" + strings.Repeat("a", 64)
	for _, run := range []string{"", "root-public"} {
		reader := &selectedTraceWorkbenchReader{revision: ref}
		s := languageServer()
		s.Traces = reader
		s.Inspector = &inspectionReader{}
		v := &traceView{Run: run, Tab: "overview"}
		if err := s.loadTrace(context.Background(), v); err != nil {
			t.Fatal(err)
		}
		if len(reader.order) != 2 || reader.order[0] != "selection" || !v.WorkbenchManaged || v.ReaderRevision != ref {
			t.Fatal("page did not freeze before facts", reader.order)
		}
		var body strings.Builder
		view, err := tracePageLayout(httptest.NewRequest("GET", "http://localhost/developer/observability", nil), v)
		if err != nil {
			t.Fatal(err)
		}
		if err := view.ExecuteTemplate(&body, "observability-page", page{Trace: v}); err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(body.String(), `data-trace-reader-revision="`+ref+`"`) || !strings.Contains(body.String(), `data-trace-program-revision="`+ref+`"`) || !strings.Contains(body.String(), "reader_revision=sha256%3A") {
			t.Fatal("page marker or original links lost frozen revision")
		}
		reader.err = errors.New("revoked")
		reader.order = nil
		if err := s.loadTrace(context.Background(), &traceView{Run: run, ReaderRevision: ref}); err == nil || len(reader.order) != 1 || reader.requested != ref {
			t.Fatal("failed selection read facts or ignored pinned graph", reader.order)
		}
	}
}

func TestTraceListAPIKeepsRequestedActivityRevision(t *testing.T) {
	reference := "sha256:" + strings.Repeat("a", 64)
	reader := &selectedTraceWorkbenchReader{revision: reference}
	s := languageServer()
	s.Traces = reader
	request := httptest.NewRequest("GET", "http://127.0.0.1:8080/api/developer/traces?reader_revision="+reference, nil)
	request.Header.Set("Authorization", "Bearer private-token-at-least-24-bytes")
	response := httptest.NewRecorder()
	s.ServeHTTP(response, request)
	if response.Code != 200 || len(reader.order) != 1 || reader.order[0] != "list" {
		t.Fatal("HTTP list lost frozen activity reference", response.Code, response.Body.String(), reader.order)
	}
	if reader.requested != "" {
		t.Fatal("HTTP reference was repaired by selecting another graph")
	}
}

func owningTraceFixtureProjection(tree observability.ObservationTree, revision string) *observability.TracePresentation {
	start, _ := time.Parse(time.RFC3339Nano, tree.Trace.CreatedAt)
	f := contract.Facts{Started: start.UnixMicro(), Now: time.Now().UnixMicro(), SelectedStatus: tree.Selected.Status, SelectedID: tree.Selected.ID, SelectedType: tree.Selected.Type, SelectedName: tree.Selected.Name}
	if tree.Trace.EndedAt != "" {
		end, _ := time.Parse(time.RFC3339Nano, tree.Trace.EndedAt)
		v := end.UnixMicro()
		f.Ended = &v
	}
	if tree.Generation != nil {
		f.Usage = &contract.Usage{KnownUsage: tree.Generation.Metrics.KnownUsage, OutputTokens: tree.Generation.Metrics.Output}
	}
	sources := map[string]observability.Observation{}
	for _, o := range tree.Items {
		sources[o.ID] = o
		f.Items = append(f.Items, contract.Observation{ID: o.ID, Parent: o.Parent, Started: o.Started, Ended: o.Ended, Type: o.Type, Name: o.Name, Status: o.Status})
	}
	for _, mark := range tree.ProviderTimings {
		f.Timings = append(f.Timings, contract.Timing{Attempt: mark.Attempt, Phase: mark.Phase, At: mark.At})
	}
	layout := traceinspector.Project(f)
	p := &observability.TracePresentation{FirstOutputAt: layout.FirstOutputAt, OutputRate: layout.OutputRate, ReaderRevision: revision, Timeline: &observability.TraceTimeline{Start: layout.Timeline.Start, End: layout.Timeline.End}, Tabs: layout.Tabs, TTFTMS: layout.TTFTMS}
	for _, n := range layout.Nodes {
		p.Rows = append(p.Rows, observability.TraceRow{Observation: sources[n.ID], Primary: n.Primary, PrimaryHasChildren: n.PrimaryHasChildren, Running: n.Running, Depth: n.Depth, ParentOutside: n.ParentOutside, HasChildren: n.HasChildren, Guides: n.Guides, X: n.X, Width: n.Width})
	}
	return p
}
func (r *selectedTraceWorkbenchReader) ProjectedObservations(ctx context.Context, q observability.ObservationQuery) (observability.ObservationTree, *observability.TracePresentation, error) {
	tree, err := r.Observations(ctx, q)
	if err != nil {
		return tree, nil, err
	}
	return tree, owningTraceFixtureProjection(tree, r.revision), nil
}

type missingCurrentTraceLayout struct{ *selectedTraceWorkbenchReader }

func (r missingCurrentTraceLayout) ProjectedObservations(ctx context.Context, q observability.ObservationQuery) (observability.ObservationTree, *observability.TracePresentation, error) {
	tree, err := r.Observations(ctx, q)
	p := owningTraceFixtureProjection(tree, r.revision)
	p.Timeline = nil
	return tree, p, err
}
func TestTraceWorkbenchManagedMissingLayoutRejectsNativeFallback(t *testing.T) {
	reader := missingCurrentTraceLayout{&selectedTraceWorkbenchReader{revision: "sha256:" + strings.Repeat("a", 64)}}
	s := languageServer()
	s.Traces = reader
	s.Inspector = &inspectionReader{}
	v := &traceView{Run: "root-public", Tab: "overview"}
	if err := s.loadTrace(context.Background(), v); err == nil || len(v.Rows) != 0 {
		t.Fatal("managed missing policy used native display", err, v.Rows)
	}
}

package web

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/z-chenhao/assistant/internal/observability"
)

type traceToolPageReader struct{ *selectedTraceWorkbenchReader }

func (r *traceToolPageReader) ProjectedObservations(ctx context.Context, q observability.ObservationQuery) (observability.ObservationTree, *observability.TracePresentation, error) {
	tree, err := r.traceReader.Observations(ctx, q)
	if err != nil {
		return tree, nil, err
	}
	node := tree.Items[1]
	node.ID = "tool:receipt"
	node.Type = "tool"
	node.Name = "builtin__web_search"
	tree.Items[1] = node
	tree.Selected = node
	return tree, owningTraceFixtureProjection(tree, r.revision), nil
}

type traceToolDispatchReader struct {
	*traceToolPageReader
	called  int
	query   observability.BodyQuery
	managed bool
	preview *observability.ToolPresentation
	err     error
}

func (r *traceToolDispatchReader) ProjectedToolOutput(_ context.Context, q observability.BodyQuery) (observability.Body, *observability.ToolPresentation, bool, error) {
	r.called++
	r.query = q
	return observability.Body{Available: true, Content: `{"text":"saved raw receipt"}`}, r.preview, r.managed, r.err
}
func TestTraceToolPageUsesFrozenManagedPreviewWithoutNativeFallback(t *testing.T) {
	for _, scenario := range []string{"owned", "nil-owned", "unmanaged", "failure", "missing-projector"} {
		t.Run(scenario, func(t *testing.T) {
			revision := "sha256:" + strings.Repeat("a", 64)
			base := &traceToolPageReader{&selectedTraceWorkbenchReader{revision: revision}}
			reader := &traceToolDispatchReader{traceToolPageReader: base, managed: true, preview: &observability.ToolPresentation{Texts: []string{"independent plugin preview"}}}
			switch scenario {
			case "nil-owned":
				reader.preview = nil
			case "unmanaged":
				reader.managed = false
			case "failure":
				reader.err = errors.New("owning read failed")
			}
			s := languageServer()
			s.Traces = reader
			s.Inspector = &inspectionReader{}
			if scenario == "missing-projector" {
				s.Traces = base
			}
			v := &traceView{Run: "root-public", Node: "tool:receipt", Tab: "overview", Field: "output", Offset: 7}
			err := s.loadTrace(context.Background(), v)
			if scenario == "unmanaged" || scenario == "failure" || scenario == "missing-projector" {
				if err == nil {
					t.Fatal("managed preview silently used native path")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if reader.called != 1 || reader.query.Selected != "tool:receipt" || reader.query.ReaderRevision != revision || reader.query.Offset != 7 || reader.query.Field != "output" {
				t.Fatal("lost frozen selection or pagination", reader.called, reader.query)
			}
			if v.ToolOutput != reader.preview {
				t.Fatal("owning preview replaced", v.ToolOutput)
			}
			if v.Output.Content != `{"text":"saved raw receipt"}` {
				t.Fatal("raw receipt changed", v.Output.Content)
			}
		})
	}
}

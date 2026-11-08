package web

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/z-chenhao/assistant/internal/llm"
	"github.com/z-chenhao/assistant/internal/observability"
)

type TraceReader interface {
	Traces(context.Context, observability.TraceQuery) (observability.Traces, error)
	Observations(context.Context, observability.ObservationQuery) (observability.ObservationTree, error)
	ObservationBody(context.Context, observability.BodyQuery) (observability.Body, error)
	ChatPreview(context.Context, observability.ObservationQuery) ([]observability.ChatPreview, error)
}

type traceView struct {
	MetadataOpen                       bool
	Query                              observability.TraceQuery
	Run, Task, Node, After, Tab, Field string
	Offset                             int
	DiffAfter                          int
	List                               observability.Traces
	Tree                               *observability.ObservationTree
	Rows                               []traceRow
	Input, Output, Wire                observability.Body
	Messages                           []observability.ChatPreview
	ModelOutput                        *llm.GenerationResult
	ToolOutput                         *toolOutputView
	Diff                               *observability.ContextDiff
	Relations                          *observability.Relationships
	FullContext                        *contextView
	Compare                            string
	Tabs                               []string
	TTFTMS                             *int64
}
type traceRow struct {
	observability.Observation
	Depth         int
	ParentOutside bool
	HasChildren   bool
	Guides        []bool
	X, Width      float64
}

func (s *Server) loadTrace(ctx context.Context, v *traceView) error {
	if s.Traces == nil {
		return errors.New("trace inspection unavailable")
	}
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	var err error
	if v.Run == "" && v.Task == "" {
		v.List, err = s.Traces.Traces(ctx, v.Query)
		return err
	}
	if v.Task != "" || v.Run != "" {
		if s.Inspector == nil {
			return errors.New("relationships unavailable")
		}
		relations, e := s.Inspector.Relationships(ctx, observability.RelationshipQuery{RunID: v.Run, TaskRef: v.Task})
		if e != nil {
			return e
		}
		v.Relations = &relations
	}
	if v.Run == "" {
		return nil
	}
	if v.FullContext != nil {
		if s.Contexts == nil {
			return errors.New("context inspection unavailable")
		}
		if v.FullContext.Query.Step < 0 {
			rounds, e := s.Contexts.Rounds(ctx, observability.RoundQuery{RunID: v.FullContext.Query.RunID, After: v.FullContext.RoundAfter})
			if e != nil {
				return e
			}
			if len(rounds.Items) > 0 {
				v.FullContext.Query.Step = rounds.Items[0].Step
			}
		}
		if err = s.loadContext(ctx, v.FullContext, ""); err != nil {
			return err
		}
		if v.FullContext.Frozen != nil {
			v.Node = "generation:" + v.FullContext.Frozen.Round.ID
		}
	}
	query := observability.ObservationQuery{RunID: v.Run, Selected: v.Node, Cursor: v.After}
	tree, err := s.Traces.Observations(ctx, query)
	if err != nil {
		return err
	}
	v.Tree = &tree
	var started, firstText int64
	starts := map[string]int64{}
	for _, mark := range tree.ProviderTimings {
		if mark.Phase == "started" {
			starts[mark.Attempt] = mark.At
			if started == 0 || mark.At < started {
				started = mark.At
			}
		}
	}
	for _, mark := range tree.ProviderTimings {
		if at, ok := starts[mark.Attempt]; ok && mark.Phase == "first_text" && mark.At >= at && (firstText == 0 || mark.At < firstText) {
			firstText = mark.At
		}
	}
	if started > 0 && firstText >= started {
		ms := (firstText - started) / 1000
		v.TTFTMS = &ms
	}
	v.Node = tree.Selected.ID
	query.Selected = v.Node
	v.Rows = traceRows(tree)
	v.Tabs = observationTabs(tree.Selected)
	if tree.Selected.Type == "generation" && s.Contexts != nil && tree.Selected.Step != nil {
		rounds, e := s.Contexts.Rounds(ctx, observability.RoundQuery{RunID: tree.Selected.RunID, After: -1})
		if e != nil {
			return e
		}
		if len(rounds.Items) == 0 || rounds.Items[0].Step >= *tree.Selected.Step {
			v.Tabs = []string{"overview", "context", "wire"}
		}
	}
	if v.Tab == "metadata" {
		v.MetadataOpen = true
		v.Tab = "overview"
	}
	valid := false
	for _, tab := range v.Tabs {
		if tab == v.Tab {
			valid = true
		}
	}
	if !valid && v.Tab != "metadata" {
		v.Tab = "overview"
	}
	if v.Tab == "context" && tree.Selected.Type == "generation" {
		if v.FullContext == nil {
			v.FullContext = &contextView{Query: observability.ContextQuery{RunID: tree.Selected.RunID, Step: *tree.Selected.Step, Section: "messages"}, RoundAfter: -1}
			return s.loadContext(ctx, v.FullContext, "")
		}
		return nil
	}
	read := func(field string) (observability.Body, error) {
		offset := 0
		if field == v.Field {
			offset = v.Offset
		}
		return s.Traces.ObservationBody(ctx, observability.BodyQuery{ObservationQuery: query, Field: field, Offset: offset})
	}
	if v.Tab == "wire" {
		v.Wire, err = read("wire")
		// Capture is bounded and immutable after dispatch. Assemble small saved
		// requests so a byte boundary does not turn valid JSON into unreadable
		// fragments; the original paging route remains for larger captures.
		if err == nil && v.Offset == 0 && v.Wire.Length <= 2<<20 && v.Wire.Next != nil {
			first := v.Wire
			var body strings.Builder
			body.WriteString(first.Content)
			next := first.Next
			for next != nil {
				part, e := s.Traces.ObservationBody(ctx, observability.BodyQuery{ObservationQuery: query, Field: "wire", Offset: *next})
				if e != nil || !part.Available || part.Length != first.Length || (part.Next != nil && *part.Next <= *next) {
					break
				}
				body.WriteString(part.Content)
				next = part.Next
			}
			if next == nil && body.Len() == first.Length && json.Valid([]byte(body.String())) {
				v.Wire.Content, v.Wire.Next = body.String(), nil
			}
		}
		return err
	}
	if v.Tab == "metadata" {
		return nil
	}
	if tree.Selected.Type == "generation" {
		if v.Tab == "diff" {
			if s.Contexts == nil {
				return errors.New("context unavailable")
			}
			diff, e := s.Contexts.ContextDiff(ctx, observability.DiffQuery{RunID: tree.Selected.RunID, Step: *tree.Selected.Step, Section: v.Compare, After: v.DiffAfter})
			v.Diff = &diff
			return e
		}

		v.Messages, err = s.Traces.ChatPreview(ctx, query)
		if err != nil {
			return err
		}
		v.Output, err = read("output")
		if err == nil && v.Output.Available && v.Offset == 0 && v.Output.Next == nil {
			var out llm.GenerationResult
			if json.Unmarshal([]byte(v.Output.Content), &out) == nil {
				v.ModelOutput = &out
			}
		}
		return err
	}
	v.Input, err = read("input")
	if err != nil {
		return err
	}
	v.Output, err = read("output")
	if err == nil && tree.Selected.Type == "tool" {
		v.ToolOutput = previewToolOutput(tree.Selected.Name, v.Output, v.Offset)
	}
	return err
}

func (s *Server) traceAPI(w http.ResponseWriter, r *http.Request, kind string) {
	v, err := traceQuery(r)
	if err == nil && s.Traces == nil {
		err = errors.New("trace unavailable")
	}
	if err != nil {
		inspectionFailure(w, err)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
	defer cancel()
	q := observability.ObservationQuery{RunID: v.Run, Selected: v.Node, Cursor: v.After}
	if kind != "traces" {
		err = q.Validate()
	}
	if err == nil && kind == "body" {
		err = (observability.BodyQuery{ObservationQuery: q, Field: v.Field, Offset: v.Offset}).Validate()
	}
	if err != nil {
		inspectionFailure(w, err)
		return
	}
	var result any
	switch kind {
	case "traces":
		result, err = s.Traces.Traces(ctx, v.Query)
	case "observations":
		result, err = s.Traces.Observations(ctx, q)
	case "body":
		result, err = s.Traces.ObservationBody(ctx, observability.BodyQuery{ObservationQuery: q, Field: v.Field, Offset: v.Offset})
	}
	if err != nil {
		inspectionFailure(w, err)
		return
	}
	jsonResult(w, result)
}

// Trace identity is the task title for workers and the incoming message for Main.
// The full input remains available in the detail; this is a read-only label.
func traceLabel(trace observability.Trace) string {
	if trace.Role == "main" && strings.TrimSpace(trace.Input) != "" {
		return trace.Input
	}
	return trace.Title
}

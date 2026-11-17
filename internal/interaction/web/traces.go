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
	Copy                               []observability.TraceTranslation
	ViewSource                         string
	WorkbenchManaged                   bool
	MetadataOpen                       bool
	ReaderRevision                     string
	ShowInternal                       bool
	HiddenInternal                     int
	InputComplete                      bool
	OutputRate                         *float64
	TimelineStart, TimelineEnd         int64
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
	EvidenceTabs                       []string
	TTFTMS                             *int64
}
type traceRow = observability.TraceRow

func (s *Server) loadTrace(ctx context.Context, v *traceView) error {
	if s.Traces == nil {
		return errors.New("trace inspection unavailable")
	}
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	var err error
	if selector, ok := s.Traces.(interface {
		TraceWorkbenchSelection(context.Context, string) (observability.TraceWorkbench, error)
	}); ok {
		selection, e := selector.TraceWorkbenchSelection(ctx, v.ReaderRevision)
		if e != nil {
			return e
		}
		v.ReaderRevision = selection.ReaderRevision
		v.Query.ReaderRevision = selection.ReaderRevision
		v.WorkbenchManaged = selection.Managed
		v.ViewSource = selection.ViewSource
		v.Copy = selection.Copy
	}
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
		v.FullContext.ReaderRevision = v.ReaderRevision
		if err = s.loadContext(ctx, v.FullContext, ""); err != nil {
			return err
		}
		if v.ReaderRevision == "" {
			v.ReaderRevision = v.FullContext.ReaderRevision
		}
		if v.FullContext.Frozen != nil {
			v.Node = "generation:" + v.FullContext.Frozen.Round.ID
		}
	}
	query := observability.ObservationQuery{RunID: v.Run, Selected: v.Node, Cursor: v.After, ReaderRevision: v.ReaderRevision}
	var tree observability.ObservationTree
	var presentation *observability.TracePresentation
	if projector, ok := s.Traces.(interface {
		ProjectedObservations(context.Context, observability.ObservationQuery) (observability.ObservationTree, *observability.TracePresentation, error)
	}); ok {
		tree, presentation, err = projector.ProjectedObservations(ctx, query)
	} else {
		tree, err = s.Traces.Observations(ctx, query)
	}
	if err != nil {
		return err
	}
	if v.WorkbenchManaged && (presentation == nil || presentation.Timeline == nil) {
		return errors.New("managed trace layout unavailable")
	}
	v.Tree = &tree
	var firstOutput *int64
	if presentation != nil && presentation.Timeline != nil {
		v.TTFTMS, v.OutputRate, firstOutput = presentation.TTFTMS, presentation.OutputRate, presentation.FirstOutputAt
	} else {
		v.TTFTMS, firstOutput = legacyTraceTimingV1(tree, presentation)
		v.OutputRate = legacyTraceGenerationOutputRateV1(tree)
	}
	v.Node = tree.Selected.ID
	query.Selected = v.Node
	if presentation != nil {
		v.EvidenceTabs = presentation.Tabs
		v.ReaderRevision = presentation.ReaderRevision
	}
	if presentation != nil && presentation.Timeline != nil {
		v.TimelineStart, v.TimelineEnd = presentation.Timeline.Start, presentation.Timeline.End
		v.Rows = nil
		v.HiddenInternal = 0
		for _, row := range presentation.Rows {
			if !v.ShowInternal && !row.Primary {
				v.HiddenInternal++
				continue
			}
			if !v.ShowInternal {
				row.HasChildren = row.PrimaryHasChildren
			}
			v.Rows = append(v.Rows, row)
		}
	} else {
		v.Rows, v.HiddenInternal, v.TimelineStart, v.TimelineEnd = legacyTracePresentationRowsV1(tree, presentation, v.ShowInternal)
	}

	query.ReaderRevision = v.ReaderRevision
	if v.FullContext != nil {
		v.FullContext.ReaderRevision = v.ReaderRevision
	}
	for i := range v.Rows {
		if v.Rows[i].ID == tree.Selected.ID && firstOutput != nil {
			v.Rows[i].FirstOutput = firstOutput
		}
	}
	if !v.WorkbenchManaged {
		v.Tabs = legacyTracePrimaryTabsV1(tree.Selected)
	}
	switch v.Tab {
	case "context", "wire", "diff":
		if !traceHasView(v, v.Tab) || (tree.Selected.Type != "generation" && !(v.Tab == "wire" && tree.Selected.Name == "Provider HTTP")) {
			v.Tab = "overview"
		}
	case "overview", "metadata":
	default:
		v.Tab = "overview"
	}
	if v.Tab == "context" && tree.Selected.Type == "generation" {
		if v.FullContext == nil {
			v.FullContext = &contextView{Query: observability.ContextQuery{RunID: tree.Selected.RunID, Step: *tree.Selected.Step, Section: "messages"}, RoundAfter: -1}
			v.FullContext.ReaderRevision = v.ReaderRevision
			return s.loadContext(ctx, v.FullContext, "")
		}
		return nil
	}
	read := func(field string) (observability.Body, error) {
		offset := 0
		if field == v.Field {
			offset = v.Offset
		}
		body, e := s.Traces.ObservationBody(ctx, observability.BodyQuery{ObservationQuery: query, Field: field, Offset: offset})
		if e != nil || offset != 0 || body.Next == nil || body.Length > 2<<20 || len(body.Content) > body.Length || *body.Next != len(body.Content) {
			return body, e
		}
		// Assemble immutable saved JSON only within a fixed budget. Incomplete or
		// changing evidence retains the original lossless byte-paging route.
		var all strings.Builder
		all.WriteString(body.Content)
		next := body.Next
		for next != nil {
			part, e := s.Traces.ObservationBody(ctx, observability.BodyQuery{ObservationQuery: query, Field: field, Offset: *next})
			if e != nil || !part.Available || part.Length != body.Length || len(part.Content) == 0 || len(part.Content) > body.Length-all.Len() || (part.Next != nil && *part.Next != *next+len(part.Content)) {
				return body, nil
			}
			all.WriteString(part.Content)
			next = part.Next
		}
		if all.Len() == body.Length && json.Valid([]byte(all.String())) {
			body.Content, body.Next = all.String(), nil
		}
		return body, nil
	}
	if v.Tab == "wire" {
		v.Wire, err = read("wire")
		return err
	}
	if v.Tab == "metadata" {
		return nil
	}
	if tree.Selected.Type == "tool" {
		v.Input, err = read("input")
		if err != nil {
			return err
		}
		if projector, ok := s.Traces.(interface {
			ProjectedToolOutput(context.Context, observability.BodyQuery) (observability.Body, *observability.ToolPresentation, bool, error)
		}); ok {
			offset := 0
			if v.Field == "output" {
				offset = v.Offset
			}
			var managed bool
			v.Output, v.ToolOutput, managed, err = projector.ProjectedToolOutput(ctx, observability.BodyQuery{ObservationQuery: query, Field: "output", Offset: offset})
			if err != nil || managed {
				return err
			}
		}
		if v.WorkbenchManaged {
			return observability.ErrInvalid
		}
		v.Output, err = read("output")
		if err == nil {
			v.ToolOutput = legacyTraceToolOutputV1(tree.Selected.Name, v.Output, v.Offset)
		}
		return err
	}
	if tree.Selected.Type == "generation" {
		if v.Tab == "diff" {
			if s.Contexts == nil {
				return errors.New("context unavailable")
			}
			diff, e := s.Contexts.ContextDiff(ctx, observability.DiffQuery{RunID: tree.Selected.RunID, Step: *tree.Selected.Step, Section: v.Compare, After: v.DiffAfter, ReaderRevision: v.ReaderRevision})
			v.Diff = &diff
			return e
		}

		v.Input, err = read("input")
		if err != nil {
			return err
		}
		if v.Input.Available && v.Input.Next == nil && !(v.Field == "input" && v.Offset > 0) {
			v.Messages, v.InputComplete = completeMessages(v.Input.Content)
		}
		if !v.InputComplete {
			v.Messages, err = s.Traces.ChatPreview(ctx, query)
			if err != nil {
				return err
			}
		}
		v.Output, err = read("output")
		if err == nil && v.Output.Available && !(v.Field == "output" && v.Offset > 0) && v.Output.Next == nil {
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
	q := observability.ObservationQuery{RunID: v.Run, Selected: v.Node, Cursor: v.After, ReaderRevision: v.ReaderRevision}
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

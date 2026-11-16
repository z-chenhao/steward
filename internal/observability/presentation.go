package observability

import "github.com/z-chenhao/assistant/internal/tool"

// TracePresentation is a private view projection, separate from the source
// records returned by inspection APIs. It never changes ledger observations.
type TraceTimeline struct{ Start, End int64 }

type TracePresentation struct {
	FirstOutputAt  *int64
	OutputRate     *float64
	Timeline       *TraceTimeline
	ReaderRevision string
	Rows           []TraceRow
	Tabs           []string
	TTFTMS         *int64
}
type TraceRow struct {
	Primary, PrimaryHasChildren bool
	Observation
	Depth         int
	ParentOutside bool
	HasChildren   bool
	Guides        []bool
	X, Width      float64
	Running       bool
	FirstOutput   *int64
}

type ToolPresentation struct {
	Texts     []string
	Artifacts []string
	Images    []string
	Error     *tool.Error
}

// TraceProgram is a read-only resource, separate from recorded trace facts.
type TraceTranslation struct {
	English string `json:"english"`
	Chinese string `json:"chinese"`
}

type TraceProgram struct {
	Copy           []TraceTranslation
	ViewSource     string
	Stylesheet     string
	ReaderRevision string
	Source         string
}

// TraceWorkbench identifies one frozen page interpreter, not source records.
type TraceWorkbench struct {
	Copy           []TraceTranslation
	ViewSource     string
	ReaderRevision string
	Managed        bool
}

package ui

import "encoding/json"

// WidgetSource is an internal authorized read snapshot, never an HTTP or SDK
// contract. The immutable Run binding identifies historical renderer versions.
type WidgetSource struct {
	Document      Widget
	GraphRevision string
	Managed       bool
}

// WidgetView carries only bytes selected and checked by the Host adapter.
// Managed views never substitute the native embedded runtime on failure.
type WidgetView struct {
	Document Widget
	Runtime  []byte
	Styles   []byte
	Managed  bool
}

// PlanView contains accepted presentation bytes selected by the Host. It has no
// script, action, credential or user-input channel.
type PlanView struct {
	HTML            string
	CSS             string
	Styles          []byte
	CompactMaxWidth int
}

// CardsView contains accepted package presentation and its isolated runtime.
// The Host controller grants only component-bound presentation interactions.
type CardsContext struct {
	Title       string `json:"title,omitempty"`
	VisibleItem string `json:"visible_item"`
}

type CardsView struct {
	Contexts        []CardsContext
	HTML            string
	CSS             string
	Runtime         string // Historical reviewed DOM source; never executed.
	Worker          string
	Styles          []byte
	CompactMaxWidth int
}

// ChartView binds an accepted chart policy to original component facts. Neither
// candidate-selected identities nor model prose are part of the bridge.
type ChartView struct {
	Title       string
	Description string
	Unit        string
	Worker      string
	Name        string
	Props       json.RawMessage
}

// ChartExport cells are original JSON string/number values, never candidate data.
type ChartExport struct {
	Title   string              `json:"title"`
	Columns []string            `json:"columns"`
	Rows    [][]json.RawMessage `json:"rows"`
}

// FormView is a frozen display for the existing native controls. Submission
// identity, authorization and the ordinary ingress remain in the Host.
type FormView struct {
	Props    FormProps         `json:"props"`
	Defaults map[string]string `json:"defaults"`
}

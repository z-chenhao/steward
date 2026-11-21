// Package contract defines the private, experimental Memory Inspector presentation seam.
// It contains navigation/display inputs, never the internal inspection page or store.
package contract

import "time"

const PluginID = "steward.memory-inspector"
const Presentation = "steward.memory-inspector-presentation/1"

// Query contains only the existing read-only route's display/navigation values.
// Host still validates and authorizes the resulting request independently.
type Query struct {
	Layer         string    `json:"layer,omitempty"`
	SourcesOffset int       `json:"sources_after,omitempty"`
	Since         time.Time `json:"since,omitempty"`
	Until         time.Time `json:"until,omitempty"`
	Ref           string    `json:"ref,omitempty"`
	Search        string    `json:"search,omitempty"`
	Tier          string    `json:"tier,omitempty"`
	Origin        string    `json:"origin,omitempty"`
	Status        string    `json:"status,omitempty"`
	View          string    `json:"view,omitempty"`
	Cursor        string    `json:"cursor,omitempty"`
	Limit         int       `json:"limit,omitempty"`
	Source        string    `json:"source,omitempty"`
	Offset        int64     `json:"offset,omitempty"`
}
type Navigation struct {
	Query Query  `json:"query"`
	Key   string `json:"key"`
	Value string `json:"value"`
}
type Request struct {
	Navigation []Navigation `json:"navigation"`
}
type SourceLabels struct {
	Conversation string `json:"conversation"`
	Task         string `json:"task"`
	Tool         string `json:"tool"`
	Control      string `json:"control"`
	Other        string `json:"other"`
}
type OriginLabels struct {
	Written   string `json:"written"`
	Dream     string `json:"dream"`
	Original  string `json:"original"`
	Automatic string `json:"automatic"`
	Explicit  string `json:"explicit"`
	Other     string `json:"other"`
}
type LayerLabels struct {
	Transcripts     string `json:"transcripts"`
	Representations string `json:"representations"`
	Injected        string `json:"injected"`
	Notes           string `json:"notes"`
}
type Labels struct {
	Source        SourceLabels `json:"source"`
	Origin        OriginLabels `json:"origin"`
	Layer         LayerLabels  `json:"layer"`
	DefaultStatus string       `json:"default_status"`
}

const ViewResource = "memory-inspector-page"
const ViewFile = "views/memory.html"

type View struct {
	Resource string `json:"resource"`
	Digest   string `json:"digest"`
	Source   string `json:"source"`
}

const StylesheetResource = "memory-inspector-styles"
const StylesheetFile = "views/memory.css"

type Stylesheet struct {
	Resource string `json:"resource"`
	Digest   string `json:"digest"`
	Source   string `json:"source"`
}

const LocalizationResource = "memory-inspector-copy"
const LocalizationFile = "views/copy.json"

type Localization struct {
	Resource string `json:"resource"`
	Digest   string `json:"digest"`
	Source   string `json:"source"`
}
type Translation struct {
	English string `json:"english"`
	Chinese string `json:"chinese"`
}

const ProgramResource = "memory-inspector-program"
const ProgramFile = "views/memory-worker.js"

type BrowserProgram struct {
	Resource string `json:"resource"`
	Digest   string `json:"digest"`
	Source   string `json:"source"`
}

type Result struct {
	CorrectionDraft bool           `json:"correction_draft,omitempty"`
	Program         BrowserProgram `json:"program"`
	Localization    Localization   `json:"localization"`
	Stylesheet      Stylesheet     `json:"stylesheet"`
	View            View           `json:"view"`
	URLs            []string       `json:"urls"`
	Labels          Labels         `json:"labels"`
}

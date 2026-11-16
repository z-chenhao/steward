// Package contract is a repository-private experimental UI document seam.
// DocumentJSON is the existing user presentation protocol, not Host row types.
package contract

import "encoding/json"

const PluginID = "steward.intelligent-ui"
const Normalize = "steward.ui-document/1"
const DocumentInput = "ui-document"

type Result struct {
	ActionViews   []ActionView  `json:"action_views,omitempty"`
	MeetingViews  []MeetingView `json:"meeting_views,omitempty"`
	ComponentText []string      `json:"component_text,omitempty"`
	DocumentJSON  string        `json:"document_json"`
	Readable      string        `json:"readable"`
	ErrorCode     string        `json:"error_code"`
}

const DefinitionContract = "steward.ui-definition/1"
const RuntimeResource = "ui-sandbox-runtime"
const StylesResource = "ui-sandbox-styles"
const RuntimeFile = "view/widget-runtime.js"
const StylesFile = "view/widget.css"

type ViewResource struct {
	Name   string `json:"name"`
	Digest string `json:"digest"`
}

type Definition struct {
	InputSchema      string       `json:"input_schema"`
	ModelInputSchema string       `json:"model_input_schema"`
	Description      string       `json:"description"`
	Runtime          ViewResource `json:"runtime"`
	Styles           ViewResource `json:"styles"`
}

// Preview is a distinct pure policy for incomplete, non-executable prefixes.
const PreviewContract = "steward.ui-preview/1"

type PreviewResult struct {
	Valid       bool   `json:"valid"`
	PreviewJSON string `json:"preview_json"`
}

// Form facts contain presentation data and answer text, never Host identities.
const FormContract = "steward.ui-form/1"
const FormInput = "ui-form"

type FormFacts struct {
	Name   string            `json:"name"`
	Props  json.RawMessage   `json:"props"`
	Values map[string]string `json:"values,omitempty"`
}
type FormResult struct {
	Parts     []string `json:"parts"`
	Response  string   `json:"response"`
	ErrorCode string   `json:"error_code"`
}

// PlanViews is a repository-private pure view seam, frozen with the UI package.
const PlanViews = "steward.ui-plan-view/1"
const PlanInput = "ui-plan"

type PlanFacts struct {
	Plans []PlanFact `json:"plans"`
}
type PlanFact struct {
	Component int             `json:"component"`
	Props     json.RawMessage `json:"props"`
}

const PlanViewsInputSchema = `{"type":"object","properties":{},"required":[],"additionalProperties":false}`
const PlanViewsOutputSchema = `{"type":"object","properties":{"views":{"type":"array","maxItems":16,"items":{"type":"object","properties":{"component":{"type":"integer","minimum":0,"maximum":15},"html":{"type":"string","maxLength":524288},"css":{"type":"string","maxLength":16384},"compact_max_width":{"type":"integer","minimum":1,"maximum":4096}},"required":["component","html","css","compact_max_width"],"additionalProperties":false}},"error_code":{"type":"string","enum":["","INVALID_ARGUMENT"]}},"required":["views","error_code"],"additionalProperties":false}`

type PlanView struct {
	Component       int    `json:"component"`
	HTML            string `json:"html"`
	CSS             string `json:"css"`
	CompactMaxWidth int    `json:"compact_max_width"`
}
type PlanViewsResult struct {
	Views     []PlanView `json:"views"`
	ErrorCode string     `json:"error_code"`
}

// Package contract is a private schedule-policy seam, not Automation state.
package contract

import "time"

const PluginID = "steward.schedule"
const Next = "steward.schedule-next/1"

type NextFacts struct {
	Type     string    `json:"trigger_type"`
	Spec     string    `json:"trigger_spec"`
	Timezone string    `json:"timezone"`
	After    time.Time `json:"after"`
}
type NextResult struct {
	Next      string `json:"next"`
	ErrorCode string `json:"error_code"`
}

const InputSchema = `{"type":"object","properties":{"trigger_type":{"type":"string","enum":["at","cron"]},"trigger_spec":{"type":"string","maxLength":256},"timezone":{"type":"string","maxLength":128},"after":{"type":"string","minLength":20,"maxLength":40}},"required":["trigger_type","trigger_spec","timezone","after"],"additionalProperties":false}`
const OutputSchema = `{"type":"object","properties":{"next":{"type":"string","maxLength":40},"error_code":{"type":"string","enum":["","INVALID_ARGUMENT"]}},"required":["next","error_code"],"additionalProperties":false}`

// LocalTime is the existing editor's exact local-minute interpretation, not
// authority to update a plan or authenticate an original saved instant.
const LocalTime = "steward.schedule-local-time/1"

type LocalTimeFacts struct {
	OriginalSpec string `json:"original_spec,omitempty"`
	Date         string `json:"date"`
	Clock        string `json:"clock"`
	Timezone     string `json:"timezone"`
}
type LocalTimeResult struct {
	Spec      string `json:"spec"`
	ErrorCode string `json:"error_code"`
}

const LocalTimeInputSchema = `{"type":"object","properties":{"date":{"type":"string","maxLength":10},"clock":{"type":"string","maxLength":5},"timezone":{"type":"string","maxLength":128},"original_spec":{"type":"string","maxLength":256}},"required":["date","clock","timezone"],"additionalProperties":false}`
const LocalTimeOutputSchema = `{"type":"object","properties":{"spec":{"type":"string","maxLength":256},"error_code":{"type":"string","enum":["","INVALID_ARGUMENT"]}},"required":["spec","error_code"],"additionalProperties":false}`

const Frequency = "steward.schedule-frequency/1"

type FrequencyFacts struct {
	Frequency string `json:"frequency"`
	Clock     string `json:"clock"`
	Weekday   string `json:"weekday"`
	Day       string `json:"day"`
}
type FrequencyResult struct {
	Spec      string `json:"spec"`
	ErrorCode string `json:"error_code"`
}

const FrequencyInputSchema = `{"type":"object","properties":{"frequency":{"type":"string","maxLength":16},"clock":{"type":"string","maxLength":5},"weekday":{"type":"string","maxLength":2},"day":{"type":"string","maxLength":2}},"required":["frequency","clock","weekday","day"],"additionalProperties":false}`
const FrequencyOutputSchema = LocalTimeOutputSchema

const Projection = "steward.schedule-editor/1"

type ProjectionFacts struct {
	Type     string `json:"trigger_type"`
	Spec     string `json:"trigger_spec"`
	Timezone string `json:"timezone"`
}
type ProjectionResult struct {
	Frequency string `json:"frequency"`
	Date      string `json:"date"`
	Clock     string `json:"clock"`
	Weekday   string `json:"weekday"`
	Day       string `json:"day"`
	ErrorCode string `json:"error_code"`
}

const ProjectionInputSchema = `{"type":"object","properties":{"trigger_type":{"type":"string","enum":["at","cron"]},"trigger_spec":{"type":"string","maxLength":256},"timezone":{"type":"string","maxLength":128}},"required":["trigger_type","trigger_spec","timezone"],"additionalProperties":false}`
const ProjectionOutputSchema = `{"type":"object","properties":{"frequency":{"type":"string","enum":["","once","daily","weekdays","weekly","monthly","custom"]},"date":{"type":"string","maxLength":10},"clock":{"type":"string","maxLength":5},"weekday":{"type":"string","maxLength":1},"day":{"type":"string","maxLength":2},"error_code":{"type":"string","enum":["","INVALID_ARGUMENT"]}},"required":["frequency","date","clock","weekday","day","error_code"],"additionalProperties":false}`

const Display = "steward.schedule-display/1"

type DisplayFacts = ProjectionFacts
type DisplayResult struct {
	FrequencyEnglish string `json:"frequency_en"`
	FrequencyChinese string `json:"frequency_zh"`
	TimeEnglish      string `json:"time_en"`
	TimeChinese      string `json:"time_zh"`
	ErrorCode        string `json:"error_code"`
}

const DisplayInputSchema = ProjectionInputSchema
const DisplayOutputSchema = `{"type":"object","properties":{"frequency_en":{"type":"string","maxLength":128},"frequency_zh":{"type":"string","maxLength":128},"time_en":{"type":"string","maxLength":512},"time_zh":{"type":"string","maxLength":512},"error_code":{"type":"string","enum":["","INVALID_ARGUMENT"]}},"required":["frequency_en","frequency_zh","time_en","time_zh","error_code"],"additionalProperties":false}`

const InstructionLabel = "steward.schedule-instruction-label/1"

type InstructionLabelFacts struct {
	Text string `json:"text"`
}
type InstructionLabelResult struct {
	Label string `json:"label"`
}

const InstructionLabelInputSchema = `{"type":"object","properties":{"text":{"type":"string","minLength":1,"maxLength":8192}},"required":["text"],"additionalProperties":false}`
const InstructionLabelOutputSchema = `{"type":"object","properties":{"label":{"type":"string","minLength":1,"maxLength":80}},"required":["label"],"additionalProperties":false}`

const Guidance = "steward.schedule-guidance/1"

type ObservationGuidance struct {
	ToolDescription   string `json:"tool_description"`
	InstructionPrefix string `json:"instruction_prefix"`
	InstructionSuffix string `json:"instruction_suffix"`
}

type GuidanceResult struct {
	Description string               `json:"description"`
	Observation *ObservationGuidance `json:"observation,omitempty"`
}

const GuidanceInputSchema = `{"type":"object","properties":{},"required":[],"additionalProperties":false}`
const GuidanceOutputSchema = `{"type":"object","properties":{"description":{"type":"string","minLength":1,"maxLength":4096},"observation":{"type":"object","properties":{"tool_description":{"type":"string","minLength":1,"maxLength":4096},"instruction_prefix":{"type":"string","minLength":1,"maxLength":4096},"instruction_suffix":{"type":"string","minLength":1,"maxLength":4096}},"required":["tool_description","instruction_prefix","instruction_suffix"],"additionalProperties":false}},"required":["description"],"additionalProperties":false}`

const Defaults = "steward.schedule-defaults/1"

type DefaultsResult struct {
	Frequency string `json:"frequency"`
	Date      string `json:"date"`
	Clock     string `json:"clock"`
	Timezone  string `json:"timezone"`
	Cron      string `json:"cron"`
	Weekday   string `json:"weekday"`
	Day       string `json:"day"`
	Enabled   bool   `json:"enabled"`
}

const DefaultsInputSchema = GuidanceInputSchema
const DefaultsOutputSchema = `{"type":"object","properties":{"frequency":{"type":"string","enum":["once","daily","weekdays","weekly","monthly","custom"]},"date":{"type":"string","maxLength":10},"clock":{"type":"string","minLength":1,"maxLength":5},"timezone":{"type":"string","minLength":1,"maxLength":128},"cron":{"type":"string","maxLength":256},"weekday":{"type":"string","maxLength":2},"day":{"type":"string","maxLength":2},"enabled":{"type":"boolean"}},"required":["frequency","date","clock","timezone","cron","weekday","day","enabled"],"additionalProperties":false}`

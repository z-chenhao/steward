package automation

import (
	"fmt"
	"strings"
	"unicode/utf8"
)

// OwnerConfiguration is the private owner editor command, separate from the
// model-visible management tool. Disabling a schedule never cancels its Task.
type OwnerConfiguration struct {
	Ref, Instruction, Type, Spec, Timezone string
	// PreserveSpec authenticates an unchanged exact editor instant on a new edit.
	PreserveSpec string
	Version      int64
	Enabled      bool
	Below        *int64
	// Unresolved Web minute; omitted so existing command hashes remain byte-stable.
	LocalDate  string          `json:",omitempty"`
	LocalClock string          `json:",omitempty"`
	Frequency  *OwnerFrequency `json:",omitempty"`
}

// OwnerFrequency is the accepted original editor input, not a new scheduling DSL.
type OwnerFrequency struct {
	Frequency string `json:"frequency"`
	Clock     string `json:"clock"`
	Weekday   string `json:"weekday"`
	Day       string `json:"day"`
}

type OwnerLocal struct {
	Date  string `json:"date"`
	Clock string `json:"clock"`
}

type OwnerReceipt struct {
	Ref     string
	Version int64
}

// ThresholdMinimumError reports the creating method's smallest reachable threshold.
type ThresholdMinimumError struct{ Minimum int64 }

func (e ThresholdMinimumError) Error() string {
	return fmt.Sprintf("below must be at least %d", e.Minimum)
}
func (e ThresholdMinimumError) Unwrap() error { return ErrInvalid }

// ValidateShape preserves owner command bounds without interpreting calendar policy.
func (q OwnerConfiguration) ValidateShape() error {
	if len(q.PreserveSpec) > 256 || (q.PreserveSpec != "" && (q.Ref == "" || q.Type != "at" || (q.PreserveSpec != q.Spec && q.LocalDate == ""))) {
		return ErrInvalid
	}
	if (q.Ref == "" && q.Version != 0) || (q.Ref != "" && (!strings.HasPrefix(q.Ref, "automation:") || len(q.Ref) > 256 || strings.ContainsAny(q.Ref, "/\\\x00\r\n") || q.Version < 1)) {
		return ErrInvalid
	}
	if strings.TrimSpace(q.Instruction) == "" || !utf8.ValidString(q.Instruction) || len(q.Instruction) > 8192 || len(q.Spec) > 256 || len(q.Timezone) > 128 || (q.Below != nil && (*q.Below < 0 || *q.Below > 1000000000000)) {
		return ErrInvalid
	}
	frequency := q.Frequency != nil
	if frequency && (q.Type != "cron" || q.Spec != "" || q.LocalDate != "" || q.LocalClock != "" || len(q.Frequency.Frequency) > 16 || len(q.Frequency.Clock) > 5 || len(q.Frequency.Weekday) > 2 || len(q.Frequency.Day) > 2) {
		return ErrInvalid
	}
	local := q.LocalDate != "" || q.LocalClock != ""
	if local && (q.Type != "at" || q.Spec != "" || q.LocalDate == "" || q.LocalClock == "" || len(q.LocalDate) > 10 || len(q.LocalClock) > 5) {
		return ErrInvalid
	}
	if (q.Type != "at" && q.Type != "cron") || (q.Spec == "" && !local && !frequency) || q.Timezone == "" || !utf8.ValidString(q.Spec) || !utf8.ValidString(q.Timezone) {
		return ErrInvalid
	}
	return nil
}

// SchedulePresentation is accepted display data, never HTML or control authority.
type SchedulePresentation struct {
	EditorDefaults   *EditorDefaults `json:"editor_defaults,omitempty"`
	InstructionLabel string          `json:"instruction_label,omitempty"`
	FrequencyEnglish string          `json:"frequency_en"`
	FrequencyChinese string          `json:"frequency_zh"`
	TimeEnglish      string          `json:"time_en"`
	TimeChinese      string          `json:"time_zh"`
}

// EditorDefaults contains only existing draft fields, never identities or authority.
type EditorDefaults struct {
	MinBelow  int64  `json:"min_below,omitempty"`
	Frequency string `json:"frequency"`
	Date      string `json:"date"`
	Clock     string `json:"clock"`
	Timezone  string `json:"timezone"`
	Cron      string `json:"cron"`
	Weekday   string `json:"weekday"`
	Day       string `json:"day"`
	Enabled   bool   `json:"enabled"`
}

func (v EditorDefaults) Validate() error {
	switch v.Frequency {
	case "once", "daily", "weekdays", "weekly", "monthly", "custom":
	default:
		return ErrInvalid
	}
	if v.MinBelow < 0 || v.MinBelow > 1000000000000 || len(v.Date) > 10 || v.Clock == "" || len(v.Clock) > 5 || v.Timezone == "" || len(v.Timezone) > 128 || len(v.Cron) > 256 || len(v.Weekday) > 2 || len(v.Day) > 2 {
		return ErrInvalid
	}
	return nil
}

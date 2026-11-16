// Package automation owns persisted scheduling intent, never Agent execution.
package automation

import (
	"encoding/json"
	"errors"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/z-chenhao/assistant/internal/tool"
)

const ManageName = "builtin__automation_manage"
const ObserveName = "builtin__automation_observe"
const Schema = `{"type":"object","properties":{"operation":{"enum":["create","update","cancel","list"]},"automationRef":{"type":"string"},"version":{"type":"integer","minimum":1},"instruction":{"type":"string","minLength":1,"maxLength":8192},"triggerType":{"enum":["at","cron"]},"triggerSpec":{"type":"string","maxLength":256},"timezone":{"type":"string","maxLength":128},"below":{"type":"integer","minimum":0,"maximum":1000000000000}},"required":["operation"],"additionalProperties":false}`
const ObserveSchema = `{"type":"object","properties":{"value":{"type":"integer","minimum":0,"maximum":1000000000000}},"required":["value"],"additionalProperties":false}`

var ErrInvalid = errors.New("invalid automation request")

type Request struct {
	Operation   string `json:"operation"`
	Ref         string `json:"automationRef,omitempty"`
	Version     int64  `json:"version,omitempty"`
	Instruction string `json:"instruction,omitempty"`
	Type        string `json:"triggerType,omitempty"`
	Spec        string `json:"triggerSpec,omitempty"`
	Timezone    string `json:"timezone,omitempty"`
	Below       *int64 `json:"below,omitempty"`
}
type View struct {
	Ref         string     `json:"automationRef"`
	Instruction string     `json:"instruction"`
	Type        string     `json:"triggerType"`
	Spec        string     `json:"triggerSpec"`
	Timezone    string     `json:"timezone"`
	Enabled     bool       `json:"enabled"`
	Version     int64      `json:"version"`
	Next        *time.Time `json:"nextFire,omitempty"`
	Below       *int64     `json:"below,omitempty"`
}

func Definitions() []tool.Definition {
	return []tool.Definition{
		{Host: true, Spec: tool.Spec{Name: ManageName, Description: legacyManageDescription(), Version: 1, InputSchema: Schema, Permission: "task_control", ReplayPolicy: "safe", OutputBytes: 131072}},
		{Host: true, Spec: tool.Spec{Name: ObserveName, Description: legacyObserveDescription(), Version: 1, InputSchema: ObserveSchema, Permission: "automation_observe", ReplayPolicy: "safe", Risk: "low", TimeoutMS: 10000, OutputBytes: 16384, Provider: "builtin", ConflictPolicy: "none"}},
	}
}

// DecodeRequest checks the command contract; calendar policy belongs to its selected method.
func DecodeRequest(raw string) (Request, error) {
	var q Request
	if !tool.Validate(Schema, raw) || json.Unmarshal([]byte(raw), &q) != nil {
		return q, ErrInvalid
	}
	q.Ref = tool.ObjectRef("automation", q.Ref)
	if q.Operation == "list" {
		if q.Ref != "" || q.Version != 0 || q.Instruction != "" || q.Type != "" || q.Spec != "" || q.Timezone != "" || q.Below != nil {
			return q, ErrInvalid
		}
		return q, nil
	}
	if q.Operation != "create" && (q.Ref == "" || q.Version < 1) {
		return q, ErrInvalid
	}
	if q.Operation == "cancel" {
		if q.Instruction != "" || q.Type != "" || q.Spec != "" || q.Timezone != "" || q.Below != nil {
			return q, ErrInvalid
		}
		return q, nil
	}
	if q.Operation == "create" && (q.Ref != "" || q.Version != 0) {
		return q, ErrInvalid
	}
	if strings.TrimSpace(q.Instruction) == "" || !utf8.ValidString(q.Instruction) || len(q.Instruction) > 8192 {
		return q, ErrInvalid
	}
	if (q.Type != "at" && q.Type != "cron") || q.Spec == "" || q.Timezone == "" {
		return q, ErrInvalid
	}
	return q, nil
}

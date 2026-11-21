package github

import (
	"encoding/json"
	"errors"

	"github.com/z-chenhao/assistant/internal/tool"
)

const bindSchema = `{"type":"object","properties":{"task_ref":{"type":"string","minLength":6,"maxLength":256},"task_epoch":{"type":"integer","minimum":1},"version":{"type":"integer","minimum":0},"mode":{"type":"string","enum":["observe","automatic"]},"event_kind":{"type":"string","enum":["review_comment","review","check_run","issue_comment"]},"check_conclusion":{"type":"string","enum":["failure"]}},"required":["task_ref","task_epoch","version"],"additionalProperties":false}`
const unbindSchema = `{"type":"object","properties":{"version":{"type":"integer","minimum":1}},"required":["version"],"additionalProperties":false}`

func ParseBinding(raw string, enabled bool) (BindingRequest, error) {
	var q BindingRequest
	schema := unbindSchema
	if enabled {
		schema = bindSchema
	}
	if !tool.Validate(schema, raw) || json.Unmarshal([]byte(raw), &q) != nil || !q.ValidRule() {
		return q, errors.New("invalid GitHub task binding request")
	}
	return q, nil
}

type BindingRequest struct {
	TaskRef         string `json:"task_ref"`
	TaskEpoch       int64  `json:"task_epoch"`
	Version         int64  `json:"version"`
	Mode            string `json:"mode,omitempty"`
	EventKind       string `json:"event_kind,omitempty"`
	CheckConclusion string `json:"check_conclusion,omitempty"`
}

func (q BindingRequest) ValidRule() bool {
	if q.CheckConclusion != "" && (q.CheckConclusion != "failure" || q.EventKind != "check_run") {
		return false
	}
	if q.Mode == "" && q.EventKind == "" {
		return true
	}
	return (q.Mode == "observe" || q.Mode == "automatic") &&
		(q.EventKind == "review_comment" || q.EventKind == "review" || q.EventKind == "check_run" || q.EventKind == "issue_comment")
}

type TaskBinding struct {
	TaskRef         string `json:"task_ref,omitempty"`
	TaskEpoch       int64  `json:"task_epoch,omitempty"`
	Version         int64  `json:"version"`
	State           string `json:"state"`
	LastSequence    int64  `json:"last_observation_sequence"`
	Scope           string `json:"source_scope,omitempty"`
	TaskGoal        string `json:"task_goal_preview,omitempty"`
	Generation      int64  `json:"rule_generation,omitempty"`
	Mode            string `json:"mode"`
	EventKind       string `json:"event_kind,omitempty"`
	CheckConclusion string `json:"check_conclusion,omitempty"`
}
